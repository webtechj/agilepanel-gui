# AgilePanel GUI Dashboard (`agilepanel-gui`)

**AgilePanel GUI** is an ultra-lightweight, single-binary companion dashboard for **AgilePanel (ap)**. It runs on a dedicated VPS port (`8889`), reads global website databases, and enables system administration through a beautiful, responsive, glassmorphic web browser interface.

Under the hood, AgilePanel GUI acts as a web wrapper that calls your system's compiled `/usr/local/bin/ap` executable, meaning every action in the web UI matches the exact performance-optimized, secure commands built into the CLI control panel.

---

## ⚡ Features
*   **Real-time Server Health**: CPU load percentages, RAM usage gauges, swap memory partitions, active sites count, and disk storage metrics.
*   **Site Management Dashboard**: Displays all hosted domains, site frameworks, active PHP pools, and user paths.
*   **Simple Creation Wizard**: Provision HTML, custom PHP, WordPress, WooCommerce, or Laravel sites with one click.
*   **Quick Service Actions**: Start/Stop/Restart underlying daemons (Caddy, MariaDB, Redis, PHP-FPM) directly from the browser.
*   **System Maintenance Tools**: Trigger system-wide optimizations (`ap server tune`), harden firewall rules (`ap server secure`), install phpMyAdmin, run non-destructive repair scripts, and upgrade OS packages.
*   **Live CLI Stream Terminal**: View real-time scrollback logs and script execution steps directly in a secure, simulated browser terminal.

---

## 🔒 Security
*   **Basic Authentication**: Shielded by HTTP Basic Authentication using the exact admin username and password hash configured in `/etc/agilepanel/state.json`.
*   **No IP/Domain Telemetry**: The GUI is self-hosted on your VPS. Credentials are verified locally on your machine via bcrypt hashes.
*   **Isolated Privileges**: If AgilePanel admin credentials are not set (e.g. fresh installation), the dashboard defaults to credentials `admin / admin` and displays a security warning banner advising you to immediately run `ap server auth` to encrypt access.

---

## 📥 Installation & Setup

AgilePanel GUI runs as a systemd service daemon under root to allow it to execute underlying CLI actions.

### 1. Build and Compile
Compile the GUI binary on your server (ensuring Go 1.20+ is installed):
```bash
git clone https://github.com/webtechj/agilepanel-gui.git
cd agilepanel-gui
go build -o /usr/local/bin/agilepanel-gui main.go
```

### 2. Configure Systemd Service
Create the service configuration file `/etc/systemd/system/agilepanel-gui.service`:
```ini
[Unit]
Description=AgilePanel Web GUI Daemon
After=network.target caddy.service mariadb.service

[Service]
Type=simple
User=root
WorkingDirectory=/usr/local/bin
ExecStart=/usr/local/bin/agilepanel-gui
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Enable and start the service:
```bash
systemctl daemon-reload
systemctl enable agilepanel-gui
systemctl start agilepanel-gui
```

### 3. Open Access in Firewall
Allow the GUI port (8889) in UFW:
```bash
ufw allow 8889/tcp
```

You can now access your AgilePanel GUI at `http://[your-server-ip]:8889`. Log in using your configured AgilePanel credentials (or `admin/admin` if not yet configured).
