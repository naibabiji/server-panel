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
                window.location.href = prefix + '/login';
                throw new Error('Unauthorized');
            }
            if (resp.status === 503) {
                throw new Error('Service busy, please retry later');
            }
            if (resp.status === 428) {
                window.location.href = prefix + '/settings?view_password_required=1#security';
                throw new Error('请先设置查看密码');
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
            return data;
        })
        .catch(err => {
            if (err.message !== 'Unauthorized') {
                console.error('Fetch failed:', err.message, 'URL:', url);
                showToast(err.message, 'error');
            }
            throw err;
        });
}

function redirectToViewPasswordSettings() {
    const prefix = document.body.dataset.panelPrefix || '';
    window.location.href = prefix + '/settings?view_password_required=1#security';
}

async function requireViewPasswordBeforeCreate() {
    try {
        const d = await api('/api/view-password/status');
        if (!d.data.is_setup) {
            redirectToViewPasswordSettings();
            return false;
        }
        return true;
    } catch (e) {
        return false;
    }
}

async function unlockViewPasswordPrompt() {
    const status = await api('/api/view-password/status');
    if (!status.data.is_setup) {
        redirectToViewPasswordSettings();
        return '';
    }

    const pw = prompt('请输入查看密码:');
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
    return new Date(t.replace(' ', 'T')).toLocaleString('zh-CN');
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
