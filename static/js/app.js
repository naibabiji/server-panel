// 防止 401/428 并发请求时重复触发跳转：第一个命中时置位，跳转后页面卸载，
// 后续并发的同状态码分支只抛 silent 错误，不再重复改 location.href。
let _authRedirecting = false;

// --- 浏览器侧 API 缓存（sessionStorage）---
// 覆盖字典（极少变，长 TTL）和列表（含 agent 上报的实时状态字段，短 TTL 兜底）。
// mutation 由 api() 统一失效，避免读到陈旧数据。
const API_CACHE_PREFIX = 'spApiCache:';
const API_CACHE_TTL = 5 * 60 * 1000;        // 默认 5 分钟（字典等极少变的数据）
const API_CACHE_TTL_LIST = 60 * 1000;       // 列表 60 秒（含 is_online/last_seen 等实时字段，60s 内可能略滞后于 agent 上报）

function apiCacheKey(path) { return API_CACHE_PREFIX + path; }

function apiCacheGet(path, ttl) {
    try {
        const raw = sessionStorage.getItem(apiCacheKey(path));
        if (!raw) return null;
        const entry = JSON.parse(raw);
        if (Date.now() - entry.t > ttl) { sessionStorage.removeItem(apiCacheKey(path)); return null; }
        return entry.data;
    } catch (e) { return null; }
}

function apiCacheSet(path, data) {
    try { sessionStorage.setItem(apiCacheKey(path), JSON.stringify({ t: Date.now(), data })); } catch (e) {}
}

// 清掉某个前缀下的所有缓存项：匹配 path 本身、path/...、path?...
function apiCacheDropPrefix(pathPrefix) {
    try {
        const toRemove = [];
        for (let i = 0; i < sessionStorage.length; i++) {
            const k = sessionStorage.key(i);
            if (!k || k.indexOf(API_CACHE_PREFIX) !== 0) continue;
            const p = k.slice(API_CACHE_PREFIX.length);
            if (p === pathPrefix || p.indexOf(pathPrefix + '/') === 0 || p.indexOf(pathPrefix + '?') === 0) {
                toRemove.push(k);
            }
        }
        toRemove.forEach(k => sessionStorage.removeItem(k));
    } catch (e) {}
}

// mutation 成功后按资源前缀失效相关 GET 缓存。
// 服务器变更会顺带失效网站列表（website 列表 JOIN servers 取 server_name）。
function apiCacheInvalidate(path) {
    if (path === '/api/settings/os-list') apiCacheDropPrefix('/api/settings/os-list');
    else if (path === '/api/settings/site-type-list') apiCacheDropPrefix('/api/settings/site-type-list');
    else if (path.indexOf('/api/servers') === 0) { apiCacheDropPrefix('/api/servers'); apiCacheDropPrefix('/api/websites'); }
    else if (path.indexOf('/api/websites') === 0) apiCacheDropPrefix('/api/websites');
}

// opt-in 的 GET 缓存封装：命中缓存直接返回，否则走 api() 并写入缓存。
// 非 GET 不要用这个——直接用 api()，api() 会在成功后自动失效相关缓存。
// cacheOpts.ttl：自定义有效期（列表用短 TTL，避免实时状态字段陈旧）。
async function cachedApi(path, options = {}, cacheOpts = {}) {
    const method = (options.method || 'GET').toUpperCase();
    if (method !== 'GET') return api(path, options);
    const ttl = cacheOpts.ttl != null ? cacheOpts.ttl : API_CACHE_TTL;
    const cached = apiCacheGet(path, ttl);
    if (cached) return cached;
    const data = await api(path, options);
    apiCacheSet(path, data);
    return data;
}

function api(path, options = {}) {
    const prefix = document.body.dataset.panelPrefix || '';
    const apiPath = path.startsWith('/api/') ? path : '/api' + path;
    const url = prefix + apiPath;

    const headers = {
        'X-CSRF-Token': document.querySelector('meta[name="csrf-token"]')?.content || '',
        ...options.headers,
    };

    if (options.body && typeof options.body === 'object' && !(options.body instanceof FormData)) {
        headers['Content-Type'] = 'application/json';
        options.body = JSON.stringify(options.body);
    }

    return fetch(url, { ...options, headers })
        .then(async (resp) => {
            if (resp.status === 401 && !path.startsWith('/api/auth/login')) {
                // 会话过期：跳转 /login。这里必须 throw（而非返回永不 settle 的
                // Promise），否则 Promise.all 会被一个挂起的 Promise 永久拖死。
                // silent 标记让 .catch 不再弹 toast（跳转本身已是反馈）。
                if (!_authRedirecting) {
                    _authRedirecting = true;
                    window.location.href = prefix + '/login';
                    // 真跳转时页面会卸载，定时器不会执行；若因某种原因未卸载，
                    // 定时器复位 flag，避免后续真正的 428/401 被误判为已在跳转。
                    setTimeout(() => { _authRedirecting = false; }, 0);
                }
                const e = new Error('会话已过期，正在跳转登录');
                e.silent = true;
                throw e;
            }
            if (resp.status === 503) {
                throw new Error('Service busy, please retry later');
            }
            if (resp.status === 428) {
                // 查看密码未设置：跳转设置页。同样必须 throw 让 Promise 能 settle，
                // 不能用 new Promise(() => {}) 挂起（否则首登用户会被骨架屏永久卡死）。
                if (!_authRedirecting) {
                    _authRedirecting = true;
                    window.location.href = prefix + '/settings?view_password_required=1#security';
                    // 已在 /settings 时这是同页 no-op 跳转（不会卸载），定时器复位 flag，
                    // 避免后续 401 因 flag 卡住而跳不到 /login。
                    setTimeout(() => { _authRedirecting = false; }, 0);
                }
                const e = new Error('请先设置查看密码');
                e.silent = true;
                throw e;
            }
            const contentType = resp.headers.get('content-type') || '';
            if (!contentType.includes('application/json')) {
                const text = await resp.text();
                console.error('Non-JSON response:', resp.status, text.substring(0, 200));
                throw new Error('服务器返回异常 (' + resp.status + ')');
            }
            const data = await resp.json();
            if (!resp.ok) {
                console.error('API error:', resp.status, data);
                throw new Error(data.message || 'Request failed (' + resp.status + ')');
            }
            if (!data.success) {
                const err = new Error(data.message || '操作失败');
                if (data.conflicts) err.conflicts = data.conflicts;
                throw err;
            }
            // mutation 成功后失效相关 GET 缓存（字典/列表），避免读到陈旧数据。
            const _method = (options.method || 'GET').toUpperCase();
            if (_method !== 'GET') apiCacheInvalidate(path);
            return data;
        })
        .catch(err => {
            console.error('Fetch failed:', err.message, 'URL:', url);
            if (!err.silent) showToast(err.message, 'error');
            throw err;
        });
}

function redirectToViewPasswordSettings() {
    const prefix = document.body.dataset.panelPrefix || '';
    window.location.href = prefix + '/settings?view_password_required=1#security';
}

async function unlockViewPasswordPrompt() {
    const status = await api('/api/view-password/status');
    if (!status.data.is_setup) {
        redirectToViewPasswordSettings();
        return '';
    }

    const pw = await passwordPromptModal('请输入查看密码');
    if (!pw) return '';
    const d = await api('/api/view-password/unlock', {method:'POST', body:{password:pw}});
    return d.data.view_token || '';
}

function showViewPasswordRequiredNotice() {
    const params = new URLSearchParams(window.location.search);
    if (params.get('view_password_required') === '1') {
        showToast('请先设置查看密码。密码不支持找回，连续输错 5 次会自动清空已保存的服务器/网站敏感凭据。', 'warning');
    }
}

document.addEventListener('DOMContentLoaded', showViewPasswordRequiredNotice);

function formatBytes(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

function fmtTime(t) {
    if (!t) return '--';
    let s = String(t).trim();
    // 归一化为带时区的 ISO：无时区后缀则按 UTC 处理，避免被浏览器当作本地时间。
    if (!/[Zz]|[+-]\d{2}:?\d{2}$/.test(s)) {
        s = s.replace(' ', 'T') + 'Z';
    }
    const d = new Date(s);
    if (isNaN(d.getTime())) return '--';
    const p = n => String(n).padStart(2, '0');
    return `${d.getUTCFullYear()}-${p(d.getUTCMonth() + 1)}-${p(d.getUTCDate())} `
         + `${p(d.getUTCHours())}:${p(d.getUTCMinutes())}:${p(d.getUTCSeconds())}`;
}

function paginationState(pageSize = 30) {
    return {
        page: 1,
        pageSize,
        totalPages: 1,
        pageStart: 0,
        pageEnd: 0,
        total: 0,
        recalcPagination() {
            this.totalPages = Math.max(1, Math.ceil((this.total || 0) / this.pageSize));
            this.pageStart = this.total ? (this.page - 1) * this.pageSize + 1 : 0;
            this.pageEnd = Math.min(this.total || 0, this.page * this.pageSize);
        },
        pageParams() {
            return 'page=' + encodeURIComponent(this.page) + '&page_size=' + encodeURIComponent(this.pageSize);
        },
        setPagination(data) {
            this.total = data.total || 0;
            this.page = data.page || this.page;
            this.pageSize = data.page_size || this.pageSize;
            this.recalcPagination();
        },
        async resetAndFetch() {
            this.page = 1;
            await this.fetch();
        },
        async prevPage() {
            if (this.page <= 1) return;
            this.page--;
            await this.fetch();
        },
        async nextPage() {
            if (this.page >= this.totalPages) return;
            this.page++;
            await this.fetch();
        },
        async goPage(page) {
            const n = parseInt(page, 10);
            if (!n || n < 1 || n > this.totalPages || n === this.page) return;
            this.page = n;
            await this.fetch();
        },
        async ensurePageHasItems(items) {
            if (this.page > 1 && this.total > 0 && (!items || items.length === 0)) {
                this.page = Math.min(this.page - 1, this.totalPages);
                await this.fetch();
            }
        }
    };
}

function formatUptime(seconds) {
    const d = Math.floor(seconds / 86400);
    const h = Math.floor((seconds % 86400) / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    const parts = [];
    if (d > 0) parts.push(d + 'd');
    if (h > 0) parts.push(h + 'h');
    if (m > 0) parts.push(m + 'm');
    return parts.join(' ') || '<1m';
}

function showToast(message, type = 'info') {
    const colors = {
        success: 'background:#065f46;border-color:#059669;color:#a7f3d0;',
        error: 'background:#991b1b;border-color:#dc2626;color:#fecaca;',
        warning: 'background:#78350f;border-color:#d97706;color:#fde68a;',
        info: 'background:#1e3a5f;border-color:#2563eb;color:#bfdbfe;',
    };
    const toast = document.createElement('div');
    toast.style.cssText = 'position:fixed;bottom:24px;left:50%;transform:translateX(-50%);z-index:9999;padding:12px 24px;border-radius:8px;border:1px solid;box-shadow:0 4px 12px rgba(0,0,0,0.3);transition:opacity 0.3s;' + (colors[type] || colors.info);
    toast.textContent = message;
    document.body.appendChild(toast);
    setTimeout(() => {
        toast.style.opacity = '0';
        setTimeout(() => toast.remove(), 300);
    }, 5000);
}

function inputModal(options = {}) {
    return new Promise((resolve) => {
        const overlay = document.createElement('div');
        overlay.style.cssText = 'position:fixed;inset:0;background:rgba(0,0,0,0.72);display:flex;align-items:center;justify-content:center;z-index:9999;padding:16px;';

        const dialog = document.createElement('div');
        dialog.style.cssText = 'background:#111827;border:1px solid #374151;border-radius:8px;box-shadow:0 24px 60px rgba(0,0,0,0.45);max-width:28rem;width:100%;padding:20px;';

        const title = document.createElement('div');
        title.style.cssText = 'color:#f9fafb;font-size:16px;font-weight:600;margin-bottom:6px;';
        title.textContent = options.title || '请输入';
        dialog.appendChild(title);

        if (options.message) {
            const message = document.createElement('p');
            message.style.cssText = 'color:#9ca3af;font-size:13px;line-height:1.5;margin:0 0 14px;';
            message.textContent = options.message;
            dialog.appendChild(message);
        }

        const label = document.createElement('label');
        label.style.cssText = 'display:block;color:#d1d5db;font-size:13px;margin-bottom:6px;';
        label.textContent = options.label || title.textContent;
        dialog.appendChild(label);

        const input = document.createElement('input');
        input.type = options.type || 'text';
        input.value = options.defaultValue || '';
        input.placeholder = options.placeholder || '';
        if (options.autocomplete) input.autocomplete = options.autocomplete;
        input.style.cssText = 'width:100%;box-sizing:border-box;background:#0d1117;border:1px solid #374151;border-radius:8px;color:#f9fafb;padding:10px 12px;font-size:14px;outline:none;';
        input.onfocus = () => { input.style.borderColor = '#8b5cf6'; input.style.boxShadow = '0 0 0 3px rgba(139,92,246,0.18)'; };
        input.onblur = () => { input.style.borderColor = '#374151'; input.style.boxShadow = 'none'; };

        // 密码字段：用 text + CSS 掩码(-webkit-text-security:disc)替代 type=password。
        // type=password 即使加 autocomplete=new-password，Chrome 等仍会弹"已存密码"
        // 自动填充提示；改成 text 后浏览器根本不识别为密码字段 → 不弹、不存、不填。
        // 无 <form>/无配对用户名，并对第三方密码管理器加 ignore 属性。附显隐开关便于核对。
        if (input.type === 'password') {
            input.type = 'text';
            input.style.webkitTextSecurity = 'disc';
            input.autocomplete = 'off';
            input.setAttribute('data-lpignore', 'true');
            input.setAttribute('data-1p-ignore', '');
            input.setAttribute('data-form-type', 'other');
            input.style.paddingRight = '44px';

            const wrap = document.createElement('div');
            wrap.style.cssText = 'position:relative;';
            const toggle = document.createElement('button');
            toggle.type = 'button';
            toggle.tabIndex = -1;
            toggle.textContent = '显示';
            toggle.style.cssText = 'position:absolute;right:6px;top:50%;transform:translateY(-50%);background:transparent;border:none;color:#9ca3af;cursor:pointer;font-size:12px;padding:2px 4px;';
            toggle.onclick = () => {
                const showing = input.style.webkitTextSecurity === '';
                input.style.webkitTextSecurity = showing ? 'disc' : '';
                toggle.textContent = showing ? '显示' : '隐藏';
                input.focus();
            };
            wrap.appendChild(input);
            wrap.appendChild(toggle);
            dialog.appendChild(wrap);
        } else {
            dialog.appendChild(input);
        }

        const actions = document.createElement('div');
        actions.style.cssText = 'display:flex;justify-content:flex-end;gap:10px;margin-top:18px;';

        const cancel = document.createElement('button');
        cancel.type = 'button';
        cancel.style.cssText = 'background:#374151;color:#f3f4f6;border:none;padding:8px 14px;border-radius:8px;cursor:pointer;font-size:14px;';
        cancel.textContent = options.cancelText || '取消';

        const confirm = document.createElement('button');
        confirm.type = 'button';
        confirm.style.cssText = 'background:#7c3aed;color:#fff;border:none;padding:8px 14px;border-radius:8px;cursor:pointer;font-size:14px;';
        confirm.textContent = options.confirmText || '确认';

        actions.appendChild(cancel);
        actions.appendChild(confirm);
        dialog.appendChild(actions);
        overlay.appendChild(dialog);

        const close = (value) => {
            overlay.remove();
            document.removeEventListener('keydown', onKeydown);
            resolve(value);
        };
        const submit = () => close(input.value);
        const onKeydown = (e) => {
            if (e.key === 'Escape') close(null);
            if (e.key === 'Enter') submit();
        };

        cancel.onclick = () => close(null);
        confirm.onclick = submit;
        overlay.onclick = (e) => { if (e.target === overlay) close(null); };
        document.addEventListener('keydown', onKeydown);
        document.body.appendChild(overlay);
        requestAnimationFrame(() => input.focus());
    });
}

function passwordPromptModal(label, options = {}) {
    return inputModal({
        title: options.title || '密码验证',
        label,
        message: options.message || '',
        // type=password 由 inputModal 内部转为 text + CSS 掩码，让浏览器不识别为
        // 密码字段，从而不弹自动填充/保存提示（详见 inputModal 密码分支）。
        type: 'password',
        autocomplete: options.autocomplete || 'new-password',
        placeholder: options.placeholder || '',
        confirmText: options.confirmText || '确认',
    });
}

function confirmModal(message) {
    return new Promise((resolve) => {
        const overlay = document.createElement('div');
        overlay.style.cssText = 'position:fixed;inset:0;background:rgba(0,0,0,0.75);display:flex;align-items:center;justify-content:center;z-index:9999;';
        overlay.innerHTML = `
            <div style="background:#1f2937;border-radius:12px;border:1px solid #374151;padding:24px;max-width:32rem;width:100%;margin:0 16px;">
                <p id="modal-message" style="color:#e5e7eb;margin-bottom:16px;"></p>
                <div style="display:flex;justify-content:flex-end;gap:12px;">
                    <button id="modal-cancel" style="background:#4b5563;color:#fff;border:none;padding:8px 16px;border-radius:8px;cursor:pointer;font-size:14px;">取消</button>
                    <button id="modal-confirm" style="background:#dc2626;color:#fff;border:none;padding:8px 16px;border-radius:8px;cursor:pointer;font-size:14px;">确认</button>
                </div>
            </div>
        `;
        overlay.querySelector('#modal-message').textContent = message;
        document.body.appendChild(overlay);
        overlay.querySelector('#modal-cancel').onclick = () => { overlay.remove(); resolve(false); };
        overlay.querySelector('#modal-confirm').onclick = () => { overlay.remove(); resolve(true); };
        overlay.onclick = (e) => { if (e.target === overlay) { overlay.remove(); resolve(false); } };
    });
}

// Shared CPU/memory/load chart rendering, used by both
// templates/monitor_detail.html (managed servers) and templates/dashboard.html
// (the panel's own host) against the same MetricPoint JSON shape
// ({t, cpu, mem, disk, load1, load5, load15, rx, tx}), each with its own
// #cpuChart/#memChart/#loadChart canvases (never on screen at the same time,
// so reusing those element ids across pages is fine).
let charts = {};

function destroyCharts() {
    Object.values(charts).forEach(c => c.destroy());
    charts = {};
}

function hexToRGBA(hex, alpha) {
    const n = parseInt(hex.replace('#', ''), 16);
    return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`;
}

// Load 不是百分比，仍然按数据范围给一个规整上限。
function niceLoadAxisMax(values) {
    const nums = (values || []).filter(v => typeof v === 'number' && isFinite(v));
    const max = nums.length ? Math.max(...nums) : 0;
    const floor = 1;
    let target = Math.max(max * 1.25, floor);
    const step = target > 10 ? 1 : 0.5;
    return Math.ceil(target / step) * step;
}

// compact renders a minimal "sparkline" (no axes/legend, thin line, no
// points) for tight spaces like the Dashboard's per-metric cards, instead
// of the full interactive chart templates/monitor_detail.html uses - same
// data, same shared function, just a different presentation.
function makeChart(canvasId, label, color, compact) {
    const ctx = document.getElementById(canvasId);
    if (!ctx) return null;
    return new Chart(ctx, {
        type: 'line',
        data: { labels: [], datasets: [{
            label, data: [],
            borderColor: color, borderWidth: compact ? 1.5 : 2, tension: 0.3,
            fill: true, backgroundColor: hexToRGBA(color, 0.12),
            pointRadius: compact ? 0 : (c) => c.dataIndex === c.dataset.data.length - 1 ? 4 : 0,
            pointHoverRadius: compact ? 0 : 5, pointHitRadius: compact ? 0 : 12,
            pointBackgroundColor: color, pointBorderColor: '#171b21', pointBorderWidth: 2
        }] },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            interaction: { mode: 'index', intersect: false },
            layout: compact ? { padding: 0 } : undefined,
            plugins: {
                legend: { display: !compact, labels: { color: '#a1a1aa', font: { size: 11 } } },
                tooltip: compact ? { enabled: false } : {
                    backgroundColor: '#11151a', borderColor: '#2a3038', borderWidth: 1,
                    titleColor: '#e5e7eb', bodyColor: '#e5e7eb', padding: 10, displayColors: false
                }
            },
            scales: {
                x: { display: !compact, ticks: { color: '#52525b', maxTicksLimit: 10 }, grid: { color: '#27272a' } },
                y: { display: !compact, ticks: { color: '#52525b' }, grid: { color: '#27272a' }, beginAtZero: true }
            }
        }
    });
}

function updateCharts(points, compact) {
    destroyCharts();
    if (!points || !points.length) return;

    const labels = points.map(p => fmtTime(p.t));

    charts.cpu = makeChart('cpuChart', 'CPU %', '#a78bfa', compact);
    if (charts.cpu) {
        const data = points.map(p => p.cpu);
        charts.cpu.data.labels = labels;
        charts.cpu.data.datasets[0].data = data;
        charts.cpu.options.scales.y.min = 0;
        charts.cpu.options.scales.y.max = 100;
        charts.cpu.update();
    }

    charts.mem = makeChart('memChart', 'Memory %', '#4ade80', compact);
    if (charts.mem) {
        const data = points.map(p => p.mem);
        charts.mem.data.labels = labels;
        charts.mem.data.datasets[0].data = data;
        charts.mem.options.scales.y.min = 0;
        charts.mem.options.scales.y.max = 100;
        charts.mem.update();
    }

    charts.load = makeChart('loadChart', 'Load 1m', '#fbbf24', compact);
    if (charts.load) {
        const data = points.map(p => p.load1);
        charts.load.data.labels = labels;
        charts.load.data.datasets[0].data = data;
        charts.load.options.scales.y.suggestedMax = niceLoadAxisMax(data);
        charts.load.update();
    }
}
