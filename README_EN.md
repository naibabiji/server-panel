# Server Panel — Self-Hosted VPS Asset Management & Server Monitoring

> 🌐 **中文文档：**[阅读完整简体中文说明 →](README.md)

**English documentation**

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-single%20binary-00ADD8?logo=go)](go.mod)
[![Platform](https://img.shields.io/badge/Linux-amd64%20%7C%20arm64-FCC624?logo=linux&logoColor=black)](#requirements)

Server Panel is a security-focused, lightweight, open-source server management panel for independent developers, website owners, hosting operators, and small infrastructure teams. It brings **VPS and dedicated-server assets, websites, customers, providers, renewals, Agent-based performance monitoring, alerts, backups, and everyday file operations** into one private dashboard.

Built with Go and SQLite, Server Panel deploys as a single binary and includes a bilingual Chinese/English UI, built-in HTTPS, layered login protection, and encrypted credential storage. It is a practical self-hosted choice for anyone looking for a VPS management panel, server asset inventory, website expiry reminder, or lightweight Linux server monitoring dashboard without sending credentials and customer data to a third-party SaaS.

> Debian 13 is the primary development and test platform. Other Debian/Ubuntu systems using systemd may work, but are not guaranteed to be fully compatible.

## Security Is a Core Design Goal

Server Panel addresses two separate questions: **How difficult is it to break into the panel, and how difficult is it to obtain stored credentials even after gaining dashboard access?**

| Attack stage | Defenses an attacker must cross | Limits that remain after a breach |
|---|---|---|
| Discover and enter the panel | Randomized URL path, HTTPS, optional BasicAuth, independent panel login, failed-attempt bans, malicious scan detection, and nftables IPv4/IPv6 integration | Entry-point access or a normal dashboard session still cannot directly read stored secrets |
| Read sensitive data | Independent view password, bcrypt verification, AES-256-GCM encryption, single-use tokens bound to the session and source IP, and a two-minute lifetime | Five consecutive failures erase stored server and website password copies instead of allowing unlimited guessing |

In other words, **dashboard access is not secret-access permission**. An attacker who obtains a login password, browser session, or ordinary dashboard access must still defeat a second independent authorization layer before sensitive-data endpoints will return credentials. See the full [Security Model](#security-model) below.

## Highlights

- **Centralized asset inventory:** manage servers, websites, customers, and providers, including ownership, location, pricing, currency, purchase dates, and expiry dates.
- **Agent-based monitoring:** collect CPU, memory, disk, network, load, uptime, and heartbeat data with fleet and per-server history views.
- **Availability and renewal tracking:** HTTP probes, Agent offline detection, server reachability checks, server/website expiry reminders, and recurring renewal-date advancement.
- **Flexible alerts:** rules for CPU, memory, disk, offline servers, failed HTTP probes, and expiring assets, with SMTP email notifications.
- **Encrypted secrets:** protect SSH passwords, control-panel passwords, website credentials, private provider notes, and Agent keys.
- **Files and local storage:** upload, download, copy, move, rename, compress, and extract inside restricted roots; inspect, mount, unmount, or initialize local data disks.
- **Backup and restore:** create manual or scheduled backups containing both SQLite data and the encryption key, with retention, optional email delivery, and upload restore.
- **Safe maintenance:** signed panel updates with health checks and rollback, automatic update policies, and Debian/Ubuntu package update support.
- **Bilingual interface:** switch between Simplified Chinese and English from the login page or dashboard.

## Quick Install

Run as `root` on a Debian/Ubuntu server using systemd:

```bash
curl -fsSL https://raw.githubusercontent.com/naibabiji/server-panel/master/install.sh | bash
```

The installer downloads the latest `amd64` or `arm64` release and creates a self-signed TLS certificate, randomized URL path, panel account, BasicAuth credentials, and systemd service.

Open the generated URL after installation:

```text
https://your-server:8444/<random-path>/login
```

Credentials are printed in the installation log. The installer opens the HTTPS port when UFW or firewalld is already active. If your provider uses a cloud firewall or security group, allow the matching TCP port there as well (default: `8444`).

## Screenshots

<table>
  <tr>
    <td width="50%" align="center"><a href="screenshots/dashboard.png"><img src="screenshots/dashboard.png" width="100%" alt="Server Panel server management dashboard"></a><br><sub>Assets, Agents, expirations, and alerts</sub></td>
    <td width="50%" align="center"><a href="screenshots/servers.png"><img src="screenshots/servers.png" width="100%" alt="VPS and server asset management"></a><br><sub>Server asset inventory</sub></td>
  </tr>
  <tr>
    <td width="50%" align="center"><a href="screenshots/servers-2.png"><img src="screenshots/servers-2.png" width="100%" alt="Server details and encrypted credentials"></a><br><sub>Configuration, renewals, Agent, and credentials</sub></td>
    <td width="50%" align="center"><a href="screenshots/websites.png"><img src="screenshots/websites.png" width="100%" alt="Website and domain expiry management"></a><br><sub>Websites, domains, and expiry tracking</sub></td>
  </tr>
  <tr>
    <td width="50%" align="center"><a href="screenshots/monitor.png"><img src="screenshots/monitor.png" width="100%" alt="Multi-server Agent performance monitoring"></a><br><sub>Fleet monitoring overview</sub></td>
    <td width="50%" align="center"><a href="screenshots/monitor-2.png"><img src="screenshots/monitor-2.png" width="100%" alt="CPU memory disk and network monitoring charts"></a><br><sub>CPU, memory, disk, network, and load history</sub></td>
  </tr>
  <tr>
    <td width="50%" align="center"><a href="screenshots/settings.png"><img src="screenshots/settings.png" width="100%" alt="Server Panel settings"></a><br><sub>Access, security, backup, notification, and update settings</sub></td>
    <td width="50%"></td>
  </tr>
</table>

## Features in Detail

### Servers, Websites, Customers, and Providers

- Track VPS instances, dedicated servers, shared hosting, and other server assets.
- Store IP address, operating system, CPU, memory, disk, bandwidth, location, SSH/control-panel details, dates, renewal cycle, price, and status.
- Link servers to customers and providers, and websites to servers and customers; detail pages expose related records.
- Search, filter, and paginate records. Operating-system and website-type dictionaries are configurable.
- Enable HTTP probing, automatic renewal-date advancement, and an independent Agent installation key per server.

### Monitoring and Alerts

- The Agent reports CPU, memory, disk, network, load, uptime, version, and heartbeat data.
- The fleet view groups servers by provider; individual server pages show historical charts.
- The panel also collects its own host metrics and deletes old history according to a configurable retention period.
- Alert rules cover Agent offline events, high CPU/memory/disk usage, failed HTTP probes, and server or website expiration.
- SMTP testing, email notifications, alert history, and resolution state are included.

### Files, Disks, and Operations

- The file manager is limited to the panel data directory, mounted storage, and restricted roots explicitly added by an administrator.
- Upload, download, create directories, rename, delete, copy, move, compress, and extract files, with important actions logged.
- Inspect disks and partitions; mount, unmount, check user permissions, format or initialize data disks, and maintain automatic mounts.
- Review panel operation logs and background task state from Settings.

> Formatting or initializing a disk erases target data. Verify the device path and keep an independent backup before using these operations.

### HTTPS, Updates, and Backups

- Use a self-signed certificate, upload your own certificate, or issue a Let's Encrypt certificate through ACME.
- Panel updates include download, signature/checksum verification, database and binary backup, replacement, restart, health check, and automatic rollback on failure.
- Schedule daily or weekly backups, configure retention, and optionally deliver backups over SMTP when they fit the configured size limit.
- Restore the database and encryption key from a local backup or an uploaded `.tar.gz` archive.

## Security Model

### 1. What an attacker must defeat to enter the panel

Server Panel does not place its entire security boundary behind a single login form. An attacker encounters multiple layers:

1. **Discover the entry point:** installation generates a randomized URL path, so common `/admin`, `/login`, and vulnerability-scanner paths do not expose the panel directly.
2. **Pass entry authentication:** optional BasicAuth sits in front of the panel login and uses separate credentials.
3. **Avoid automatic bans:** repeated entry or panel login failures are recorded by source IP and trigger bans; malicious unknown-path scans also activate the defense.
4. **Bypass ban enforcement:** ban records are always stored in the panel database, and banned sources receive `403` when accessing management-panel paths. If a working `nft` command is present, the panel process has firewall-management privileges, and the `sp_filter` table, IPv4/IPv6 sets, and drop rules are all created successfully, banned sources are additionally dropped by nftables on the panel TCP port. If any initialization step fails, the panel does not report network-level protection as enabled.
5. **Forge a valid session:** authenticated operations use server-side sessions, HttpOnly/Secure cookies, a 30-minute sliding lifetime, and CSRF validation.
6. **Defeat client-IP validation:** only explicitly trusted reverse proxies or verified Cloudflare networks may supply the real client IP, reducing forwarded-header spoofing opportunities.

No Internet service can be promised to be “impossible to breach,” but these controls materially increase the cost of automated scanning, credential guessing, session abuse, and sustained brute-force attacks.

> **nftables deployment boundary:** on Debian 13, the primary target platform, the one-click installer installs nftables through APT. At startup the panel creates separate IPv4/IPv6 ban sets and drop rules for the panel port. DNF/YUM systems also attempt to install nftables; if the distribution repository does not provide it, installation continues with an explicit warning. Authentication and application-level bans remain active, but the administrator must then supply network-layer protection through the host firewall or cloud security group. The installer does not impose a global default-deny policy: it manages only the panel's own ban table and opens the panel port in an already active UFW or firewalld configuration.

### 2. Why dashboard access still does not reveal stored secrets

Server SSH passwords, server/website control-panel passwords, and private provider notes are not returned merely because a user logged in:

1. **Independent view password:** it is separate from the entry and panel login credentials, and its hash is stored with bcrypt cost 12.
2. **Encryption at rest:** sensitive values use AES-256-GCM. The database stores randomized-nonce ciphertext rather than directly readable plaintext.
3. **Short-lived single-use authorization:** successful view-password verification creates a one-time token bound to the current server-side session and source IP. It expires after two minutes and is consumed on first use.
4. **Endpoint-level enforcement:** sensitive-data endpoints require and consume a valid view token in addition to checking the login session.
5. **Brute-force damage control:** five consecutive incorrect view-password attempts from a source erase stored server SSH, server control-panel, and website control-panel password copies and revoke view tokens. Server, website, customer, monitoring, and expiry records remain intact.
6. **Agent isolation:** every Agent has an independent key, old keys become invalid after regeneration, and Agents only submit monitoring data—they do not read stored passwords.

This means **stealing a normal panel account or authenticated browser session does not directly enable a credential export**. The attacker must also obtain the independent view password before triggering erasure and satisfy the token, session, source-IP, and expiration constraints.

### The security boundary that must be understood

If an attacker gains `root` on the panel host, can read arbitrary panel process memory, or can replace the running binary, the host is fully compromised. Because the panel must decrypt credentials for legitimate users, no software can honestly promise secrecy at that privilege level. Isolate the host immediately, rotate every credential, and rebuild from a trusted backup.

Backups are also sensitive assets. A full backup contains both the database and encryption key so credentials remain recoverable after disaster restoration. Restrict backup file permissions and keep off-site copies in a separate trusted location.

> Self-hosting does not make a service automatically secure. Use strong passwords and HTTPS, and place the panel behind a cloud firewall, VPN, Tailscale, WireGuard, Cloudflare, or a trusted reverse proxy when appropriate.

## Installation and Maintenance

### Requirements

- Linux with systemd (Debian 13 is the primary target)
- `amd64` or `arm64`
- `root` privileges for installation
- Access to GitHub Releases; an optional temporary GitHub proxy can be supplied when generating an Agent installer

Running the installer over an existing installation preserves configuration, database, certificates, and credentials while replacing the binary. To regenerate configuration and login details:

```bash
curl -fsSL https://raw.githubusercontent.com/naibabiji/server-panel/master/install.sh | INSTALL_MODE=reinstall bash
```

For an offline installation, place `install.sh` and the matching release binary in the same directory, then run `bash install.sh`.

Common commands:

```bash
systemctl status server-panel
systemctl restart server-panel
journalctl -u server-panel -f
server-panel --reset-password
```

Restore a full backup from the command line:

```bash
systemctl stop server-panel
server-panel -config /www/server/server-panel/config.json -restore-backup=/path/to/server-panel-backup.<timestamp>.tar.gz
systemctl start server-panel
```

## Intended Use and Limitations

Server Panel fits individuals managing multiple VPS or dedicated servers, small teams tracking customer websites, providers, costs, and renewals, and developers who want to self-host their monitoring and operations records.

It is not a large multi-tenant cloud platform and does not provide complex RBAC, approval workflows, ticketing, or container orchestration. It is designed as a lightweight private panel for a single administrator or a small trusted team.

## Contributing

Issues and pull requests are welcome. When reporting a problem, include the operating system, Server Panel version, reproduction steps, and relevant logs. Remove passwords, keys, IP addresses, and customer data from anything posted publicly.

## License

Server Panel is open source under the [GNU General Public License v3.0](LICENSE).

<!--
Search topics: self-hosted server management panel, VPS management panel, Linux server monitoring,
server asset management, website expiry reminder, uptime monitoring, Go SQLite admin panel
-->
