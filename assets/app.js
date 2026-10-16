// Global Fetch Interceptor to handle Session Expired globally
(function() {
    function getCookie(name) {
        const value = `; ${document.cookie}`;
        const parts = value.split(`; ${name}=`);
        if (parts.length === 2) return parts.pop().split(';').shift();
    }

    const originalFetch = window.fetch;
    window.fetch = async function(url, options) {
        options = options || {};
        options.headers = options.headers || {};
        const sessionToken = getCookie("ap_csrf_token");
        if (sessionToken) {
            if (options.headers instanceof Headers) {
                options.headers.set("X-Session-Token", sessionToken);
            } else {
                options.headers["X-Session-Token"] = sessionToken;
            }
        }
        const response = await originalFetch(url, options);
        if (response.status === 403) {
            // Check if it's an authentication/session failure
            const text = await response.clone().text();
            if (text.includes("Session Expired") || text.includes("Session Required")) {
                if (typeof checkGuiAuthStatus === 'function') {
                    checkGuiAuthStatus();
                }
            } else {
                if (typeof showToast === 'function') {
                    showToast(text || "Access Denied", "error");
                } else {
                    alert("Access Denied: " + (text || "Forbidden"));
                }
            }
        } else if (response.status === 429) {
            // Rate limiting error handler
            const text = await response.clone().text();
            if (typeof showToast === 'function') {
                showToast(text || "Too many requests. Please wait.", "warning");
            } else {
                alert(text || "Too many requests.");
            }
        } else if (response.status === 503) {
            // Service Unavailable (AgilePanel not configured)
            const text = await response.clone().text();
            if (typeof showToast === 'function') {
                showToast(text || "Service Unavailable", "error");
            } else {
                alert(text || "Service Unavailable");
            }
        }
        return response;
    };
})();

// Helper to escape HTML characters for XSS prevention
function escapeHTML(str) {
    if (!str) return '';
    return String(str).replace(/[&<>'"]/g, 
        tag => ({
            '&': '&amp;',
            '<': '&lt;',
            '>': '&gt;',
            "'": '&#39;',
            '"': '&quot;'
        }[tag] || tag)
    );
}

// Show Premium Toast Notifications
function showToast(message, type = 'info') {
    let container = document.getElementById('toast-container');
    if (!container) {
        container = document.createElement('div');
        container.id = 'toast-container';
        container.className = 'toast-container';
        document.body.appendChild(container);
    }
    const toast = document.createElement('div');
    toast.className = `toast toast-${type}`;
    
    // Choose icon based on type
    let icon = 'ℹ️';
    if (type === 'success') icon = '✅';
    else if (type === 'error') icon = '❌';
    else if (type === 'warning') icon = '⚠️';
    
    toast.innerHTML = `<span class="toast-icon">${icon}</span><span class="toast-message">${message}</span>`;
    container.appendChild(toast);
    
    // Auto dismiss after 4 seconds
    setTimeout(() => {
        toast.classList.add('toast-closing');
        toast.addEventListener('animationend', () => {
            toast.remove();
            if (container.children.length === 0) {
                container.remove();
            }
        });
    }, 4000);
}

// Tab and visibility tracking for resource optimization
let currentActiveTab = 'dashboard';
let isDocumentVisible = true;
let lastStatusFetchTime = 0;

document.addEventListener('visibilitychange', () => {
    isDocumentVisible = !document.hidden;
    if (isDocumentVisible && currentActiveTab === 'dashboard') {
        loadStatus();
    }
});

// Tab management
function switchTab(tabId) {
    currentActiveTab = tabId;

    // Close sidebar on mobile
    closeSidebar();

    // Update nav links
    document.querySelectorAll('.nav-item').forEach(el => {
        el.classList.remove('active');
    });
    const activeLink = document.querySelector(`a[href="#${tabId}"]`);
    if (activeLink) {
        activeLink.classList.add('active');
    }

    // Update panes
    document.querySelectorAll('.tab-pane').forEach(el => {
        el.classList.remove('active');
    });
    const activePane = document.getElementById(`tab-${tabId}`);
    if (activePane) {
        activePane.classList.add('active');
    }

    // Set header title
    const titles = {
        'dashboard': 'Dashboard Overview',
        'sites': 'Website Management',
        'files': 'Web Space File Manager',
        'services': 'Daemon Control Panel',
        'security': 'Security Hardening Policy',
        'tools': 'System Tuning & Maintenance'
    };
    document.getElementById('page-title').innerText = titles[tabId] || 'AgilePanel';

    // Context changes
    if (tabId === 'dashboard') {
        loadStatus();
    } else if (tabId === 'sites') {
        loadSites();
    } else if (tabId === 'files') {
        initFileManagerContexts();
    } else if (tabId === 'security') {
        switchSecuritySubTab(activeSecuritySubTab || 'overview');
    } else if (tabId === 'tools') {
        loadS3Settings();
        loadTelegramSettings();
    }
}

// Format memory outputs (GB)
function formatGB(val) {
    return val ? val.toFixed(2) : '0.00';
}

// Fetch and render system health telemetry
let currentServerIP = "";

function copyServerIP() {
    const ip = currentServerIP || window.location.hostname;
    if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(ip).then(() => {
            showToast(`Server IP copied: ${ip}`, 'success');
        }).catch(() => {
            fallbackCopy(ip);
        });
    } else {
        fallbackCopy(ip);
    }
}

function fallbackCopy(text) {
    const el = document.createElement('textarea');
    el.value = text;
    document.body.appendChild(el);
    el.select();
    document.execCommand('copy');
    document.body.removeChild(el);
    showToast(`Server IP copied: ${text}`, 'success');
}

function refreshDashboardTelemetry() {
    loadStatus();
    loadMetricsHistory();
    showToast("Server telemetry refreshed", "info");
}

function openFileManagerForDomain(domain) {
    switchTab('files');
    const select = document.getElementById('file-site-select');
    if (select) {
        select.value = domain;
        onFileSiteChange();
    }
}

// Fetch and render system health telemetry
async function loadStatus() {
    if (!isDocumentVisible) return;
    const now = Date.now();
    if (currentActiveTab !== 'dashboard' && (now - lastStatusFetchTime) < 30000) {
        return;
    }
    lastStatusFetchTime = now;

    try {
        const res = await fetch('/api/status');
        if (!res.ok) throw new Error("HTTP connection failed");
        const data = await res.json();

        // 1. Render Warning Banner if admin credentials are default
        const banner = document.getElementById('auth-warning');
        if (data.global && !data.global.has_credentials) {
            banner.classList.remove('hidden');
        } else {
            banner.classList.add('hidden');
        }

        // Header user and server identity
        if (data.global && data.global.admin_user) {
            const adminUserEl = document.getElementById('header-admin-user');
            if (adminUserEl) adminUserEl.innerText = data.global.admin_user;
        }

        currentServerIP = data.serverIP || window.location.hostname;
        const serverIpEl = document.getElementById('header-server-ip');
        if (serverIpEl) {
            serverIpEl.innerText = `Server IP: ${currentServerIP}`;
        }
        const serverOsEl = document.getElementById('header-server-os');
        if (serverOsEl) {
            serverOsEl.innerText = data.osInfo || (data.hostname ? `${data.hostname}` : 'Linux');
        }
        const serverHostEl = document.getElementById('server-hostname');
        if (serverHostEl) {
            serverHostEl.innerText = data.hostname || window.location.hostname;
        }

        // 2. CPU
        document.getElementById('metric-cpu').innerText = `${data.cpu.toFixed(1)}%`;
        document.getElementById('progress-cpu').style.width = `${Math.min(data.cpu, 100)}%`;
        const cpuSub = document.getElementById('metric-cpu-sub');
        if (cpuSub && data.loadAvg && data.loadAvg.length > 0) {
            cpuSub.innerText = `Load: ${data.loadAvg.map(n => n.toFixed(2)).join(', ')}`;
        }

        // 3. RAM
        if (data.ram) {
            document.getElementById('metric-ram').innerText = `${formatGB(data.ram.used)} / ${formatGB(data.ram.total)} GB`;
            document.getElementById('progress-ram').style.width = `${data.ram.pct}%`;
            const ramSub = document.getElementById('metric-ram-sub');
            if (ramSub) {
                ramSub.innerText = `${data.ram.pct.toFixed(0)}% consumed (${formatGB(data.ram.total - data.ram.used)} GB free)`;
            }
        }

        // 4. Disk
        if (data.disk) {
            document.getElementById('metric-disk').innerText = `${formatGB(data.disk.used)} / ${formatGB(data.disk.total)} GB`;
            document.getElementById('progress-disk').style.width = `${data.disk.pct}%`;
            const diskSub = document.getElementById('metric-disk-sub');
            if (diskSub) {
                diskSub.innerText = `${formatGB(data.disk.total - data.disk.used)} GB free (${data.disk.pct.toFixed(0)}% used)`;
            }
        }

        // 5. Total Websites breakdown
        document.getElementById('metric-sites').innerText = data.siteCount;
        const wpStr = data.wpCount === 1 ? '1 WordPress' : `${data.wpCount} WordPress`;
        const htmlStr = data.htmlCount === 1 ? '1 HTML' : `${data.htmlCount} HTML`;
        const laravelStr = data.laravelCount === 1 ? '1 Laravel' : `${data.laravelCount} Laravel`;
        const phpStr = data.phpCount === 1 ? '1 PHP' : `${data.phpCount} PHP`;
        document.getElementById('metric-sites-breakdown').innerText = `${wpStr} · ${htmlStr} · ${laravelStr} · ${phpStr}`;

        // 6. Uptime, load, sockets, version
        document.getElementById('server-uptime').innerText = data.uptime || 'N/A';
        document.getElementById('server-sockets').innerText = `${data.tcpConns || 0} active sockets`;
        if (data.loadAvg && data.loadAvg.length >= 3) {
            document.getElementById('server-load').innerText = data.loadAvg.map(n => n.toFixed(2)).join(', ');
        }
        const apVersionEl = document.getElementById('ap-version');
        if (apVersionEl) {
            apVersionEl.innerText = data.apVersion ? `v${data.apVersion}` : 'N/A';
        }

        // 7. Top Processes table
        const processBody = document.getElementById('processes-table-body');
        if (processBody) {
            processBody.innerHTML = '';
            if (data.topProcesses && data.topProcesses.length > 0) {
                data.topProcesses.forEach(p => {
                    const tr = document.createElement('tr');
                    tr.innerHTML = `
                        <td><code>${escapeHTML(p.pid)}</code></td>
                        <td><strong>${escapeHTML(p.comm)}</strong></td>
                        <td><span class="badge-lock" style="background:rgba(59,130,246,0.1); color:#60a5fa; border:none;">${p.cpu.toFixed(1)}%</span></td>
                        <td><span class="badge-lock" style="background:rgba(139,92,246,0.1); color:#c084fc; border:none;">${p.mem.toFixed(1)}%</span></td>
                    `;
                    processBody.appendChild(tr);
                });
            } else {
                processBody.innerHTML = `<tr><td colspan="4" class="loading-placeholder">No active high-load processes.</td></tr>`;
            }
        }

        // 8. Core Daemon Services list rendering in right column
        const servicesContainer = document.getElementById('services-list-container');
        if (servicesContainer && data.services) {
            servicesContainer.innerHTML = '';
            
            const serviceMeta = {
                'caddy': { label: 'Caddy Web Server', port: 'Port 80/443 (HTTP/3 + SSL)', icon: '🌐' },
                'mariadb': { label: 'MariaDB SQL Engine', port: 'Port 3306 (MySQL Socket)', icon: '🗄️' },
                'redis': { label: 'Redis Object Cache', port: 'Unix Socket (Object Cache)', icon: '⚡' },
                'php-fpm': { label: 'PHP-FPM Processors', port: 'PHP 8.1, 8.2, 8.3 Pools', icon: '🐘' }
            };

            for (const [name, active] of Object.entries(data.services)) {
                const card = document.createElement('div');
                card.className = 'dash-service-card';
                
                const meta = serviceMeta[name] || { label: name.toUpperCase(), port: 'Daemon Service', icon: '⚙️' };
                const dotClass = active ? 'green' : 'red';
                const statusText = active ? 'Running' : 'Stopped';
                const btnClass = active ? 'btn-secondary' : 'btn-danger';
                
                card.innerHTML = `
                    <div class="dash-service-info">
                        <span class="dash-service-name">
                            <span class="meta-dot ${dotClass}"></span>
                            <span>${meta.icon} ${escapeHTML(meta.label)}</span>
                        </span>
                        <span class="dash-service-port">${escapeHTML(meta.port)}</span>
                    </div>
                    <div style="display:flex; align-items:center; gap:0.5rem;">
                        <span style="font-size:0.75rem; font-weight:600; color:${active ? '#34d399' : '#f87171'}; font-family:var(--font-mono);">${statusText}</span>
                        <button class="btn ${btnClass} btn-sm" style="padding:0.25rem 0.6rem; font-size:0.75rem;" onclick="triggerAction('server-restart', ['${escapeHTML(name)}'])" title="Restart ${escapeHTML(name)}">Restart</button>
                    </div>
                `;
                servicesContainer.appendChild(card);
            }
        }

        // 9. Integrate dynamic phpMyAdmin links
        const pmaLink = document.getElementById('pma-link');
        if (pmaLink) pmaLink.href = `http://${window.location.hostname}:8888`;
        const sidebarPmaLink = document.getElementById('sidebar-pma-link');
        if (sidebarPmaLink) sidebarPmaLink.href = `http://${window.location.hostname}:8888`;
        const dashPmaBtn = document.getElementById('dashboard-pma-btn');
        if (dashPmaBtn) dashPmaBtn.href = `http://${window.location.hostname}:8888`;

        // 10. Render Database Storage allocations
        const dbContainer = document.getElementById('db-storage-list');
        if (dbContainer && data.dbSizes) {
            dbContainer.innerHTML = '';
            const dbEntries = Object.entries(data.dbSizes);
            if (dbEntries.length > 0) {
                const totalMb = dbEntries.reduce((sum, [_, size]) => sum + size, 0);
                const totalBadge = document.getElementById('db-total-size-badge');
                if (totalBadge) {
                    totalBadge.innerText = totalMb > 1024 ? `${(totalMb/1024).toFixed(2)} GB Total` : `${totalMb.toFixed(1)} MB Total`;
                }

                const maxSize = Math.max(...dbEntries.map(([_, size]) => size)) || 1.0;
                dbEntries.forEach(([name, size]) => {
                    const card = document.createElement('div');
                    card.className = 'service-card';
                    card.style.flexDirection = 'column';
                    card.style.alignItems = 'stretch';
                    card.style.gap = '0.5rem';
                    card.style.padding = '0.75rem 1rem';
                    
                    const pct = (size / maxSize) * 100;
                    card.innerHTML = `
                        <div style="display:flex; justify-content:space-between; align-items:center; width:100%;">
                            <span class="service-name" style="font-family:var(--font-mono); font-size:0.85rem; text-overflow:ellipsis; overflow:hidden; white-space:nowrap;">🛢️ ${escapeHTML(name)}</span>
                            <span class="service-name" style="font-family:var(--font-mono); font-size:0.85rem; color:#60a5fa; flex-shrink:0;">${size.toFixed(2)} MB</span>
                        </div>
                        <div class="progress-bar-container" style="margin-top:0.2rem; height:4px; background:rgba(255,255,255,0.03);">
                            <div class="progress-bar" style="width: ${pct}%; height:100%; background:linear-gradient(90deg, #3b82f6 0%, #06b6d4 100%);"></div>
                        </div>
                    `;
                    dbContainer.appendChild(card);
                });
            } else {
                dbContainer.innerHTML = `<div class="loading-placeholder">No MariaDB databases detected.</div>`;
            }
        }

        // 11. Hosted Websites Glance Table
        const sitesTbody = document.getElementById('dashboard-sites-table-body');
        if (sitesTbody) {
            if (data.recentSites && data.recentSites.length > 0) {
                sitesTbody.innerHTML = '';
                data.recentSites.slice(0, 6).forEach(s => {
                    const tr = document.createElement('tr');
                    const backupText = s.last_backup_time && !s.last_backup_time.startsWith('0001') ? 
                        new Date(s.last_backup_time).toLocaleDateString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : 
                        'No backups';
                    const lockBadge = s.is_locked ? 
                        `<span class="badge-lock">🔒 Protected</span>` : 
                        `<span class="badge-lock" style="background:rgba(16,185,129,0.1); color:#34d399; border:none;">🟢 Active</span>`;
                    
                    tr.innerHTML = `
                        <td>
                            <a href="http://${escapeHTML(s.domain)}" target="_blank" style="color:#60a5fa; font-weight:600; text-decoration:none; display:flex; align-items:center; gap:0.35rem;">
                                🌐 ${escapeHTML(s.domain)} <span style="font-size:0.75rem; opacity:0.7;">↗</span>
                            </a>
                        </td>
                        <td><span class="badge-lock" style="background:rgba(99,102,241,0.12); color:#a5b4fc; border:none; text-transform:uppercase;">${escapeHTML(s.type)}</span></td>
                        <td><code style="font-size:0.8rem;">PHP ${escapeHTML(s.php_version || '8.3')}</code></td>
                        <td>${lockBadge}</td>
                        <td style="font-size:0.8rem; color:var(--text-muted);">${escapeHTML(backupText)}</td>
                        <td>
                            <div style="display:flex; gap:0.4rem;">
                                <button class="btn btn-secondary btn-sm" style="padding:0.25rem 0.5rem; font-size:0.75rem;" onclick="openFileManagerForDomain('${escapeHTML(s.domain)}')" title="Explore Files">📁 Files</button>
                                <button class="btn btn-secondary btn-sm" style="padding:0.25rem 0.5rem; font-size:0.75rem;" onclick="switchTab('sites'); openManageModal('${escapeHTML(s.domain)}')" title="Manage Site">⚙️</button>
                            </div>
                        </td>
                    `;
                    sitesTbody.appendChild(tr);
                });
            } else {
                sitesTbody.innerHTML = `<tr><td colspan="6" class="loading-placeholder">No sites configured yet. Click "+ Create Site" to deploy your first application!</td></tr>`;
            }
        }

    } catch (err) {
        console.error("Error loadStatus:", err);
    }
}

// Fetch list of websites
async function loadSites() {
    try {
        const res = await fetch('/api/sites');
        if (!res.ok) throw new Error("HTTP lookup error");
        const sites = await res.json();
        window.allSites = sites; // cache sites globally

        const tableBody = document.getElementById('sites-table-body');
        tableBody.innerHTML = '';

        if (sites.length === 0) {
            tableBody.innerHTML = `<tr><td colspan="6" class="loading-placeholder">No sites registered yet. Click 'Create New Site' above.</td></tr>`;
            return;
        }

        sites.forEach(site => {
            const tr = document.createElement('tr');
            
            const lockBadge = site.is_locked ? 
                `<span class="badge-lock">Locked (RO)</span>` : 
                `<span class="badge-unlock">Unlocked (RW)</span>`;
 
            const lockButton = site.is_locked ?
                `<button class="btn btn-success" onclick="triggerAction('site-unlock', ['${escapeHTML(site.domain)}'])">Unlock</button>` :
                `<button class="btn btn-warning" onclick="triggerAction('site-lock', ['${escapeHTML(site.domain)}'])">Lock</button>`;
 
            const filesBackupBtn = site.has_files_backup ? 
                `<a href="/api/backup/download?domain=${escapeHTML(site.domain)}&type=files" class="btn btn-success" style="background:#047857; color:#fff; text-decoration:none;" title="Download Files Backup ZIP">💾 Files ZIP</a>` : 
                `<button class="btn btn-secondary" style="opacity:0.4; cursor:not-allowed;" title="No files backup available. Click Backup first." disabled>💾 Files ZIP</button>`;
 
            const dbBackupBtn = site.type === 'html' ? '' : (site.has_db_backup ? 
                `<a href="/api/backup/download?domain=${escapeHTML(site.domain)}&type=db" class="btn btn-success" style="background:#047857; color:#fff; text-decoration:none;" title="Download Database SQL ZIP">🗄️ DB ZIP</a>` : 
                `<button class="btn btn-secondary" style="opacity:0.4; cursor:not-allowed;" title="No database backup available. Click Backup first." disabled>🗄️ DB ZIP</button>`);
 
            tr.innerHTML = `
                <td>
                    <strong>${escapeHTML(site.domain)}</strong><br>
                    <a href="${escapeHTML(site.staging_url)}" target="_blank" style="color: #60a5fa; font-size: 0.8rem; text-decoration: none; display: inline-flex; align-items: center; gap: 0.25rem; margin-top: 0.25rem; margin-bottom: 0.25rem;">🔍 Staging Link 🔗</a>
                    ${site.has_files_backup || site.has_db_backup ? `
                        <div style="font-size: 0.75rem; color: #a7f3d0; opacity: 0.9; margin-top: 0.25rem; display: flex; flex-direction: column; gap: 0.15rem;">
                            ${site.has_files_backup ? `<span>💾 Files Backup: <span style="color:#e2e8f0; font-family:var(--font-mono); font-weight:600;">${escapeHTML(site.files_backup_time)}</span></span>` : ''}
                            ${site.has_db_backup ? `<span>🗄️ DB Backup: <span style="color:#e2e8f0; font-family:var(--font-mono); font-weight:600;">${escapeHTML(site.db_backup_time)}</span></span>` : ''}
                        </div>
                    ` : ''}
                </td>
                <td><span class="badge" style="background:#1e293b; padding:0.2rem 0.5rem; border-radius:4px;">${site.type ? escapeHTML(site.type.toUpperCase()) : 'WP'}</span></td>
                <td>PHP ${escapeHTML(site.php_version)}</td>
                <td><code>${escapeHTML(site.system_user)}</code></td>
                <td>${lockBadge}</td>
                <td>
                    <div class="actions-cell">
                        ${filesBackupBtn}
                        ${dbBackupBtn}
                        <button class="btn btn-primary" onclick="openSiteCaddyConfig('${escapeHTML(site.domain)}')" style="font-weight:600;">📝 Caddy Config</button>
                        <button class="btn btn-secondary" onclick="openManageModal('${escapeHTML(site.domain)}', ${site.is_locked})" style="font-weight:600;">Manage ⚙️</button>
                    </div>
                </td>
            `;
            tableBody.appendChild(tr);
        });

    } catch (err) {
        console.error("Error loadSites:", err);
    }
}

// Modal open/close actions and Wizard step control
let currentCreateStep = 1;

function goToCreateStep(step) {
    if (step === 2) {
        const domain = document.getElementById('domain').value.trim();
        if (!domain) {
            alert("Please enter a valid domain name first!");
            return;
        }
        
        // If WordPress is selected and mode is clean, validate fields
        const type = document.getElementById('site-type').value;
        const mode = document.getElementById('create-mode').value;
        if ((type === 'wp' || type === 'woocommerce') && mode === 'clean') {
            const wpUser = document.getElementById('wp-admin-username').value.trim();
            const wpEmail = document.getElementById('wp-admin-email').value.trim();
            const wpPass = document.getElementById('wp-admin-password').value.trim();
            if (!wpUser || !wpEmail || !wpPass) {
                alert("Please provide WordPress administrator Username, Email, and Password.");
                return;
            }
        }
    }
    
    currentCreateStep = step;
    document.getElementById('create-wizard-title').innerText = `Create Website (Step ${step} of 2)`;
    
    document.querySelectorAll('.create-wizard-step').forEach(el => {
        el.style.display = 'none';
    });
    document.getElementById(`create-step-${step}`).style.display = 'block';
}

function toggleCreateMode(mode) {
    const migrationFields = document.getElementById('migration-fields');
    if (mode === 'migrate') {
        migrationFields.style.display = 'block';
    } else {
        migrationFields.style.display = 'none';
    }
}

function openCreateModal() {
    document.getElementById('create-site-form').reset();
    goToCreateStep(1);
    onSiteTypeChange('wp');
    toggleCreateMode('clean');
    document.getElementById('create-modal').classList.add('open');
}

// Handle site type toggle (HTML database option)
function onSiteTypeChange(value) {
    const htmlDiv = document.getElementById('html-db-choice');
    const wpFields = document.getElementById('wp-admin-setup-fields');
    
    if (value === 'html') {
        htmlDiv.style.display = 'flex';
        wpFields.style.display = 'none';
    } else if (value === 'wp' || value === 'woocommerce') {
        htmlDiv.style.display = 'none';
        wpFields.style.display = 'block';
    } else {
        htmlDiv.style.display = 'none';
        wpFields.style.display = 'none';
    }
}

function closeCreateModal() {
    document.getElementById('create-modal').classList.remove('open');
}

// Submit Create Site
async function submitCreateSite(e) {
    e.preventDefault();
    const form = e.target;
    const domain = form.domain.value.trim();
    const type = form.type.value;
    const php = form.php.value;
    const mode = form.create_mode.value;
    const htmlDb = document.getElementById('html-needs-db').checked;

    const filesInput = document.getElementById('import-files');
    const dbInput = document.getElementById('import-db');

    let importFilesPath = "";
    let importDbPath = "";

    closeCreateModal();

    if (mode === 'migrate' && (filesInput.files.length > 0 || dbInput.files.length > 0)) {
        try {
            if (filesInput.files.length > 0) {
                importFilesPath = await uploadImportFile(domain, 'files', filesInput.files[0]);
            }
            if (dbInput.files.length > 0) {
                importDbPath = await uploadImportFile(domain, 'db', dbInput.files[0]);
            }
        } catch (err) {
            alert("File upload failed: " + err.message);
            return;
        }
    }

    const dbOpt = (type === 'html') ? (htmlDb ? 'true' : 'false') : 'default';
    
    let wpUser = "";
    let wpPass = "";
    let wpEmail = "";
    let wpName = "";
    if (mode === 'clean' && (type === 'wp' || type === 'woocommerce')) {
        wpUser = document.getElementById('wp-admin-username').value.trim();
        wpPass = document.getElementById('wp-admin-password').value.trim();
        wpEmail = document.getElementById('wp-admin-email').value.trim();
        wpName = document.getElementById('wp-admin-name').value.trim();
    }

    const args = [
        domain, 
        php, 
        type, 
        dbOpt, 
        importFilesPath, 
        importDbPath,
        wpUser,
        wpPass,
        wpEmail,
        wpName
    ];

    triggerAction('site-create', args);
    form.reset();
    onSiteTypeChange('wp');
    toggleCreateMode('clean');
}

function uploadImportFile(domain, type, file) {
    return new Promise((resolve, reject) => {
        const progressModal = document.getElementById('upload-progress-modal');
        const progressFilename = document.getElementById('upload-progress-filename');
        const progressBar = document.getElementById('upload-progress-bar');
        const progressPct = document.getElementById('upload-progress-pct');
        const progressBytes = document.getElementById('upload-progress-bytes');

        progressFilename.innerText = `[Importing ${type}] ${file.name}`;
        progressBar.style.width = '0%';
        progressPct.innerText = '0%';
        progressBytes.innerText = `0.00 / ${(file.size / (1024*1024)).toFixed(2)} MB`;

        progressModal.classList.add('open');

        const xhr = new XMLHttpRequest();
        xhr.open('POST', '/api/sites/upload-import');

        xhr.upload.onprogress = function(e) {
            if (e.lengthComputable) {
                const percentComplete = (e.loaded / e.total) * 100;
                progressBar.style.width = percentComplete.toFixed(0) + '%';
                progressPct.innerText = percentComplete.toFixed(0) + '%';
                progressBytes.innerText = `${(e.loaded / (1024*1024)).toFixed(2)} / ${(e.total / (1024*1024)).toFixed(2)} MB`;
            }
        };

        xhr.onload = function() {
            progressModal.classList.remove('open');
            if (xhr.status >= 200 && xhr.status < 300) {
                resolve(xhr.responseText.trim());
            } else {
                reject(new Error(xhr.responseText || 'Upload failed'));
            }
        };

        xhr.onerror = function() {
            progressModal.classList.remove('open');
            reject(new Error('Network error during upload'));
        };

        const formData = new FormData();
        formData.append('domain', domain);
        formData.append('type', type);
        formData.append('file', file);
        xhr.send(formData);
    });
}

// Confirm and trigger database/files restore
function triggerRestoreSite(domain) {
    if (confirm(`WARNING: You are about to overwrite all public files and database schemas for '${domain}' with the latest backups located in its secure storage.\n\nAre you sure you want to run this restore?`)) {
        triggerAction('site-restore', [domain]);
    }
}

// Dual confirm delete action
function confirmDeleteSite(domain) {
    if (confirm(`Are you absolutely sure you want to permanently delete website '${domain}'?\n\nThis will drop the database schema and wipe all directory files!`)) {
        const doubleCheck = prompt(`WARNING: Action is destructive!\nTo verify, please re-type the domain name '${domain}':`);
        if (doubleCheck === domain) {
            triggerAction('site-delete', [domain]);
        } else {
            alert("Verification mismatch. Deletion cancelled.");
        }
    }
}

// ----------------------------------------------------
// LIGHTWEIGHT FILE MANAGER LOGIC
// ----------------------------------------------------
let fileCurrentDomain = "";
let fileCurrentPath = "";
let editorFilePath = "";

// State for Sorting, Selection, Pagination, and Dotfiles
let fileItems = [];
let fileSortColumn = 'name';
let fileSortAsc = true;
let fileShowDotfiles = true;
let selectedFiles = new Set();
let fileCurrentPage = 1;
const filePageSize = 100;

async function initFileManagerContexts() {
    try {
        const res = await fetch('/api/sites');
        const sites = await res.json();

        const select = document.getElementById('file-site-select');
        select.innerHTML = '<option value="">Select a website context...</option>';
        
        sites.forEach(site => {
            const opt = document.createElement('option');
            opt.value = site.domain;
            opt.innerText = `${site.domain} (${site.type ? site.type.toUpperCase() : 'WP'})`;
            select.appendChild(opt);
        });

        // Maintain context if already set
        initFileDropZone();
        if (fileCurrentDomain) {
            select.value = fileCurrentDomain;
            loadFiles();
        } else {
            document.getElementById('files-table-body').innerHTML = `<tr><td colspan="6" class="loading-placeholder">Please select a website context above.</td></tr>`;
            renderFileBreadcrumbs();
        }
    } catch (err) {
        console.error("FileManager Context setup failed:", err);
    }
}

function renderFileBreadcrumbs() {
    const el = document.getElementById('file-breadcrumbs');
    if (!el) return;
    if (!fileCurrentDomain) {
        el.innerHTML = '<span class="crumb-root">Webroot:</span> <span>/var/www/</span>';
        return;
    }

    let html = `<span class="crumb-root">Webroot:</span> <span class="crumb-sep">/var/www/</span>`;
    
    // Domain root link
    if (!fileCurrentPath || fileCurrentPath === '.' || fileCurrentPath === './') {
        html += `<span class="crumb-current">${escapeHTML(fileCurrentDomain)}</span>`;
    } else {
        html += `<a href="#" class="crumb-link" onclick="navigateToBreadcrumb(''); return false;">${escapeHTML(fileCurrentDomain)}</a>`;
    }

    // Path segments
    if (fileCurrentPath && fileCurrentPath !== '.' && fileCurrentPath !== './') {
        const segments = fileCurrentPath.split('/').filter(Boolean);
        let accum = '';
        segments.forEach((seg, idx) => {
            accum = accum ? `${accum}/${seg}` : seg;
            html += ` <span class="crumb-sep">/</span> `;
            if (idx === segments.length - 1) {
                html += `<span class="crumb-current">${escapeHTML(seg)}</span>`;
            } else {
                const target = accum;
                html += `<a href="#" class="crumb-link" onclick="navigateToBreadcrumb('${escapeHTML(target)}'); return false;">${escapeHTML(seg)}</a>`;
            }
        });
    }

    el.innerHTML = html;
}

function navigateToBreadcrumb(targetPath) {
    fileCurrentPath = targetPath;
    loadFiles();
}

function onFileSiteChange() {
    const val = document.getElementById('file-site-select').value;
    fileCurrentDomain = val;
    fileCurrentPath = ""; // reset back to domain root
    if (val) {
        loadFiles();
    } else {
        document.getElementById('files-table-body').innerHTML = `<tr><td colspan="6" class="loading-placeholder">Please select a website context above.</td></tr>`;
        renderFileBreadcrumbs();
        document.getElementById('btn-file-up').disabled = true;
    }
}

async function loadFiles() {
    if (!fileCurrentDomain) return;
    const body = document.getElementById('files-table-body');
    body.innerHTML = `<tr><td colspan="6" class="loading-placeholder">Loading directory structure...</td></tr>`;

    // Enable/disable UP button
    const isRoot = fileCurrentPath === "" || fileCurrentPath === "." || fileCurrentPath === "./";
    document.getElementById('btn-file-up').disabled = isRoot;

    // Build interactive breadcrumbs
    renderFileBreadcrumbs();

    try {
        const res = await fetch(`/api/files/list?domain=${fileCurrentDomain}&path=${fileCurrentPath}`);
        if (!res.ok) {
            const errText = await res.text();
            throw new Error(errText);
        }
        fileItems = await res.json();
        fileCurrentPage = 1;
        selectedFiles.clear();
        updateBatchBar();
        renderFilesTable();
    } catch (err) {
        body.innerHTML = `<tr><td colspan="6" class="loading-placeholder" style="color:var(--danger);">Error loading files: ${err.message}</td></tr>`;
    }
}

function getVisibleFilteredFiles() {
    const searchInput = document.getElementById('file-search-input');
    const query = searchInput ? searchInput.value.toLowerCase().trim() : '';

    return fileItems.filter(f => {
        // Dotfile filter
        if (!fileShowDotfiles && f.name.startsWith('.')) {
            return false;
        }
        // Search query filter
        if (query && !f.name.toLowerCase().includes(query)) {
            return false;
        }
        return true;
    });
}

function renderFilesTable() {
    const body = document.getElementById('files-table-body');
    if (!body) return;

    const visibleFiles = getVisibleFilteredFiles();

    if (visibleFiles.length === 0) {
        body.innerHTML = `<tr><td colspan="6" class="loading-placeholder">Directory is empty or no files match search.</td></tr>`;
        const pagContainer = document.getElementById('files-pagination-container');
        if (pagContainer) pagContainer.classList.add('hidden');
        return;
    }

    // Sort visible files
    visibleFiles.sort((a, b) => {
        if (a.isDir && !b.isDir) return -1;
        if (!a.isDir && b.isDir) return 1;

        let comp = 0;
        if (fileSortColumn === 'size') {
            comp = (a.size || 0) - (b.size || 0);
        } else if (fileSortColumn === 'modTime') {
            comp = (a.modTime || '').localeCompare(b.modTime || '');
        } else if (fileSortColumn === 'mode') {
            comp = (a.mode || '').localeCompare(b.mode || '');
        } else {
            comp = (a.name || '').localeCompare(b.name || '');
        }
        return fileSortAsc ? comp : -comp;
    });

    // Pagination calculations
    const totalItems = visibleFiles.length;
    const totalPages = Math.ceil(totalItems / filePageSize) || 1;
    if (fileCurrentPage > totalPages) fileCurrentPage = totalPages;
    if (fileCurrentPage < 1) fileCurrentPage = 1;

    const startIndex = (fileCurrentPage - 1) * filePageSize;
    const endIndex = Math.min(startIndex + filePageSize, totalItems);
    const pageFiles = visibleFiles.slice(startIndex, endIndex);

    const pagContainer = document.getElementById('files-pagination-container');
    const pagInfo = document.getElementById('files-pagination-info');
    const pageCurrent = document.getElementById('files-page-current');
    const btnPrev = document.getElementById('btn-page-prev');
    const btnNext = document.getElementById('btn-page-next');

    if (pagContainer && pagInfo && pageCurrent && btnPrev && btnNext) {
        if (totalPages > 1) {
            pagContainer.classList.remove('hidden');
            pagInfo.innerText = `Showing ${startIndex + 1} - ${endIndex} of ${totalItems} items`;
            pageCurrent.innerText = `Page ${fileCurrentPage} of ${totalPages}`;
            btnPrev.disabled = (fileCurrentPage <= 1);
            btnNext.disabled = (fileCurrentPage >= totalPages);
        } else {
            pagContainer.classList.add('hidden');
        }
    }

    body.innerHTML = '';

    const imageExtensions = ['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp', 'ico'];
    const editableExtensions = ['html', 'css', 'js', 'php', 'txt', 'json', 'conf', 'ini', 'htaccess'];

    pageFiles.forEach(file => {
        const tr = document.createElement('tr');
        const isSelected = selectedFiles.has(file.name);
        if (isSelected) tr.classList.add('row-selected');

        let icon = "📄";
        const ext = file.name.split('.').pop().toLowerCase();
        const isImage = !file.isDir && imageExtensions.includes(ext);

        if (file.isDir) {
            icon = "📁";
        } else if (['zip', 'tar', 'gz', 'rar', '7z'].includes(ext)) {
            icon = "📦";
        } else if (['html', 'htm'].includes(ext)) {
            icon = "🌐";
        } else if (ext === 'css') {
            icon = "🎨";
        } else if (ext === 'js') {
            icon = "⚡";
        } else if (['json', 'xml', 'yml', 'yaml'].includes(ext)) {
            icon = "⚙️";
        } else if (ext === 'php') {
            icon = "🐘";
        } else if (isImage) {
            icon = "🖼️";
        } else if (ext === 'sql') {
            icon = "🛢️";
        } else if (['md', 'txt', 'conf', 'ini', 'htaccess'].includes(ext)) {
            icon = "📝";
        }

        let nameEl = '';
        if (file.isDir) {
            nameEl = `<a href="#" onclick="enterFolder('${escapeHTML(file.name)}'); return false;" style="color:#60a5fa; font-weight:600; text-decoration:none;">${icon} ${escapeHTML(file.name)}</a>`;
        } else if (isImage) {
            nameEl = `<a href="#" onclick="openImagePreview('${escapeHTML(file.name)}'); return false;" style="color:#38bdf8; font-weight:500; text-decoration:none;" title="Click to preview image">${icon} ${escapeHTML(file.name)}</a>`;
        } else {
            nameEl = `<span>${icon} ${escapeHTML(file.name)}</span>`;
        }

        const sizeText = file.isDir ? '-' : formatBytes(file.size);
        const canEdit = !file.isDir && editableExtensions.includes(ext);

        let editBtn = '';
        if (canEdit) {
            editBtn = `<button class="btn btn-primary" onclick="openFileEditor('${escapeHTML(file.name)}')">Edit</button>`;
        } else if (isImage) {
            editBtn = `<button class="btn btn-secondary" onclick="openImagePreview('${escapeHTML(file.name)}')">Preview</button>`;
        }

        const filePath = fileCurrentPath ? `${fileCurrentPath}/${file.name}` : file.name;

        tr.innerHTML = `
            <td style="text-align: center;">
                <input type="checkbox" class="fm-row-checkbox" ${isSelected ? 'checked' : ''} onchange="toggleSelectFile('${escapeHTML(file.name)}', this.checked)">
            </td>
            <td>${nameEl}</td>
            <td>${sizeText}</td>
            <td>
                <code class="permission-badge-btn" style="color:#a7f3d0;" title="Click to view & edit permissions (chmod)" onclick="openChmodModalForPath('${escapeHTML(filePath)}', '${escapeHTML(file.mode)}', ${file.isDir})">${escapeHTML(file.mode)}</code>
            </td>
            <td style="color:#94a3b8; font-size:0.85rem;">${escapeHTML(file.modTime)}</td>
            <td>
                <div class="actions-cell">
                    ${editBtn}
                    <button class="btn btn-danger" onclick="deleteFileConfirm('${escapeHTML(filePath)}', '${escapeHTML(file.name)}')">Delete</button>
                </div>
            </td>
        `;

        // Attach right-click context menu
        tr.addEventListener('contextmenu', (e) => {
            e.preventDefault();
            showFileContextMenu(e, file);
        });

        body.appendChild(tr);
    });

    updateSortIcons();
}

function prevFilesPage() {
    if (fileCurrentPage > 1) {
        fileCurrentPage--;
        renderFilesTable();
    }
}

function nextFilesPage() {
    const visible = getVisibleFilteredFiles();
    const maxPage = Math.ceil(visible.length / filePageSize) || 1;
    if (fileCurrentPage < maxPage) {
        fileCurrentPage++;
        renderFilesTable();
    }
}

function toggleDotfiles() {
    fileShowDotfiles = !fileShowDotfiles;
    const btn = document.getElementById('btn-toggle-dotfiles');
    if (btn) {
        btn.innerText = fileShowDotfiles ? '👁️ Dotfiles: Shown' : '👁️ Dotfiles: Hidden';
    }
    fileCurrentPage = 1;
    renderFilesTable();
}

function sortFiles(col) {
    if (fileSortColumn === col) {
        fileSortAsc = !fileSortAsc;
    } else {
        fileSortColumn = col;
        fileSortAsc = true;
    }
    renderFilesTable();
}

function updateSortIcons() {
    ['name', 'size', 'mode', 'modTime'].forEach(c => {
        const iconEl = document.getElementById(`sort-icon-${c}`);
        if (!iconEl) return;
        if (c === fileSortColumn) {
            iconEl.innerText = fileSortAsc ? '▲' : '▼';
        } else {
            iconEl.innerText = '';
        }
    });
}

function toggleSelectFile(name, checked) {
    if (checked) {
        selectedFiles.add(name);
    } else {
        selectedFiles.delete(name);
    }
    updateBatchBar();
    updateRowSelectionStyles();
}

function toggleSelectAllFiles(checked) {
    const visible = getVisibleFilteredFiles();
    const startIndex = (fileCurrentPage - 1) * filePageSize;
    const pageFiles = visible.slice(startIndex, startIndex + filePageSize);

    pageFiles.forEach(f => {
        if (checked) {
            selectedFiles.add(f.name);
        } else {
            selectedFiles.delete(f.name);
        }
    });
    updateBatchBar();
    renderFilesTable();
}

function clearFileSelection() {
    selectedFiles.clear();
    const allBox = document.getElementById('fm-select-all');
    if (allBox) allBox.checked = false;
    updateBatchBar();
    renderFilesTable();
}

function updateBatchBar() {
    const bar = document.getElementById('file-batch-bar');
    const countEl = document.getElementById('file-batch-count');
    if (!bar) return;
    if (selectedFiles.size > 0) {
        bar.classList.remove('hidden');
        if (countEl) countEl.innerText = selectedFiles.size;
    } else {
        bar.classList.add('hidden');
    }
}

function updateRowSelectionStyles() {
    document.querySelectorAll('#files-table-body tr').forEach(tr => {
        const checkbox = tr.querySelector('.fm-row-checkbox');
        if (checkbox && checkbox.checked) {
            tr.classList.add('row-selected');
        } else {
            tr.classList.remove('row-selected');
        }
    });
}

async function batchDeleteSelected() {
    if (selectedFiles.size === 0) return;
    const count = selectedFiles.size;
    if (!confirm(`Are you sure you want to permanently delete all ${count} selected item(s)?`)) {
        return;
    }

    const paths = Array.from(selectedFiles).map(name => {
        return fileCurrentPath ? `${fileCurrentPath}/${name}` : name;
    });

    try {
        const res = await fetch('/api/files/batch-delete', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                domain: fileCurrentDomain,
                paths: paths
            })
        });
        if (!res.ok) throw new Error(await res.text());
        const data = await res.json();
        showToast(`Deleted ${data.deleted} item(s)`, "success");
        clearFileSelection();
        loadFiles();
    } catch (err) {
        showToast("Batch delete failed: " + err.message, "error");
    }
}

async function batchZipSelected() {
    if (selectedFiles.size === 0) return;
    if (selectedFiles.size === 1) {
        const name = Array.from(selectedFiles)[0];
        const filePath = fileCurrentPath ? `${fileCurrentPath}/${name}` : name;
        zipFileOrFolder(filePath);
        return;
    }
    showToast("Batch ZIP is supported by selecting a folder or compressing items individually", "info");
}

function formatBytes(bytes) {
    if (bytes === 0) return '0 Bytes';
    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}

function enterFolder(name) {
    fileCurrentPath = fileCurrentPath ? `${fileCurrentPath}/${name}` : name;
    loadFiles();
}

function goUpFolder() {
    if (fileCurrentPath === "" || fileCurrentPath === ".") return;
    const parts = fileCurrentPath.split('/');
    parts.pop();
    fileCurrentPath = parts.join('/');
    loadFiles();
}

function refreshFiles() {
    loadFiles();
}

// File actions
async function openNewFileModal() {
    if (!fileCurrentDomain) return alert("Select website context first!");
    const name = prompt("Enter new file name (e.g. info.php):");
    if (!name) return;
    
    try {
        const res = await fetch('/api/files/create', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                domain: fileCurrentDomain,
                path: fileCurrentPath,
                name: name,
                isDir: false
            })
        });
        if (!res.ok) throw new Error(await res.text());
        loadFiles();
    } catch (err) {
        alert("Failed to create file: " + err.message);
    }
}

async function openNewFolderModal() {
    if (!fileCurrentDomain) return alert("Select website context first!");
    const name = prompt("Enter new folder name (e.g. assets):");
    if (!name) return;
    
    try {
        const res = await fetch('/api/files/create', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                domain: fileCurrentDomain,
                path: fileCurrentPath,
                name: name,
                isDir: true
            })
        });
        if (!res.ok) throw new Error(await res.text());
        loadFiles();
    } catch (err) {
        alert("Failed to create folder: " + err.message);
    }
}

function initFileDropZone() {
    const dropzone = document.getElementById('file-dropzone-container');
    if (!dropzone || dropzone.dataset.dropzoneInit) return;
    dropzone.dataset.dropzoneInit = "true";

    ['dragenter', 'dragover'].forEach(eventName => {
        dropzone.addEventListener(eventName, (e) => {
            e.preventDefault();
            e.stopPropagation();
            if (fileCurrentDomain) {
                dropzone.classList.add('file-dropzone-active');
            }
        });
    });

    ['dragleave', 'dragend'].forEach(eventName => {
        dropzone.addEventListener(eventName, (e) => {
            e.preventDefault();
            e.stopPropagation();
            dropzone.classList.remove('file-dropzone-active');
        });
    });

    dropzone.addEventListener('drop', (e) => {
        e.preventDefault();
        e.stopPropagation();
        dropzone.classList.remove('file-dropzone-active');
        if (!fileCurrentDomain) {
            showToast("Please select a website context first!", "warning");
            return;
        }
        if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length > 0) {
            const files = Array.from(e.dataTransfer.files);
            uploadMultipleFiles(files);
        }
    });
}

function triggerFileUpload() {
    if (!fileCurrentDomain) return alert("Select website context first!");
    document.getElementById('file-upload-input').click();
}

function uploadFileObjectPromise(file) {
    return new Promise((resolve) => {
        const formData = new FormData();
        formData.append("domain", fileCurrentDomain);
        formData.append("path", fileCurrentPath);
        formData.append("file", file);

        const progressModal = document.getElementById('upload-progress-modal');
        const progressFilename = document.getElementById('upload-progress-filename');
        const progressBar = document.getElementById('upload-progress-bar');
        const progressPct = document.getElementById('upload-progress-pct');
        const progressBytes = document.getElementById('upload-progress-bytes');

        if (progressFilename) progressFilename.innerText = file.name;
        if (progressBar) progressBar.style.width = '0%';
        if (progressPct) progressPct.innerText = '0%';
        if (progressBytes) progressBytes.innerText = `0.00 / ${(file.size / (1024*1024)).toFixed(2)} MB`;

        if (progressModal) progressModal.classList.add('open');

        const xhr = new XMLHttpRequest();
        
        xhr.upload.onprogress = function(e) {
            if (e.lengthComputable) {
                const percentComplete = (e.loaded / e.total) * 100;
                if (progressBar) progressBar.style.width = percentComplete.toFixed(0) + '%';
                if (progressPct) progressPct.innerText = percentComplete.toFixed(0) + '%';
                if (progressBytes) progressBytes.innerText = `${(e.loaded / (1024*1024)).toFixed(2)} / ${(e.total / (1024*1024)).toFixed(2)} MB`;
            }
        };

        xhr.onload = function() {
            if (progressModal) progressModal.classList.remove('open');
            if (xhr.status >= 200 && xhr.status < 300) {
                showToast(`Uploaded ${file.name} successfully`, "success");
                resolve(true);
            } else {
                showToast("Upload failed: " + (xhr.responseText || "Unknown server error"), "error");
                resolve(false);
            }
        };

        xhr.onerror = function() {
            if (progressModal) progressModal.classList.remove('open');
            showToast("Upload failed: network error occurred", "error");
            resolve(false);
        };

        xhr.open("POST", "/api/files/upload");
        xhr.send(formData);
    });
}

async function uploadMultipleFiles(fileList) {
    if (!fileList || fileList.length === 0) return;
    for (let i = 0; i < fileList.length; i++) {
        await uploadFileObjectPromise(fileList[i]);
    }
    loadFiles();
}

async function handleFileUploadSelected() {
    const input = document.getElementById('file-upload-input');
    if (!input.files || input.files.length === 0) return;
    const files = Array.from(input.files);
    await uploadMultipleFiles(files);
    input.value = '';
}

async function deleteFileConfirm(path, name) {
    if (confirm(`Are you sure you want to permanently delete '${name}'?`)) {
        try {
            const res = await fetch('/api/files/delete', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    domain: fileCurrentDomain,
                    path: path
                })
            });
            if (!res.ok) throw new Error(await res.text());
            loadFiles();
        } catch (err) {
            alert("Delete failed: " + err.message);
        }
    }
}

// Modal Text Editor Actions
async function openFileEditor(name) {
    editorFilePath = fileCurrentPath ? `${fileCurrentPath}/${name}` : name;
    const fullPath = `/var/www/${fileCurrentDomain}/${editorFilePath}`;
    
    try {
        const res = await fetch(`/api/files/read?domain=${fileCurrentDomain}&path=${editorFilePath}`);
        if (!res.ok) throw new Error(await res.text());
        const data = await res.text();

        document.getElementById('editor-title').innerText = `Editing: ${name}`;
        document.getElementById('editor-subtitle').innerText = `Path: ${fullPath}`;
        
        const contentArea = document.getElementById('editor-content');
        contentArea.value = data;
        
        // Remove old listeners to prevent duplication
        const oldContentArea = contentArea.cloneNode(true);
        contentArea.parentNode.replaceChild(oldContentArea, contentArea);
        const newContentArea = document.getElementById('editor-content');

        // Setup Tab indent intercept and brace auto-closing
        newContentArea.addEventListener('keydown', function(e) {
            if (e.key === 'Tab') {
                e.preventDefault();
                const start = this.selectionStart;
                const end = this.selectionEnd;
                this.value = this.value.substring(0, start) + "\t" + this.value.substring(end);
                this.selectionStart = this.selectionEnd = start + 1;
                updateLineNumbers();
            }
            
            const pairs = {
                '"': '"',
                "'": "'",
                '`': '`',
                '(': ')',
                '{': '}',
                '[': ']',
                '<': '>'
            };
            if (pairs[e.key] !== undefined) {
                e.preventDefault();
                const start = this.selectionStart;
                const end = this.selectionEnd;
                const char = e.key;
                const closeChar = pairs[char];
                
                if (start !== end) {
                    const selectedText = this.value.substring(start, end);
                    this.value = this.value.substring(0, start) + char + selectedText + closeChar + this.value.substring(end);
                    this.selectionStart = start + 1;
                    this.selectionEnd = end + 1;
                } else {
                    this.value = this.value.substring(0, start) + char + closeChar + this.value.substring(end);
                    this.selectionStart = this.selectionEnd = start + 1;
                }
                updateLineNumbers();
            }
        });

        // Re-attach scroll sync
        newContentArea.addEventListener('scroll', syncEditorScroll);
        newContentArea.addEventListener('input', updateLineNumbers);
        
        // Show modal first to ensure clientWidth/clientHeight are computed correctly
        document.getElementById('editor-modal').classList.add('open');
        
        // Trigger initial gutter rendering and stats count
        updateLineNumbers();
        
        // Reset scroll position to top
        newContentArea.scrollTop = 0;
        document.getElementById('editor-line-numbers').scrollTop = 0;
        document.getElementById('editor-stat-status').innerText = 'Ready';
        document.getElementById('editor-stat-status').style.color = '#38bdf8';
        
        // Hide find-replace bar on open
        toggleEditorSearch(false);
    } catch (err) {
        alert("Failed to load file contents: " + err.message);
    }
}

function updateLineNumbers() {
    const textarea = document.getElementById('editor-content');
    const gutter = document.getElementById('editor-line-numbers');
    const text = textarea.value;
    const lines = text.split('\n');
    const lineCount = lines.length;
    
    let numbers = [];
    for (let i = 1; i <= lineCount; i++) {
        numbers.push(i);
    }
    gutter.textContent = numbers.join('\n');

    // Update stats
    document.getElementById('editor-stat-lines').innerText = `Lines: ${lineCount}`;
    document.getElementById('editor-stat-chars').innerText = `Chars: ${text.length}`;
}

function syncEditorScroll() {
    const textarea = document.getElementById('editor-content');
    const gutter = document.getElementById('editor-line-numbers');
    gutter.scrollTop = textarea.scrollTop;
}

function closeEditorModal() {
    document.getElementById('editor-modal').classList.remove('open');
}

// Global key events for full screen editor, modals, and file explorer navigation
window.addEventListener('keydown', (e) => {
    const editorModal = document.getElementById('editor-modal');
    const isEditorOpen = editorModal && editorModal.classList.contains('open');

    const chmodModal = document.getElementById('chmod-modal');
    const isChmodOpen = chmodModal && chmodModal.classList.contains('open');

    const imgModal = document.getElementById('image-preview-modal');
    const isImgOpen = imgModal && imgModal.classList.contains('open');

    // Lightbox image preview modal: Escape to close
    if (isImgOpen && e.key === 'Escape') {
        e.preventDefault();
        closeImagePreview();
        return;
    }

    // Chmod modal: Escape to close
    if (isChmodOpen && e.key === 'Escape') {
        e.preventDefault();
        closeChmodModal();
        return;
    }

    // Full screen editor modal shortcuts
    if (isEditorOpen) {
        // Ctrl+S / Cmd+S: Save file
        if ((e.ctrlKey || e.metaKey) && e.key === 's') {
            e.preventDefault();
            saveEditorContent();
        }
        // Ctrl+F / Cmd+F: Find in editor
        if ((e.ctrlKey || e.metaKey) && e.key === 'f') {
            e.preventDefault();
            toggleEditorSearch(true);
        }
        // Esc: Close search bar or exit editor
        if (e.key === 'Escape') {
            e.preventDefault();
            const searchBar = document.getElementById('editor-search-replace');
            if (searchBar && !searchBar.classList.contains('hidden')) {
                toggleEditorSearch(false);
            } else {
                closeEditorModal();
            }
        }
        return;
    }

    // File Manager Explorer Shortcuts (active when files tab is open and no form inputs focused)
    if (currentActiveTab === 'files') {
        const activeTag = document.activeElement ? document.activeElement.tagName.toLowerCase() : '';
        const isInputFocused = activeTag === 'input' || activeTag === 'textarea' || activeTag === 'select';
        if (isInputFocused) return;

        // Backspace or Alt+ArrowUp: Navigate up directory
        if (e.key === 'Backspace' || (e.altKey && e.key === 'ArrowUp')) {
            e.preventDefault();
            goUpFolder();
            return;
        }

        // Delete: Batch delete selected files
        if (e.key === 'Delete' && selectedFiles.size > 0) {
            e.preventDefault();
            batchDeleteSelected();
            return;
        }
    }
});

async function saveEditorContent() {
    const content = document.getElementById('editor-content').value;
    const statusText = document.getElementById('editor-stat-status');
    statusText.innerText = 'Saving...';
    statusText.style.color = '#f59e0b';
    
    try {
        const res = await fetch('/api/files/write', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                domain: fileCurrentDomain,
                path: editorFilePath,
                content: content
            })
        });
        if (!res.ok) throw new Error(await res.text());
        statusText.innerText = 'Saved successfully';
        statusText.style.color = '#10b981';
        setTimeout(() => {
            if (statusText.innerText === 'Saved successfully') {
                statusText.innerText = 'Ready';
                statusText.style.color = '#38bdf8';
            }
        }, 3000);
        loadFiles();
    } catch (err) {
        statusText.innerText = 'Error saving';
        statusText.style.color = '#ef4444';
        alert("Failed to save changes: " + err.message);
    }
}

// ----------------------------------------------------
// REVAMPED SECURITY OPERATIONS CENTER (SOC) CONTROLLER
// ----------------------------------------------------

let currentSecurityChecks = [];
let currentSecurityFilter = 'all';
let activeSecuritySubTab = 'overview';
let logSearchDebounceTimer = null;

function switchSecuritySubTab(subTabId) {
    activeSecuritySubTab = subTabId;
    
    // Update subnav buttons
    const navButtons = document.querySelectorAll('.sec-nav-btn');
    navButtons.forEach(btn => btn.classList.remove('active'));
    const targetNav = document.getElementById(`sec-subnav-${subTabId}`);
    if (targetNav) targetNav.classList.add('active');

    // Update subpanes
    const subPanes = document.querySelectorAll('.sec-subpane');
    subPanes.forEach(pane => {
        pane.style.display = 'none';
        pane.classList.remove('active');
    });

    const targetPane = document.getElementById(`sec-subpane-${subTabId}`);
    if (targetPane) {
        targetPane.style.display = 'block';
        targetPane.classList.add('active');
    }

    // Lazy load data for the active sub-tab (minimal resource usage)
    if (subTabId === 'overview') {
        runSecurityDashboardScan();
    } else if (subTabId === 'firewall') {
        loadFirewallData();
    } else if (subTabId === 'auditlogs') {
        loadSecurityAuditLogs();
    } else if (subTabId === 'access') {
        loadActiveSessions();
        if (typeof checkGuiAuthStatus === 'function') checkGuiAuthStatus();
    } else if (subTabId === 'ssl') {
        loadSecuritySSLData();
    }
}

async function runSecurityDashboardScan() {
    const scoreGrade = document.getElementById('sec-score-grade');
    const scoreText = document.getElementById('sec-score-text');
    const scoreSummary = document.getElementById('sec-score-summary');
    const postureBadge = document.getElementById('sec-posture-badge');
    const radialBar = document.getElementById('sec-radial-bar');
    const ufwSummary = document.getElementById('sec-ufw-summary');
    const sshPortSummary = document.getElementById('sec-ssh-port');
    const checksCounter = document.getElementById('sec-checks-counter');
    const lastScanTime = document.getElementById('sec-last-scan-time');

    if (scoreText) scoreText.innerText = 'Scanning...';
    if (scoreSummary) scoreSummary.innerText = 'Querying live system security status...';

    try {
        const res = await fetch('/api/security/audit');
        if (!res.ok) throw new Error('Security audit query failed');
        const data = await res.json();
        currentSecurityChecks = data.checks || [];

        // Update score badge & text
        if (scoreGrade) {
            scoreGrade.innerText = data.grade || 'A';
            if (data.grade === 'B') scoreGrade.style.color = '#60a5fa';
            else if (data.grade === 'C') scoreGrade.style.color = '#fbbf24';
            else if (data.grade === 'D') scoreGrade.style.color = '#f87171';
            else scoreGrade.style.color = '#34d399';
        }

        if (scoreText) {
            scoreText.innerText = `${data.score}%`;
        }

        // Animated SVG Radial Gauge (stroke-dashoffset transition)
        if (radialBar) {
            const circumference = 2 * Math.PI * 50; // ~314.159
            const offset = Math.max(0, circumference - (data.score / 100) * circumference);
            radialBar.style.strokeDashoffset = offset;
            if (data.score >= 90) radialBar.style.stroke = '#10b981';
            else if (data.score >= 80) radialBar.style.stroke = '#3b82f6';
            else if (data.score >= 65) radialBar.style.stroke = '#f59e0b';
            else radialBar.style.stroke = '#ef4444';
        }

        if (postureBadge) {
            postureBadge.innerText = data.score >= 80 ? 'System Hardened' : 'Attention Needed';
            postureBadge.className = data.score >= 80 ? 'badge-unlock' : 'badge-lock';
        }

        if (scoreSummary) {
            if (data.score >= 80) {
                scoreSummary.innerText = data.isMock ? 'System configuration verified (Mock Environment).' : 'System is securely configured according to security hardening standards.';
            } else {
                scoreSummary.innerText = 'Some security policies need attention. Review checklist and remediate vulnerabilities.';
            }
        }

        if (lastScanTime && data.timestamp) {
            lastScanTime.innerText = `Last scan: ${data.timestamp}`;
        }

        // Category Scores
        if (data.categoryScores) {
            const setCat = (cat, val) => {
                const textElem = document.getElementById(`sec-cat-${cat}-val`);
                const fillElem = document.getElementById(`sec-cat-${cat}-fill`);
                if (textElem) textElem.innerText = `${val}%`;
                if (fillElem) {
                    fillElem.style.width = `${val}%`;
                    if (val < 70) fillElem.style.background = '#ef4444';
                    else if (val < 90) fillElem.style.background = '#f59e0b';
                    else fillElem.style.background = '#10b981';
                }
            };
            setCat('network', data.categoryScores['network'] ?? 100);
            setCat('access', data.categoryScores['access'] ?? 100);
            setCat('web', data.categoryScores['web'] ?? 100);
            setCat('system', data.categoryScores['system'] ?? 100);
        }

        if (ufwSummary) {
            const ufwCheck = currentSecurityChecks.find(c => c.id === 'ufw');
            ufwSummary.innerText = (ufwCheck && ufwCheck.passed) ? 'Active' : 'Disabled';
            ufwSummary.style.color = (ufwCheck && ufwCheck.passed) ? '#34d399' : '#f87171';
        }

        if (sshPortSummary) {
            sshPortSummary.innerText = data.customSSH ? `${data.customSSH} (Custom)` : '22 (Default)';
        }

        const passedCount = currentSecurityChecks.filter(c => c.passed).length;
        const totalCount = currentSecurityChecks.length;
        if (checksCounter) {
            checksCounter.innerText = `${passedCount} / ${totalCount}`;
        }

        // Update filter badge counters
        const criticalCount = currentSecurityChecks.filter(c => !c.passed && (c.severity === 'critical' || c.severity === 'high')).length;
        const actionCount = currentSecurityChecks.filter(c => !c.passed).length;
        const cntAll = document.getElementById('filter-cnt-all');
        const cntCritical = document.getElementById('filter-cnt-critical');
        const cntAction = document.getElementById('filter-cnt-action');
        const cntPassed = document.getElementById('filter-cnt-passed');
        if (cntAll) cntAll.innerText = totalCount;
        if (cntCritical) cntCritical.innerText = criticalCount;
        if (cntAction) cntAction.innerText = actionCount;
        if (cntPassed) cntPassed.innerText = passedCount;

        // Update dashboard mini security card if present
        const dashSecScore = document.getElementById('dash-sec-score');
        if (dashSecScore) dashSecScore.innerText = data.grade || 'A';
        const dashSecStatus = document.getElementById('dash-sec-status');
        if (dashSecStatus) {
            dashSecStatus.innerText = data.score >= 80 ? 'System Hardened' : 'Attention Needed';
            dashSecStatus.style.color = data.score >= 80 ? '#6ee7b7' : '#f59e0b';
        }
        const dashSecNotes = document.getElementById('dash-sec-notes');
        if (dashSecNotes) {
            dashSecNotes.innerText = `Audit Score: ${data.score}% · ${passedCount}/${totalCount} Policy Checks Passed`;
        }
        const dashUfw = document.getElementById('dash-ufw-status');
        if (dashUfw) {
            const ufwCheck = currentSecurityChecks.find(c => c.id === 'ufw');
            dashUfw.innerText = (ufwCheck && ufwCheck.passed) ? 'Active & Enforced' : 'Disabled / Inactive';
            dashUfw.style.color = (ufwCheck && ufwCheck.passed) ? '#34d399' : '#f87171';
        }

        renderSecurityChecks();

    } catch (err) {
        console.error("Security Scan failed:", err);
        if (scoreText) scoreText.innerText = 'Scan Failed';
        if (scoreSummary) scoreSummary.innerText = 'Could not query security API endpoint.';
    }
}

function filterSecurityChecks(filterType) {
    currentSecurityFilter = filterType;
    const chips = document.querySelectorAll('.sec-chip');
    chips.forEach(c => c.classList.remove('active'));
    const activeChip = document.getElementById(`chip-filter-${filterType}`);
    if (activeChip) activeChip.classList.add('active');
    renderSecurityChecks();
}

function renderSecurityChecks() {
    const grid = document.getElementById('security-checks-grid');
    if (!grid) return;
    grid.innerHTML = '';

    const filtered = currentSecurityChecks.filter(chk => {
        if (currentSecurityFilter === 'passed') return chk.passed;
        if (currentSecurityFilter === 'action') return !chk.passed;
        if (currentSecurityFilter === 'critical') return !chk.passed && (chk.severity === 'critical' || chk.severity === 'high');
        return true;
    });

    if (filtered.length === 0) {
        grid.innerHTML = `<div style="grid-column: 1/-1; text-align: center; color: var(--text-muted); padding: 2rem;">No checks match the selected filter.</div>`;
        return;
    }

    filtered.forEach(chk => {
        const card = document.createElement('div');
        card.className = 'security-status-card';
        card.id = `sec-${chk.id}`;

        const badgeClass = chk.passed ? 'badge-unlock' : 'badge-lock';
        const severityClass = chk.severity || 'medium';
        const category = chk.category || 'general';

        const remediationHtml = (!chk.passed && chk.canFix) ? `
            <div class="sec-remediation-bar">
                <span class="sec-remediation-text">💡 <strong>Remediation:</strong> ${escapeHTML(chk.remediation || 'Automated fix available')}</span>
                <button class="btn-remediate" onclick="triggerTargetedRemediation('${escapeHTML(chk.id)}')">Fix Now ⚡</button>
            </div>
        ` : (chk.remediation ? `
            <div class="sec-remediation-bar fixed">
                <span class="sec-remediation-text" style="color: #a7f3d0;">🛡️ <strong>Policy:</strong> ${escapeHTML(chk.remediation)}</span>
            </div>
        ` : '');

        card.innerHTML = `
            <div class="status-header">
                <div style="display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap;">
                    <h3>${escapeHTML(chk.title)}</h3>
                    <span class="severity-pill ${severityClass}">${severityClass}</span>
                    <span class="sec-card-category">${escapeHTML(category)}</span>
                </div>
                <span class="status-badge ${badgeClass}">${escapeHTML(chk.statusText)}</span>
            </div>
            <p>${escapeHTML(chk.description)}</p>
            ${chk.detail ? `<div style="font-size: 0.75rem; font-family: var(--font-mono); color: var(--text-dim); background: rgba(0,0,0,0.25); padding: 0.4rem 0.6rem; border-radius: 6px; margin-top: 0.4rem;">${escapeHTML(chk.detail)}</div>` : ''}
            ${remediationHtml}
        `;
        grid.appendChild(card);
    });
}

async function triggerTargetedRemediation(checkId) {
    if (!confirm(`Apply automated security remediation for policy [${checkId}]?`)) return;

    try {
        showToast(`Remediating policy ${checkId}...`, 'info');
        const res = await fetch('/api/security/remediate', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ check_id: checkId })
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.message || 'Remediation failed');

        showToast(data.message || 'Policy remediated successfully!', 'success');
        runSecurityDashboardScan();
    } catch (err) {
        showToast(`Remediation failed: ${err.message}`, 'error');
    }
}

// ----------------------------------------------------
// FIREWALL & PORTS CONTROLLER
// ----------------------------------------------------

async function loadFirewallData() {
    const daemonElem = document.getElementById('fw-daemon-status');
    const inElem = document.getElementById('fw-default-in');
    const outElem = document.getElementById('fw-default-out');
    const rulesBody = document.getElementById('firewall-rules-body');
    const socketsBody = document.getElementById('listening-sockets-body');

    try {
        const res = await fetch('/api/security/firewall');
        if (!res.ok) throw new Error('Failed to query firewall API');
        const data = await res.json();

        if (daemonElem) {
            daemonElem.innerHTML = `<span class="${data.status === 'Active' ? 'badge-unlock' : 'badge-lock'}">UFW ${escapeHTML(data.status)}</span>`;
        }
        if (inElem) inElem.innerText = (data.default_incoming || 'deny').toUpperCase();
        if (outElem) outElem.innerText = (data.default_outgoing || 'allow').toUpperCase();

        // Render rules table
        if (rulesBody) {
            rulesBody.innerHTML = '';
            if (!data.rules || data.rules.length === 0) {
                rulesBody.innerHTML = `<tr><td colspan="6" style="text-align: center; color: var(--text-muted); padding: 1.5rem;">No active rules configured. Default policies apply.</td></tr>`;
            } else {
                data.rules.forEach(r => {
                    const tr = document.createElement('tr');
                    tr.innerHTML = `
                        <td style="font-family: var(--font-mono); color: var(--text-dim);">${r.num}</td>
                        <td style="font-family: var(--font-mono); font-weight: 700; color: #38bdf8;">${escapeHTML(r.to)}</td>
                        <td><span class="status-badge ${r.action === 'ALLOW' ? 'badge-unlock' : 'badge-lock'}">${escapeHTML(r.action)}</span></td>
                        <td style="font-family: var(--font-mono); font-size: 0.85rem;">${escapeHTML(r.from)}</td>
                        <td style="font-size: 0.85rem; color: var(--text-muted);">${escapeHTML(r.comment || '—')}</td>
                        <td style="text-align: right;">
                            <button class="btn btn-outline-danger btn-sm" onclick="deleteFirewallRule(${r.num})" title="Delete UFW Rule #${r.num}">🗑️ Delete</button>
                        </td>
                    `;
                    rulesBody.appendChild(tr);
                });
            }
        }

        // Render sockets table
        if (socketsBody) {
            socketsBody.innerHTML = '';
            if (!data.listening_sockets || data.listening_sockets.length === 0) {
                socketsBody.innerHTML = `<tr><td colspan="5" style="text-align: center; color: var(--text-muted); padding: 1.5rem;">No open sockets detected.</td></tr>`;
            } else {
                data.listening_sockets.forEach(s => {
                    const tr = document.createElement('tr');
                    tr.innerHTML = `
                        <td style="font-family: var(--font-mono); font-weight: 700; color: #38bdf8;">${escapeHTML(s.port)}</td>
                        <td style="font-family: var(--font-mono); font-size: 0.85rem;">${escapeHTML(s.protocol)}</td>
                        <td style="font-family: var(--font-mono); font-size: 0.85rem; color: var(--text-dim);">${escapeHTML(s.address)}</td>
                        <td style="font-size: 0.85rem; font-weight: 600;">${escapeHTML(s.service)}</td>
                        <td>
                            <span class="status-badge ${s.is_public ? 'badge-lock' : 'badge-unlock'}" style="font-size: 0.7rem;">
                                ${s.is_public ? '🌐 Public Internet' : '🔒 Localhost Only'}
                            </span>
                        </td>
                    `;
                    socketsBody.appendChild(tr);
                });
            }
        }

    } catch (err) {
        console.error("Firewall fetch failed:", err);
        showToast("Could not load firewall rules", "error");
    }
}

function openAddFirewallRuleModal() {
    const modal = document.getElementById('add-firewall-modal');
    if (modal) modal.style.display = 'flex';
}

function closeAddFirewallRuleModal() {
    const modal = document.getElementById('add-firewall-modal');
    if (modal) modal.style.display = 'none';
}

async function submitAddFirewallRule(e) {
    e.preventDefault();
    const port = document.getElementById('fw-input-port').value.trim();
    const policy = document.getElementById('fw-input-policy').value;
    const from = document.getElementById('fw-input-source').value.trim();
    const comment = document.getElementById('fw-input-comment').value.trim();

    if (!port) {
        showToast("Port specification is required", "error");
        return;
    }

    try {
        const btn = document.getElementById('btn-save-fw-rule');
        if (btn) btn.disabled = true;

        const res = await fetch('/api/security/firewall/rule', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                action: 'add',
                port: port,
                policy: policy,
                from: from || 'any',
                comment: comment
            })
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.message || 'Failed to add firewall rule');

        showToast(data.message || 'Firewall rule created!', 'success');
        closeAddFirewallRuleModal();
        document.getElementById('add-firewall-form').reset();
        loadFirewallData();
    } catch (err) {
        showToast(err.message, 'error');
    } finally {
        const btn = document.getElementById('btn-save-fw-rule');
        if (btn) btn.disabled = false;
    }
}

async function deleteFirewallRule(ruleNum) {
    if (!confirm(`Are you sure you want to delete UFW rule #${ruleNum}?`)) return;

    try {
        const res = await fetch('/api/security/firewall/rule', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                action: 'delete',
                rule_num: ruleNum
            })
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.message || 'Failed to delete rule');

        showToast(`Rule #${ruleNum} deleted`, 'success');
        loadFirewallData();
    } catch (err) {
        showToast(err.message, 'error');
    }
}

// ----------------------------------------------------
// AUDIT LOGS & THREAT FEED CONTROLLER
// ----------------------------------------------------

async function loadSecurityAuditLogs() {
    const tbody = document.getElementById('security-audit-tbody');
    const countBadge = document.getElementById('sec-audit-count');
    const q = document.getElementById('sec-log-search')?.value.trim() || '';
    const action = document.getElementById('sec-log-action-filter')?.value || '';
    const status = document.getElementById('sec-log-status-filter')?.value || '';

    try {
        const params = new URLSearchParams();
        if (q) params.set('q', q);
        if (action) params.set('action', action);
        if (status) params.set('status', status);
        params.set('limit', '80');

        const res = await fetch(`/api/security/logs?${params.toString()}`);
        if (!res.ok) throw new Error('Failed to query security logs');
        const logs = await res.json();

        if (countBadge) countBadge.innerText = logs.length;

        if (tbody) {
            tbody.innerHTML = '';
            if (!logs || logs.length === 0) {
                tbody.innerHTML = `<tr><td colspan="5" style="text-align: center; color: var(--text-muted); padding: 2rem;">No security events found matching current criteria.</td></tr>`;
            } else {
                logs.forEach(entry => {
                    const tr = document.createElement('tr');
                    const statusClass = (entry.status || 'info').toLowerCase();
                    tr.innerHTML = `
                        <td style="font-family: var(--font-mono); font-size: 0.78rem; color: var(--text-dim); white-space: nowrap;">${escapeHTML(entry.timestamp)}</td>
                        <td style="font-family: var(--font-mono); font-size: 0.82rem; color: #38bdf8;">${escapeHTML(entry.ip)}</td>
                        <td><span style="font-size: 0.82rem; font-family: var(--font-mono); font-weight: 600;">${escapeHTML(entry.action)}</span></td>
                        <td><span class="audit-badge ${statusClass}">${escapeHTML(entry.status)}</span></td>
                        <td style="font-size: 0.82rem; color: var(--text-muted); max-width: 450px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;" title="${escapeHTML(entry.details)}">${escapeHTML(entry.details)}</td>
                    `;
                    tbody.appendChild(tr);
                });
            }
        }
    } catch (err) {
        console.error("Audit log query failed:", err);
        if (tbody) tbody.innerHTML = `<tr><td colspan="5" style="text-align: center; color: #ef4444; padding: 1.5rem;">Failed to load audit logs.</td></tr>`;
    }
}

function debounceSecurityLogSearch() {
    clearTimeout(logSearchDebounceTimer);
    logSearchDebounceTimer = setTimeout(() => {
        loadSecurityAuditLogs();
    }, 300);
}

function exportSecurityLogs(format) {
    const q = document.getElementById('sec-log-search')?.value.trim() || '';
    const action = document.getElementById('sec-log-action-filter')?.value || '';
    const status = document.getElementById('sec-log-status-filter')?.value || '';

    const params = new URLSearchParams();
    if (q) params.set('q', q);
    if (action) params.set('action', action);
    if (status) params.set('status', status);
    params.set('export', format);

    window.open(`/api/security/logs?${params.toString()}`, '_blank');
}

// ----------------------------------------------------
// SESSIONS & DEVICE ACCESS CONTROLLER
// ----------------------------------------------------

async function loadActiveSessions() {
    const tbody = document.getElementById('active-sessions-tbody');
    try {
        const res = await fetch('/api/security/sessions');
        if (!res.ok) throw new Error('Failed to query sessions API');
        const sessions = await res.json();

        if (tbody) {
            tbody.innerHTML = '';
            if (!sessions || sessions.length === 0) {
                tbody.innerHTML = `<tr><td colspan="6" style="text-align: center; color: var(--text-muted); padding: 1.5rem;">No active secondary sessions registered.</td></tr>`;
            } else {
                sessions.forEach(s => {
                    const tr = document.createElement('tr');
                    tr.innerHTML = `
                        <td style="font-family: var(--font-mono); font-size: 0.82rem; color: #38bdf8;">
                            ${escapeHTML(s.token_prefix)}
                            ${s.is_current ? '<span class="badge" style="background: rgba(16, 185, 129, 0.2); color: #6ee7b7; font-size: 0.65rem; margin-left: 0.4rem;">This Device</span>' : ''}
                        </td>
                        <td style="font-family: var(--font-mono); font-size: 0.85rem;">${escapeHTML(s.ip)}</td>
                        <td style="font-size: 0.82rem; color: var(--text-muted); max-width: 250px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;" title="${escapeHTML(s.user_agent)}">${escapeHTML(s.user_agent)}</td>
                        <td style="font-size: 0.82rem; color: var(--text-dim);">${escapeHTML(s.created_at)}</td>
                        <td style="font-family: var(--font-mono); font-size: 0.82rem; color: #fbbf24;">${escapeHTML(s.expires_in)}</td>
                        <td style="text-align: right;">
                            ${s.is_current ? '<span style="font-size: 0.75rem; color: var(--text-dim);">Active Now</span>' : `<button class="btn btn-outline-danger btn-sm" onclick="revokeSession('${escapeHTML(s.token_key)}')">Revoke 🚪</button>`}
                        </td>
                    `;
                    tbody.appendChild(tr);
                });
            }
        }
    } catch (err) {
        console.error("Sessions fetch failed:", err);
    }
}

async function revokeSession(tokenKey) {
    if (!confirm('Are you sure you want to terminate this administrator session?')) return;

    try {
        const res = await fetch('/api/security/sessions/revoke', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ token_key: tokenKey })
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.message || 'Revoke failed');

        showToast('Session terminated', 'success');
        loadActiveSessions();
    } catch (err) {
        showToast(err.message, 'error');
    }
}

async function revokeAllOtherSessions() {
    if (!confirm('🚨 Panic Action: Revoke ALL other active administrator sessions across all devices?')) return;

    try {
        const res = await fetch('/api/security/sessions/revoke', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ all_other: true })
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.message || 'Panic revoke failed');

        showToast(`Revoked ${data.revoked || 0} other sessions!`, 'success');
        loadActiveSessions();
    } catch (err) {
        showToast(err.message, 'error');
    }
}

function triggerEmergencySiteLock() {
    if (!confirm('🔒 Emergency Global Site Lock: Do you want to lock all website webroots to read-only mode to prevent tampering?')) return;

    if (typeof allSitesData !== 'undefined' && allSitesData && allSitesData.length > 0) {
        allSitesData.forEach(s => {
            triggerAction('site-lock', [s.domain]);
        });
        showToast('Triggered lockdown sequence for all websites.', 'warning');
    } else {
        showToast('No active websites detected to lock.', 'info');
    }
}

// ----------------------------------------------------
// SSL & TLS HEALTH CONTROLLER
// ----------------------------------------------------

async function loadSecuritySSLData() {
    const subjectElem = document.getElementById('gui-ssl-subject');
    const issuerElem = document.getElementById('gui-ssl-issuer');
    const expiryElem = document.getElementById('gui-ssl-expiry');
    const badgeElem = document.getElementById('gui-ssl-status-badge');
    const tbody = document.getElementById('sites-ssl-tbody');

    try {
        const res = await fetch('/api/security/ssl');
        if (!res.ok) throw new Error('Failed to query SSL certificates API');
        const data = await res.json();

        if (subjectElem) subjectElem.innerText = data.gui_cert_subject || 'AgilePanel Dashboard TLS';
        if (issuerElem) issuerElem.innerText = `Issuer: ${data.gui_cert_issuer || 'Self-Signed / AgilePanel CA'}`;
        if (expiryElem) expiryElem.innerText = `Expires: ${data.gui_cert_expires || 'Never'} (${data.gui_days_remaining || 0} days remaining)`;
        if (badgeElem) {
            badgeElem.innerText = data.gui_cert_valid ? (data.gui_is_self_signed ? 'Self-Signed TLS' : 'CA-Signed TLS') : 'Unencrypted';
            badgeElem.className = data.gui_cert_valid ? 'badge-unlock' : 'badge-lock';
        }

        if (tbody) {
            tbody.innerHTML = '';
            if (!data.sites || data.sites.length === 0) {
                tbody.innerHTML = `<tr><td colspan="6" style="text-align: center; color: var(--text-muted); padding: 1.5rem;">No hosted websites found in panel state.</td></tr>`;
            } else {
                data.sites.forEach(site => {
                    const tr = document.createElement('tr');
                    const daysColor = site.days_remaining < 15 ? '#ef4444' : (site.days_remaining < 30 ? '#f59e0b' : '#34d399');
                    tr.innerHTML = `
                        <td style="font-weight: 700; color: #38bdf8;">${escapeHTML(site.domain)}</td>
                        <td><span class="status-badge ${site.has_ssl ? 'badge-unlock' : 'badge-lock'}">${site.has_ssl ? (site.is_self_signed ? 'Self-Signed' : 'Let\'s Encrypt TLS') : 'No SSL'}</span></td>
                        <td style="font-size: 0.85rem; color: var(--text-muted);">${escapeHTML(site.issuer)}</td>
                        <td style="font-family: var(--font-mono); font-size: 0.82rem;">${escapeHTML(site.expires_at)}</td>
                        <td style="font-family: var(--font-mono); font-weight: 700; color: ${daysColor};">${site.days_remaining}d</td>
                        <td style="text-align: right;">
                            <button class="btn btn-secondary btn-sm" onclick="triggerAction('site-ssl', ['${escapeHTML(site.domain)}'])" title="Renew SSL Certificate">🔄 Renew SSL</button>
                        </td>
                    `;
                    tbody.appendChild(tr);
                });
            }
        }
    } catch (err) {
        console.error("SSL fetch failed:", err);
    }
}

// ----------------------------------------------------
// LOG STREAM CONSOLE MODAL & CONTROLS
// ----------------------------------------------------
function openTerminalModal() {
    const term = document.getElementById('terminal-modal');
    const output = document.getElementById('terminal-output');
    const closeBtn = document.getElementById('terminal-close-btn');
    const pill = document.getElementById('terminal-status-pill');
    
    output.innerHTML = '';
    closeBtn.disabled = true;
    if (pill) {
        pill.innerText = 'RUNNING';
        pill.className = 'terminal-pill running';
    }
    term.classList.add('open');
}

function closeTerminalModal() {
    document.getElementById('terminal-modal').classList.remove('open');
}

function filterTerminalLogs() {
    const query = (document.getElementById('terminal-search-input')?.value || '').toLowerCase();
    const lines = document.querySelectorAll('#terminal-output > div');
    lines.forEach(line => {
        if (!query || line.innerText.toLowerCase().includes(query)) {
            line.style.display = '';
        } else {
            line.style.display = 'none';
        }
    });
}

function copyTerminalLogs() {
    const output = document.getElementById('terminal-output');
    if (!output) return;
    const text = output.innerText;
    if (!text) {
        showToast("No terminal output to copy.", "info");
        return;
    }
    navigator.clipboard.writeText(text).then(() => {
        showToast("Terminal logs copied to clipboard!", "success");
    }).catch(() => {
        showToast("Failed to copy logs to clipboard.", "error");
    });
}

function clearTerminalLogs() {
    const output = document.getElementById('terminal-output');
    if (output) output.innerHTML = '';
}

function switchManageSubTab(tab) {
    document.querySelectorAll('.modal-subtab-btn').forEach(btn => btn.classList.remove('active'));
    document.querySelectorAll('.modal-tab-content').forEach(p => p.classList.remove('active'));

    const btn = document.getElementById(`btn-subtab-${tab}`);
    const pane = document.getElementById(`subtab-pane-${tab}`);
    if (btn) btn.classList.add('active');
    if (pane) pane.classList.add('active');
}

async function triggerAction(action, args) {
    openTerminalModal();
    const output = document.getElementById('terminal-output');
    const statusText = document.getElementById('terminal-status-text');
    const closeBtn = document.getElementById('terminal-close-btn');
    const pill = document.getElementById('terminal-status-pill');

    statusText.innerText = "Connecting to command router...";
    let hasError = false;

    try {
        const response = await fetch('/api/action', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ action, args })
        });

        if (!response.ok) {
            const errText = await response.text();
            throw new Error(errText || "Backend router error");
        }

        const reader = response.body.getReader();
        const decoder = new TextDecoder("utf-8");
        statusText.innerText = "Running action - streaming logs...";

        let done = false;
        let buffer = "";

        while (!done) {
            const { value, done: streamDone } = await reader.read();
            done = streamDone;
            
            if (value) {
                buffer += decoder.decode(value, { stream: !done });
                const lines = buffer.split("\n\n");
                buffer = lines.pop();

                lines.forEach(line => {
                    if (line.startsWith("data: ")) {
                        const content = line.substring(6);
                        const span = document.createElement('div');
                        if (content.startsWith("ERR: ") || content.includes("Exit error") || content.includes("failed")) {
                            span.className = 'err-line';
                            span.innerText = content.startsWith("ERR: ") ? content.substring(5) : content;
                            hasError = true;
                        } else if (content.startsWith("Running Command:")) {
                            span.className = 'cmd-line';
                            span.innerText = `> ${content}`;
                        } else {
                            span.innerText = content;
                        }
                        output.appendChild(span);

                        const autoscroll = document.getElementById('terminal-autoscroll');
                        if (!autoscroll || autoscroll.checked) {
                            output.scrollTop = output.scrollHeight;
                        }
                    }
                });
            }
        }

        statusText.innerText = hasError ? "Execution finished with warnings/errors." : "Finished execution successfully.";
        if (pill) {
            pill.innerText = hasError ? "WARNING" : "SUCCESS";
            pill.className = hasError ? "terminal-pill failed" : "terminal-pill success";
        }

    } catch (err) {
        const span = document.createElement('div');
        span.className = 'err-line';
        span.innerText = `ERROR: Failed to run command pipeline: ${err.message}`;
        output.appendChild(span);
        statusText.innerText = "Failed execution.";
        if (pill) {
            pill.innerText = "FAILED";
            pill.className = "terminal-pill failed";
        }
    } finally {
        closeBtn.disabled = false;
        loadStatus();
        loadSites();
        if (action === 'server-secure') {
            runSecurityDashboardScan();
        }
    }
}

// Dropdown toggling handler
function toggleDropdown(btn) {
    if (window.event) window.event.stopPropagation();
    const menu = btn.nextElementSibling;
    const show = menu.classList.contains('show');
    
    // Hide all other dropdowns
    document.querySelectorAll('.dropdown-menu').forEach(m => m.classList.remove('show'));
    
    if (!show) {
        menu.classList.add('show');
    }
}

// Close dropdowns when clicking outside or scrolling
window.addEventListener('click', () => {
    document.querySelectorAll('.dropdown-menu').forEach(m => m.classList.remove('show'));
});

window.addEventListener('scroll', () => {
    document.querySelectorAll('.dropdown-menu').forEach(m => m.classList.remove('show'));
}, { passive: true });

// ----------------------------------------------------
// HISTORICAL TREND GRAPH SYSTEM (SVG)
// ----------------------------------------------------
let currentGraphRange = 'today';
let metricsHistoryData = null;

async function loadMetricsHistory() {
    try {
        const res = await fetch('/api/metrics/history');
        if (!res.ok) throw new Error("Metrics history error");
        metricsHistoryData = await res.json();
        renderResourceGraphs();
    } catch (err) {
        console.error("Failed to load metrics history:", err);
    }
}

function setGraphRange(range) {
    currentGraphRange = range;
    
    const todayBtn = document.getElementById('btn-graph-today');
    const thisweekBtn = document.getElementById('btn-graph-thisweek');
    
    if (range === 'today') {
        if (todayBtn) todayBtn.className = 'btn btn-primary btn-sm';
        if (thisweekBtn) thisweekBtn.className = 'btn btn-secondary btn-sm';
    } else {
        if (todayBtn) todayBtn.className = 'btn btn-secondary btn-sm';
        if (thisweekBtn) thisweekBtn.className = 'btn btn-primary btn-sm';
    }
    
    renderResourceGraphs();
}

function renderResourceGraphs() {
    if (!metricsHistoryData) return;
    
    const slicedData = currentGraphRange === 'today' ? metricsHistoryData.today : metricsHistoryData.thisweek;
    if (!slicedData || slicedData.length === 0) return;
    
    // 1. CPU Chart
    const cpuPoints = slicedData.map(d => d.cpu !== undefined ? d.cpu : d.CPU);
    const cpuLabels = slicedData.map(d => d.label !== undefined ? d.label : d.Label);
    const cpuAvg = cpuPoints.reduce((a, b) => a + b, 0) / cpuPoints.length;
    document.getElementById('graph-cpu-avg').innerText = `Avg: ${cpuAvg.toFixed(1)}%`;
    drawSvgLineChart('graph-container-cpu', cpuPoints, cpuLabels, '#3b82f6', 'rgba(59, 130, 246, 0.08)');
    
    // 2. RAM Chart
    const ramPoints = slicedData.map(d => d.ram !== undefined ? d.ram : d.RAM);
    const ramLabels = slicedData.map(d => d.label !== undefined ? d.label : d.Label);
    const ramAvg = ramPoints.reduce((a, b) => a + b, 0) / ramPoints.length;
    document.getElementById('graph-ram-avg').innerText = `Avg: ${ramAvg.toFixed(1)}%`;
    drawSvgLineChart('graph-container-ram', ramPoints, ramLabels, '#10b981', 'rgba(16, 185, 129, 0.08)');
}

function drawSvgLineChart(containerId, points, labels, strokeColor, fillColor) {
    const container = document.getElementById(containerId);
    if (!container) return;
    
    const width = container.clientWidth || 340;
    const height = 140;
    
    const paddingLeft = 32;
    const paddingRight = 10;
    const paddingTop = 15;
    const paddingBottom = 20;
    
    const chartWidth = width - paddingLeft - paddingRight;
    const chartHeight = height - paddingTop - paddingBottom;
    
    const minVal = 0;
    const maxVal = 100;
    
    const xStep = chartWidth / (points.length - 1 || 1);
    
    const coords = points.map((val, index) => {
        const x = paddingLeft + index * xStep;
        const y = paddingTop + chartHeight - ((val - minVal) / (maxVal - minVal)) * chartHeight;
        return { x, y };
    });
    
    // Generate SVG path for smooth line
    let dLine = `M ${coords[0].x} ${coords[0].y}`;
    for (let i = 1; i < coords.length; i++) {
        dLine += ` L ${coords[i].x} ${coords[i].y}`;
    }
    
    let dArea = `${dLine} L ${coords[coords.length - 1].x} ${paddingTop + chartHeight} L ${coords[0].x} ${paddingTop + chartHeight} Z`;
    
    let gridLinesHtml = "";
    [0, 25, 50, 75, 100].forEach(pct => {
        const y = paddingTop + chartHeight - (pct / 100) * chartHeight;
        gridLinesHtml += `
            <line x1="${paddingLeft}" y1="${y}" x2="${width - paddingRight}" y2="${y}" stroke="rgba(255,255,255,0.03)" stroke-width="1" />
            <text x="5" y="${y + 4}" fill="rgba(255,255,255,0.18)" font-size="9" font-family="var(--font-mono)">${pct}%</text>
        `;
    });
    
    let labelsHtml = "";
    if (labels.length > 0) {
        const labelIndices = [0, Math.floor(labels.length / 2), labels.length - 1];
        labelIndices.forEach(idx => {
            if (coords[idx]) {
                const x = coords[idx].x;
                const textAnchor = idx === 0 ? "start" : idx === labels.length - 1 ? "end" : "middle";
                labelsHtml += `
                    <text x="${x}" y="${height - 2}" fill="rgba(255,255,255,0.22)" font-size="9" text-anchor="${textAnchor}" font-family="var(--font-mono)">${labels[idx]}</text>
                `;
            }
        });
    }
    
    const filterId = `glow-${containerId}`;
    
    const svgHtml = `
        <svg width="100%" height="${height}" style="overflow: visible;">
            <defs>
                <filter id="${filterId}" x="-20%" y="-20%" width="140%" height="140%">
                    <feDropShadow dx="0" dy="3" stdDeviation="3" flood-color="${strokeColor}" flood-opacity="0.2"/>
                </filter>
            </defs>
            
            ${gridLinesHtml}
            ${labelsHtml}
            
            <path d="${dArea}" fill="${fillColor}" stroke="none" />
            <path d="${dLine}" fill="none" stroke="${strokeColor}" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" filter="url(#${filterId})" />
            <circle cx="${coords[coords.length - 1].x}" cy="${coords[coords.length - 1].y}" r="3.5" fill="${strokeColor}" stroke="#fff" stroke-width="1" />
        </svg>
    `;
    
    container.innerHTML = svgHtml;
}

// Secondary Session Authentication state and handlers
let guiAuthStatus = { initialized: false, enabled: false, authenticated: false };

async function checkGuiAuthStatus() {
    try {
        const res = await fetch('/api/auth/status');
        if (!res.ok) throw new Error("Status API request failed");
        guiAuthStatus = await res.json();
        
        renderAuthOverlay();
        renderLockSettingsUI();
        
        const logoutLink = document.getElementById('sidebar-logout-link');
        if (logoutLink) {
            logoutLink.style.display = 'flex';
            if (guiAuthStatus.enabled && guiAuthStatus.authenticated) {
                logoutLink.innerHTML = `<span class="nav-icon">🔒</span> Lock Panel`;
            } else {
                logoutLink.innerHTML = `<span class="nav-icon">🚪</span> Logout Session`;
            }
        }

        const authBadge = document.getElementById('header-auth-badge');
        if (authBadge) {
            if (guiAuthStatus.enabled && guiAuthStatus.authenticated) {
                authBadge.innerHTML = `<span class="pulse-dot"></span> PIN Protected`;
            } else {
                authBadge.innerHTML = `<span class="pulse-dot"></span> Active`;
            }
        }

        const dashLockStatus = document.getElementById('dash-lock-status');
        if (dashLockStatus) {
            dashLockStatus.innerText = guiAuthStatus.enabled ? "Active (PIN)" : "Basic Auth Only";
            dashLockStatus.style.color = guiAuthStatus.enabled ? "#34d399" : "#f59e0b";
        }
    } catch (err) {
        console.error("checkGuiAuthStatus failed:", err);
    }
}

async function triggerLogout() {
    if (confirm("Are you sure you want to log out of AgilePanel?")) {
        try {
            // 1. Invalidate secondary GUI session cookie
            await fetch('/api/auth/logout', { method: 'POST' });
        } catch (e) {
            console.warn("Secondary session logout call failed:", e);
        }

        try {
            // 2. Invalidate browser HTTP Basic Auth credential cache.
            // Opening an XMLHttpRequest with deliberate bogus credentials forces the browser
            // to overwrite its cached credentials for this realm.
            const xhr = new XMLHttpRequest();
            xhr.open("GET", "/api/auth/logout?basic=1", false, "logout", "logout");
            xhr.send();
        } catch (e) {
            // Expected 401 response from server
        }

        showToast("Logged out successfully. Redirecting...", "info");
        setTimeout(() => {
            // Navigate back to root which will now challenge for fresh credentials
            window.location.href = "/";
        }, 400);
    }
}

function renderAuthOverlay() {
    const setupModal = document.getElementById('setup-auth-modal');
    const loginModal = document.getElementById('login-auth-modal');
    
    if (setupModal && loginModal) {
        setupModal.classList.remove('open');
        loginModal.classList.remove('open');
        
        if (guiAuthStatus.enabled && !guiAuthStatus.authenticated) {
            if (!guiAuthStatus.initialized) {
                setupModal.classList.add('open');
            } else {
                loginModal.classList.add('open');
            }
        }
    }
}

function renderLockSettingsUI() {
    const btn = document.getElementById('btn-toggle-gui-lock');
    if (!btn) return;
    
    if (!guiAuthStatus.initialized) {
        btn.innerText = "🔒 Set Credentials First";
        btn.className = "btn btn-secondary";
        btn.disabled = false;
    } else {
        if (guiAuthStatus.enabled) {
            btn.innerText = "🔴 Disable Session Lock";
            btn.className = "btn btn-danger";
        } else {
            btn.innerText = "🟢 Enable Session Lock";
            btn.className = "btn btn-success";
        }
        btn.disabled = false;
    }
}

async function submitAuthSignup(e) {
    e.preventDefault();
    const pin = document.getElementById('setup-pin').value.trim();
    
    try {
        const res = await fetch('/api/auth/signup', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ pin })
        });
        if (!res.ok) throw new Error(await res.text());
        
        alert("Secondary session security PIN configured successfully!");
        location.reload();
    } catch (err) {
        alert("Configuration failed: " + err.message);
    }
}

async function submitAuthLogin(e) {
    e.preventDefault();
    const pin = document.getElementById('login-pin').value.trim();
    const errorMsg = document.getElementById('login-error-msg');
    
    if (errorMsg) errorMsg.style.display = 'none';
    
    try {
        const res = await fetch('/api/auth/login', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ pin })
        });
        if (!res.ok) throw new Error("Invalid PIN code.");
        
        location.reload();
    } catch (err) {
        if (errorMsg) {
            errorMsg.innerText = err.message;
            errorMsg.style.display = 'block';
        }
    }
}

async function toggleGuiSessionLock() {
    if (!guiAuthStatus.initialized) {
        const setupModal = document.getElementById('setup-auth-modal');
        if (setupModal) setupModal.classList.add('open');
        return;
    }
    
    const newStatus = !guiAuthStatus.enabled;
    const actionText = newStatus ? "enable" : "disable";
    if (confirm(`Are you sure you want to ${actionText} the secondary session lock layer?`)) {
        try {
            const res = await fetch('/api/auth/toggle', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ enabled: newStatus })
            });
            if (!res.ok) throw new Error(await res.text());
            checkGuiAuthStatus();
        } catch (err) {
            alert("Failed to toggle: " + err.message);
        }
    }
}

// Manage Website Operations Modal Actions
let currentManageDomain = "";
let currentManageIsLocked = false;

function toggleS3AccessUI(enabled) {
    const destRow = document.getElementById('manage-backup-destination-row');
    const versionsRow = document.getElementById('manage-s3-versions-row');
    const backupsHistoryContainer = document.getElementById('manage-s3-backups-container');
    const backupsHistoryCard = backupsHistoryContainer ? backupsHistoryContainer.closest('div') : null;
    const destSelect = document.getElementById('manage-backup-destination-select');

    if (enabled) {
        // S3 access is on — show destination row so user can choose local or S3
        if (destRow) destRow.style.display = 'flex';
        // Show or hide S3-specific panels based on current destination selection
        const dest = destSelect ? destSelect.value : 'local';
        const isS3Dest = dest === 's3';
        if (versionsRow) versionsRow.style.display = isS3Dest ? 'flex' : 'none';
        if (backupsHistoryCard) backupsHistoryCard.style.display = isS3Dest ? 'block' : 'none';
    } else {
        // S3 access is off — keep destination row visible but force local, hide S3-only controls
        if (destRow) destRow.style.display = 'flex';
        if (versionsRow) versionsRow.style.display = 'none';
        if (backupsHistoryCard) backupsHistoryCard.style.display = 'none';

        // Force destination to local if S3 is disabled
        if (destSelect && destSelect.value !== 'local') {
            destSelect.value = 'local';
            updateBackupDestination('local');
        }
    }
}

function openManageModal(domain, isLocked) {
    currentManageDomain = domain;
    currentManageIsLocked = isLocked;
    
    document.getElementById('manage-site-title').innerText = `Manage: ${domain}`;
    document.getElementById('manage-site-desc').innerText = `Select an administrative operation to run on this site.`;
    
    const lockText = document.getElementById('manage-lock-text');
    if (lockText) {
        lockText.innerHTML = isLocked ? "🔓 Unlock Site" : "🔒 Lock Site";
    }
    
    const lockCard = document.getElementById('manage-lock-card');
    if (lockCard) {
        lockCard.setAttribute('data-tooltip', isLocked ? 
            "Unlock the site to allow editing, writing, and code modifications on the files." : 
            "Lock the site to prevent any modifications or uploads to files, protecting it against changes.");
    }
    
    const site = (window.allSites || []).find(s => s.domain.toLowerCase() === domain.toLowerCase());
    const dbCredsContainer = document.getElementById('manage-db-creds-container');
    if (site) {
        document.getElementById('manage-staging-unlocked-toggle').checked = site.staging_unlocked || false;
        document.getElementById('manage-backup-interval-select').value = site.backup_interval || 'none';
        document.getElementById('manage-backup-destination-select').value = site.backup_destination || 'local';
        document.getElementById('manage-s3-versions-select').value = site.s3_backup_versions || 5;
        document.getElementById('manage-s3-enabled-toggle').checked = site.s3_enabled || false;
        toggleS3AccessUI(site.s3_enabled || false);
        
        // Database credentials
        document.getElementById('manage-db-user').value = site.db_user || '';
        document.getElementById('manage-db-pass').value = site.db_pass || '';
        if (dbCredsContainer) {
            dbCredsContainer.style.display = site.type === 'html' ? 'none' : 'block';
        }
    } else {
        document.getElementById('manage-staging-unlocked-toggle').checked = false;
        document.getElementById('manage-backup-interval-select').value = 'none';
        document.getElementById('manage-backup-destination-select').value = 'local';
        document.getElementById('manage-s3-versions-select').value = 5;
        document.getElementById('manage-s3-enabled-toggle').checked = false;
        toggleS3AccessUI(false);
        
        document.getElementById('manage-db-user').value = '';
        document.getElementById('manage-db-pass').value = '';
        if (dbCredsContainer) {
            dbCredsContainer.style.display = 'block';
        }
    }
    
    loadS3BackupList(domain);
    
    document.getElementById('manage-modal').classList.add('open');
}

function closeManageModal() {
    document.getElementById('manage-modal').classList.remove('open');
}

function runManageAction(action) {
    closeManageModal();
    const domain = currentManageDomain;
    
    if (action === 'lock') {
        triggerAction(currentManageIsLocked ? 'site-unlock' : 'site-lock', [domain]);
    } else if (action === 'cache') {
        triggerAction('site-cache', [domain]);
    } else if (action === 'perms') {
        triggerAction('site-perms', [domain]);
    } else if (action === 'ssl') {
        triggerAction('site-ssl', [domain]);
    } else if (action === 'backup') {
        triggerAction('site-backup', [domain]);
    } else if (action === 'restore') {
        openRestoreWizard(domain, 'local', '');
    } else if (action === 'reinstall') {
        if (confirm(`Are you sure you want to reinstall '${domain}'?\n\nThis will wipe all existing files and database schemas!`)) {
            triggerAction('site-reinstall', [domain]);
        }
    } else if (action === 'delete') {
        confirmDeleteSite(domain);
    }
}

// Initializer
document.addEventListener("DOMContentLoaded", () => {
    checkGuiAuthStatus().then(() => {
        if (!guiAuthStatus.enabled || guiAuthStatus.authenticated) {
            loadStatus();
            loadMetricsHistory();
            runSecurityDashboardScan();
            setInterval(loadStatus, 4000);
            setInterval(loadMetricsHistory, 60000);
        }
    });

    // Enforce only digits input on PIN inputs
    ['setup-pin', 'login-pin'].forEach(id => {
        const el = document.getElementById(id);
        if (el) {
            el.addEventListener('keydown', (e) => {
                if (['Backspace', 'Delete', 'Tab', 'Escape', 'Enter', 'ArrowLeft', 'ArrowRight'].includes(e.key)) {
                    return;
                }
                if (e.key < '0' || e.key > '9') {
                    e.preventDefault();
                }
            });
        }
    });
});

// File Context Menu implementation
let contextMenuTargetFile = null;

function showFileContextMenu(e, file) {
    contextMenuTargetFile = file;
    const menu = document.getElementById('file-context-menu');
    if (!menu) return;
    
    // Position menu at cursor coordinates
    menu.style.left = `${e.clientX}px`;
    menu.style.top = `${e.clientY}px`;
    menu.style.display = 'block';
    
    const isZip = file.name.endsWith('.zip');
    const isConfDir = fileCurrentPath === 'conf';
    const ext = file.name.split('.').pop().toLowerCase();
    const imageExtensions = ['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp', 'ico'];
    const editableExtensions = ['html', 'css', 'js', 'php', 'txt', 'json', 'conf', 'ini', 'htaccess'];
    const isImage = !file.isDir && imageExtensions.includes(ext);

    // Toggle item visibility in context menu
    const btnPreview = document.getElementById('ctx-preview');
    if (btnPreview) btnPreview.style.display = isImage ? 'block' : 'none';

    const btnEdit = document.getElementById('ctx-edit');
    if (btnEdit) btnEdit.style.display = (!file.isDir && editableExtensions.includes(ext)) ? 'block' : 'none';

    const btnDownload = document.getElementById('ctx-download');
    if (btnDownload) btnDownload.style.display = !file.isDir ? 'block' : 'none';

    const btnDup = document.getElementById('ctx-duplicate');
    if (btnDup) btnDup.style.display = (!file.isDir && !isConfDir) ? 'block' : 'none';

    const btnCopy = document.getElementById('ctx-copy');
    if (btnCopy) btnCopy.style.display = !isConfDir ? 'block' : 'none';

    const btnRename = document.getElementById('ctx-rename');
    if (btnRename) btnRename.style.display = !isConfDir ? 'block' : 'none';

    const btnChmod = document.getElementById('ctx-chmod');
    if (btnChmod) btnChmod.style.display = !isConfDir ? 'block' : 'none';

    const btnSize = document.getElementById('ctx-size');
    if (btnSize) btnSize.style.display = (file.isDir && !isConfDir) ? 'block' : 'none';

    const btnZip = document.getElementById('ctx-zip');
    if (btnZip) btnZip.style.display = (!isZip && !isConfDir) ? 'block' : 'none';

    const btnUnzip = document.getElementById('ctx-unzip');
    if (btnUnzip) btnUnzip.style.display = (isZip && !isConfDir) ? 'block' : 'none';

    const btnDelete = document.getElementById('ctx-delete');
    if (btnDelete) btnDelete.style.display = !isConfDir ? 'block' : 'none';
}

// Close context menu on left click anywhere
document.addEventListener('click', () => {
    const menu = document.getElementById('file-context-menu');
    if (menu) menu.style.display = 'none';
});

async function handleContextAction(action) {
    if (!contextMenuTargetFile || !fileCurrentDomain) return;
    const file = contextMenuTargetFile;
    const filePath = fileCurrentPath ? `${fileCurrentPath}/${file.name}` : file.name;
    
    switch (action) {
        case 'preview':
            openImagePreview(file.name);
            break;
        case 'edit':
            openFileEditor(file.name);
            break;
        case 'download':
            downloadFileContent(file.name);
            break;
        case 'duplicate':
            duplicateFilePrompt(filePath, file.name);
            break;
        case 'copy':
            copyFilePrompt(filePath, file.name);
            break;
        case 'rename':
            renameFilePrompt(filePath, file.name);
            break;
        case 'chmod':
            openChmodModal(file);
            break;
        case 'size':
            calculateFolderSize(filePath, file.name);
            break;
        case 'zip':
            zipFileOrFolder(filePath);
            break;
        case 'unzip':
            unzipFile(filePath);
            break;
        case 'delete':
            deleteFileConfirm(filePath, file.name);
            break;
    }
}

// Chmod Permissions Modal Logic
let chmodTargetFile = null;

function openChmodModal(file) {
    if (!file) return;
    chmodTargetFile = file;
    const modal = document.getElementById('chmod-modal');
    if (!modal) return;

    const filePath = fileCurrentPath ? `${fileCurrentPath}/${file.name}` : file.name;
    document.getElementById('chmod-target-name').innerText = `Target: ${filePath} (${file.isDir ? 'Directory' : 'File'})`;

    const recRow = document.getElementById('chmod-recursive-row');
    if (recRow) {
        recRow.style.display = file.isDir ? 'flex' : 'none';
        document.getElementById('chmod-recursive').checked = false;
    }

    parseModeToOctalAndCheckboxes(file.mode || '');
    modal.classList.add('open');
}

function openChmodModalForPath(filePath, modeStr, isDir) {
    const name = filePath.split('/').pop();
    openChmodModal({ name, mode: modeStr, isDir });
}

function closeChmodModal() {
    const modal = document.getElementById('chmod-modal');
    if (modal) modal.classList.remove('open');
    chmodTargetFile = null;
}

function parseModeToOctalAndCheckboxes(modeStr) {
    const s = modeStr.length >= 9 ? modeStr.slice(-9) : "rw-r--r--";
    
    document.getElementById('chmod-ur').checked = (s[0] === 'r');
    document.getElementById('chmod-uw').checked = (s[1] === 'w');
    document.getElementById('chmod-ux').checked = (s[2] === 'x');

    document.getElementById('chmod-gr').checked = (s[3] === 'r');
    document.getElementById('chmod-gw').checked = (s[4] === 'w');
    document.getElementById('chmod-gx').checked = (s[5] === 'x');

    document.getElementById('chmod-or').checked = (s[6] === 'r');
    document.getElementById('chmod-ow').checked = (s[7] === 'w');
    document.getElementById('chmod-ox').checked = (s[8] === 'x');

    updateChmodFromCheckboxes();
}

function updateChmodFromCheckboxes() {
    let u = 0, g = 0, o = 0;
    if (document.getElementById('chmod-ur').checked) u += 4;
    if (document.getElementById('chmod-uw').checked) u += 2;
    if (document.getElementById('chmod-ux').checked) u += 1;

    if (document.getElementById('chmod-gr').checked) g += 4;
    if (document.getElementById('chmod-gw').checked) g += 2;
    if (document.getElementById('chmod-gx').checked) g += 1;

    if (document.getElementById('chmod-or').checked) o += 4;
    if (document.getElementById('chmod-ow').checked) o += 2;
    if (document.getElementById('chmod-ox').checked) o += 1;

    document.getElementById('chmod-octal').value = `0${u}${g}${o}`;
}

function updateCheckboxesFromChmod() {
    const val = document.getElementById('chmod-octal').value.trim();
    const octal = val.startsWith('0') ? val.slice(1) : val;
    if (octal.length === 3) {
        const u = parseInt(octal[0], 10) || 0;
        const g = parseInt(octal[1], 10) || 0;
        const o = parseInt(octal[2], 10) || 0;

        document.getElementById('chmod-ur').checked = Boolean(u & 4);
        document.getElementById('chmod-uw').checked = Boolean(u & 2);
        document.getElementById('chmod-ux').checked = Boolean(u & 1);

        document.getElementById('chmod-gr').checked = Boolean(g & 4);
        document.getElementById('chmod-gw').checked = Boolean(g & 2);
        document.getElementById('chmod-gx').checked = Boolean(g & 1);

        document.getElementById('chmod-or').checked = Boolean(o & 4);
        document.getElementById('chmod-ow').checked = Boolean(o & 2);
        document.getElementById('chmod-ox').checked = Boolean(o & 1);
    }
}

async function submitChmod(e) {
    e.preventDefault();
    if (!chmodTargetFile || !fileCurrentDomain) return;

    const filePath = fileCurrentPath ? `${fileCurrentPath}/${chmodTargetFile.name}` : chmodTargetFile.name;
    const mode = document.getElementById('chmod-octal').value.trim();
    const recursive = Boolean(document.getElementById('chmod-recursive') && document.getElementById('chmod-recursive').checked);

    try {
        const res = await fetch('/api/files/chmod', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                domain: fileCurrentDomain,
                path: filePath,
                mode: mode,
                recursive: recursive
            })
        });
        if (!res.ok) throw new Error(await res.text());
        showToast(`Permissions updated to ${mode} on '${chmodTargetFile.name}'`, "success");
        closeChmodModal();
        loadFiles();
    } catch (err) {
        showToast("Chmod failed: " + err.message, "error");
    }
}

// Lightbox Image Preview Modal
function openImagePreview(fileName) {
    const relPath = fileCurrentPath ? `${fileCurrentPath}/${fileName}` : fileName;
    const src = `/api/files/read?domain=${encodeURIComponent(fileCurrentDomain)}&path=${encodeURIComponent(relPath)}`;

    const modal = document.getElementById('image-preview-modal');
    const img = document.getElementById('image-preview-img');
    const title = document.getElementById('image-preview-title');
    const meta = document.getElementById('image-preview-meta');
    const downloadBtn = document.getElementById('image-preview-download-btn');

    if (!modal || !img) return;

    title.innerText = `Image Preview: ${fileName}`;
    meta.innerText = `Path: /var/www/${fileCurrentDomain}/${relPath}`;
    img.src = src;
    img.onload = function() {
        meta.innerText = `Path: /var/www/${fileCurrentDomain}/${relPath} · Dimensions: ${this.naturalWidth} x ${this.naturalHeight}px`;
    };

    if (downloadBtn) {
        downloadBtn.href = `${src}&download=true`;
        downloadBtn.download = fileName;
    }

    modal.classList.add('open');
}

function closeImagePreview() {
    const modal = document.getElementById('image-preview-modal');
    if (modal) {
        modal.classList.remove('open');
        const img = document.getElementById('image-preview-img');
        if (img) img.src = '';
    }
}

// Duplicate & Copy Actions
async function duplicateFilePrompt(oldPath, oldName) {
    const ext = oldName.includes('.') ? '.' + oldName.split('.').pop() : '';
    const base = oldName.includes('.') ? oldName.substring(0, oldName.lastIndexOf('.')) : oldName;
    const defaultNewName = `${base}-copy${ext}`;
    const newName = prompt(`Duplicate '${oldName}' as:`, defaultNewName);
    if (!newName) return;

    const parts = oldPath.split('/');
    parts.pop();
    const newPath = parts.length > 0 ? `${parts.join('/')}/${newName}` : newName;

    try {
        const res = await fetch('/api/files/copy', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                domain: fileCurrentDomain,
                oldPath: oldPath,
                newPath: newPath
            })
        });
        if (!res.ok) throw new Error(await res.text());
        showToast(`Duplicated '${oldName}' as '${newName}'`, "success");
        loadFiles();
    } catch (err) {
        showToast("Duplicate failed: " + err.message, "error");
    }
}

async function copyFilePrompt(oldPath, oldName) {
    const dest = prompt(`Copy '${oldName}' to path (relative to webroot):`, oldPath);
    if (!dest || dest === oldPath) return;

    try {
        const res = await fetch('/api/files/copy', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                domain: fileCurrentDomain,
                oldPath: oldPath,
                newPath: dest
            })
        });
        if (!res.ok) throw new Error(await res.text());
        showToast(`Copied to '${dest}'`, "success");
        loadFiles();
    } catch (err) {
        showToast("Copy failed: " + err.message, "error");
    }
}

async function calculateFolderSize(filePath, folderName) {
    showToast(`Calculating size for folder '${folderName}'...`, "info");
    try {
        const res = await fetch(`/api/files/size?domain=${fileCurrentDomain}&path=${encodeURIComponent(filePath)}`);
        if (!res.ok) throw new Error(await res.text());
        const data = await res.json();
        showToast(`📁 ${folderName}: ${formatBytes(data.size)} (${data.filesCount} files)`, "success");
    } catch (err) {
        showToast("Size calculation failed: " + err.message, "error");
    }
}

// Editor Wrap & Font Size Controls
let editorWrapState = false;
let editorFontSize = 14;

function toggleEditorWrap() {
    const textarea = document.getElementById('editor-content');
    const btn = document.getElementById('btn-editor-wrap');
    if (!textarea) return;

    editorWrapState = !editorWrapState;
    if (editorWrapState) {
        textarea.style.whiteSpace = 'pre-wrap';
        textarea.style.wordBreak = 'break-all';
        if (btn) btn.innerText = 'Wrap: On';
    } else {
        textarea.style.whiteSpace = 'pre';
        textarea.style.wordBreak = 'normal';
        if (btn) btn.innerText = 'Wrap: Off';
    }
}

function adjustEditorFontSize(delta) {
    const textarea = document.getElementById('editor-content');
    const gutter = document.getElementById('editor-line-numbers');
    editorFontSize = Math.max(10, Math.min(24, editorFontSize + delta));
    if (textarea) textarea.style.fontSize = `${editorFontSize}px`;
    if (gutter) gutter.style.fontSize = `${editorFontSize}px`;
}

async function downloadFileContent(name) {
    const relPath = fileCurrentPath ? `${fileCurrentPath}/${name}` : name;
    try {
        const res = await fetch(`/api/files/read?domain=${fileCurrentDomain}&path=${relPath}`);
        if (!res.ok) throw new Error(await res.text());
        const blob = await res.blob();
        const url = window.URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = name;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        window.URL.revokeObjectURL(url);
    } catch (err) {
        alert("Download failed: " + err.message);
    }
}

async function renameFilePrompt(oldPath, oldName) {
    const newName = prompt(`Rename '${oldName}' to:`, oldName);
    if (!newName || newName === oldName) return;
    
    const parts = oldPath.split('/');
    parts.pop();
    const newPath = parts.length > 0 ? `${parts.join('/')}/${newName}` : newName;
    
    try {
        const res = await fetch('/api/files/rename', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                domain: fileCurrentDomain,
                oldPath: oldPath,
                newPath: newPath
            })
        });
        if (!res.ok) throw new Error(await res.text());
        loadFiles();
    } catch (err) {
        alert("Rename failed: " + err.message);
    }
}

async function zipFileOrFolder(filePath) {
    try {
        const res = await fetch('/api/files/zip', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                domain: fileCurrentDomain,
                path: filePath
            })
        });
        if (!res.ok) throw new Error(await res.text());
        alert("Compressed successfully!");
        loadFiles();
    } catch (err) {
        alert("Compression failed: " + err.message);
    }
}

async function unzipFile(filePath) {
    try {
        const res = await fetch('/api/files/unzip', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                domain: fileCurrentDomain,
                path: filePath
            })
        });
        if (!res.ok) throw new Error(await res.text());
        alert("Extracted successfully!");
        loadFiles();
    } catch (err) {
        alert("Extraction failed: " + err.message);
    }
}

async function triggerDiskCleanup() {
    try {
        const res = await fetch('/api/status');
        if (!res.ok) throw new Error("Could not retrieve system disk status.");
        const data = await res.json();
        
        if (data.disk && data.disk.pct !== undefined) {
            const pct = data.disk.pct;
            if (pct < 80.0) {
                alert(`Clean up is not required. Disk usage is under 80% (current usage: ${pct.toFixed(1)}%).`);
            } else {
                if (confirm(`Disk usage is currently at ${pct.toFixed(1)}% (exceeding 80% threshold).\n\nProceeding will clear system logs, package cache, temp files, and expired backups older than 3 days.\n\nAre you sure you want to run this cleanup?`)) {
                    triggerAction('server-clean', []);
                }
            }
        } else {
            throw new Error("Disk usage percentage unavailable.");
        }
    } catch (err) {
        alert("Cleanup check failed: " + err.message);
    }
}

// ----------------------------------------------------
// REMOTE S3 CREDENTIALS & STAGING FUNCTIONS
// ----------------------------------------------------
async function loadS3Settings() {
    try {
        const res = await fetch('/api/settings/s3');
        if (!res.ok) throw new Error("Failed to load settings");
        const data = await res.json();
        
        document.getElementById('s3-endpoint').value = data.s3_endpoint || '';
        document.getElementById('s3-region').value = data.s3_region || '';
        document.getElementById('s3-bucket').value = data.s3_bucket || '';
        document.getElementById('s3-access-key').value = data.s3_access_key || '';
        document.getElementById('s3-secret-key').value = data.s3_secret_key || '';
    } catch (err) {
        console.error("loadS3Settings failed:", err);
    }
}

async function saveS3Settings(e) {
    e.preventDefault();
    const statusEl = document.getElementById('s3-settings-status');
    statusEl.innerText = "Saving settings...";
    statusEl.style.color = '#38bdf8';
    
    const payload = {
        s3_endpoint: document.getElementById('s3-endpoint').value.trim(),
        s3_region: document.getElementById('s3-region').value.trim(),
        s3_bucket: document.getElementById('s3-bucket').value.trim(),
        s3_access_key: document.getElementById('s3-access-key').value.trim(),
        s3_secret_key: document.getElementById('s3-secret-key').value.trim(),
    };
    
    try {
        const res = await fetch('/api/settings/s3', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
        });
        if (!res.ok) throw new Error(await res.text());
        
        statusEl.innerText = "Settings saved successfully!";
        statusEl.style.color = '#10b981';
        setTimeout(() => { statusEl.innerText = ""; }, 3000);
    } catch (err) {
        statusEl.innerText = "Failed to save: " + err.message;
        statusEl.style.color = '#ef4444';
    }
}

async function loadTelegramSettings() {
    try {
        const res = await fetch('/api/settings/telegram');
        if (!res.ok) throw new Error("Failed to load settings");
        const data = await res.json();
        
        document.getElementById('telegram-bot-token').value = data.telegram_bot_token || '';
        document.getElementById('telegram-chat-id').value = data.telegram_chat_id || '';
    } catch (err) {
        console.error("loadTelegramSettings failed:", err);
    }
}

async function saveTelegramSettings(e) {
    e.preventDefault();
    const statusEl = document.getElementById('telegram-settings-status');
    statusEl.innerText = "Saving settings...";
    statusEl.style.color = '#38bdf8';
    
    const payload = {
        telegram_bot_token: document.getElementById('telegram-bot-token').value.trim(),
        telegram_chat_id: document.getElementById('telegram-chat-id').value.trim(),
        is_test: false
    };
    
    try {
        const res = await fetch('/api/settings/telegram', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
        });
        if (!res.ok) throw new Error(await res.text());
        
        statusEl.innerText = "Settings saved successfully!";
        statusEl.style.color = '#10b981';
        setTimeout(() => { statusEl.innerText = ""; }, 3000);
    } catch (err) {
        statusEl.innerText = "Failed to save: " + err.message;
        statusEl.style.color = '#ef4444';
    }
}

async function testTelegramNotification() {
    const statusEl = document.getElementById('telegram-settings-status');
    statusEl.innerText = "Sending test message...";
    statusEl.style.color = '#38bdf8';
    
    const payload = {
        telegram_bot_token: document.getElementById('telegram-bot-token').value.trim(),
        telegram_chat_id: document.getElementById('telegram-chat-id').value.trim(),
        is_test: true
    };
    
    try {
        const res = await fetch('/api/settings/telegram', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
        });
        if (!res.ok) throw new Error(await res.text());
        
        statusEl.innerText = "Test message sent! Check Telegram.";
        statusEl.style.color = '#10b981';
        setTimeout(() => { statusEl.innerText = ""; }, 3000);
    } catch (err) {
        statusEl.innerText = "Test failed: " + err.message;
        statusEl.style.color = '#ef4444';
    }
}

async function loadS3BackupList(domain) {
    const container = document.getElementById('manage-s3-backups-container');
    container.innerHTML = '<div class="loading-placeholder" style="padding: 0.5rem 0;">Checking S3 backup history...</div>';
    
    try {
        const res = await fetch(`/api/sites/s3-backups?domain=${domain}`);
        if (!res.ok) throw new Error(await res.text());
        const list = await res.json();
        
        container.innerHTML = '';
        if (!list || list.length === 0) {
            container.innerHTML = '<div style="color: var(--text-muted); padding: 0.5rem 0;">No cloud backup versions available.</div>';
            return;
        }
        
        list.forEach(ts => {
            const div = document.createElement('div');
            div.style.display = 'flex';
            div.style.justifyContent = 'space-between';
            div.style.alignItems = 'center';
            div.style.padding = '0.3rem 0.5rem';
            div.style.background = 'rgba(255,255,255,0.02)';
            div.style.border = '1px solid var(--glass-border)';
            div.style.borderRadius = '4px';
            div.style.marginBottom = '0.3rem';
            
            let formattedTime = ts;
            if (ts.length >= 13 && ts.includes('-')) {
                const y = ts.substring(0, 4);
                const m = ts.substring(4, 6);
                const d = ts.substring(6, 8);
                const h = ts.substring(9, 11);
                const min = ts.substring(11, 13);
                const sec = ts.length >= 15 ? ts.substring(13, 15) : '00';
                formattedTime = `${y}-${m}-${d} ${h}:${min}:${sec}`;
            }
            
            div.innerHTML = `
                <span>📅 ${formattedTime}</span>
                <div style="display: flex; gap: 0.5rem;">
                    <button class="btn btn-primary btn-sm" style="padding: 0.25rem 0.5rem; font-size: 0.7rem;" onclick="event.stopPropagation(); openRestoreWizard('${domain}', 's3', '${ts}')">Restore</button>
                    <button class="btn btn-danger btn-sm" style="padding: 0.25rem 0.5rem; font-size: 0.7rem; background: #ef4444;" onclick="event.stopPropagation(); deleteS3Backup('${domain}', '${ts}')">Delete</button>
                </div>
            `;
            container.appendChild(div);
        });
    } catch (err) {
        // Show both a user-friendly message and the actual error for debugging
        container.innerHTML = `<div style="color: var(--text-muted); font-size: 0.8rem; padding: 0.5rem 0;">☁️ Could not load S3 backup history: ${err.message}</div>`;
    }
}

async function deleteS3Backup(domain, timestamp) {
    if (!confirm("Are you sure you want to permanently delete this cloud backup version from S3 storage?")) return;
    try {
        const res = await fetch(`/api/sites/s3-delete?domain=${domain}&timestamp=${timestamp}`, {
            method: 'POST'
        });
        if (!res.ok) throw new Error(await res.text());
        alert("S3 backup version deleted successfully!");
        loadS3BackupList(domain);
    } catch (err) {
        alert("Failed to delete S3 backup: " + err.message);
    }
}

async function toggleStagingUnlock(unlocked) {
    if (!currentManageDomain) return;
    try {
        const res = await fetch('/api/sites/toggle-staging-unlock', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ domain: currentManageDomain, unlocked })
        });
        if (!res.ok) throw new Error(await res.text());
        loadSites();
    } catch (err) {
        alert("Failed to toggle staging unlock: " + err.message);
    }
}

async function toggleS3Enabled(enabled) {
    if (!currentManageDomain) return;
    try {
        const res = await fetch('/api/sites/toggle-s3-enabled', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ domain: currentManageDomain, enabled })
        });
        if (!res.ok) throw new Error(await res.text());
        toggleS3AccessUI(enabled);
        loadSites();
    } catch (err) {
        alert("Failed to toggle S3 backup access: " + err.message);
    }
}

async function updateBackupInterval(interval) {
    if (!currentManageDomain) return;
    try {
        const res = await fetch('/api/sites/update-backup-interval', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ domain: currentManageDomain, interval })
        });
        if (!res.ok) throw new Error(await res.text());
        loadSites();
    } catch (err) {
        alert("Failed to update backup interval: " + err.message);
    }
}

async function updateBackupDestination(destination) {
    if (!currentManageDomain) return;
    try {
        const res = await fetch('/api/sites/update-backup-destination', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ domain: currentManageDomain, destination })
        });
        if (!res.ok) throw new Error(await res.text());

        // Show or hide S3-specific controls based on destination
        const versionsRow = document.getElementById('manage-s3-versions-row');
        const backupsHistoryContainer = document.getElementById('manage-s3-backups-container');
        const backupsHistoryCard = backupsHistoryContainer ? backupsHistoryContainer.closest('div') : null;
        const isS3 = destination === 's3';
        if (versionsRow) versionsRow.style.display = isS3 ? 'flex' : 'none';
        if (backupsHistoryCard) backupsHistoryCard.style.display = isS3 ? 'block' : 'none';

        loadSites();
    } catch (err) {
        alert("Failed to update backup destination: " + err.message);
    }
}

async function updateS3BackupVersions(versions) {
    if (!currentManageDomain) return;
    try {
        const res = await fetch('/api/sites/update-s3-backup-versions', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ domain: currentManageDomain, versions: parseInt(versions) })
        });
        if (!res.ok) throw new Error(await res.text());
        loadSites();
    } catch (err) {
        alert("Failed to update S3 backup versions: " + err.message);
    }
}

let s3RestoreDomain = "";
let wizardDomain = "";
let wizardSource = "local";
let wizardSelectedTimestamp = "";
let wizardEventSource = null;

function openRestoreWizard(domain, initialSource, initialTimestamp) {
    wizardDomain = domain;
    wizardSource = initialSource || 'local';
    wizardSelectedTimestamp = initialTimestamp || '';

    // Show step 1, hide others
    document.getElementById('wizard-step-1').style.display = 'block';
    document.getElementById('wizard-step-2').style.display = 'none';
    document.getElementById('wizard-step-3').style.display = 'none';

    // Update tab UI
    setWizardSource(wizardSource);

    closeManageModal();
    document.getElementById('restore-wizard-modal').classList.add('open');
}

function closeRestoreWizard() {
    if (wizardEventSource) {
        wizardEventSource.close();
        wizardEventSource = null;
    }
    document.getElementById('restore-wizard-modal').classList.remove('open');
}

function setWizardSource(source) {
    wizardSource = source;
    wizardSelectedTimestamp = "";
    
    // Toggle active tab buttons
    const localTab = document.getElementById('wizard-tab-local');
    const s3Tab = document.getElementById('wizard-tab-s3');
    
    if (source === 'local') {
        localTab.style.background = 'rgba(255,255,255,0.05)';
        localTab.style.color = '#fff';
        s3Tab.style.background = 'transparent';
        s3Tab.style.color = 'var(--text-muted)';
    } else {
        s3Tab.style.background = 'rgba(255,255,255,0.05)';
        s3Tab.style.color = '#fff';
        localTab.style.background = 'transparent';
        localTab.style.color = 'var(--text-muted)';
    }

    document.getElementById('btn-wizard-next').disabled = true;
    loadWizardVersions(wizardDomain, source);
}

async function loadWizardVersions(domain, source) {
    const container = document.getElementById('wizard-versions-container');
    container.innerHTML = '<div style="color: var(--text-muted); font-size: 0.8rem; padding: 0.5rem 0;">Loading available versions...</div>';
    
    const apiPath = (source === 'local') ? `/api/sites/local-backups?domain=${domain}` : `/api/sites/s3-backups?domain=${domain}`;
    
    try {
        const res = await fetch(apiPath);
        if (!res.ok) throw new Error(await res.text());
        const list = await res.json();
        
        container.innerHTML = '';
        if (!list || list.length === 0) {
            container.innerHTML = `<div style="color: var(--text-muted); font-size: 0.8rem; padding: 0.5rem 0;">No ${source === 'local' ? 'local' : 'cloud'} versions available.</div>`;
            return;
        }
        
        list.forEach(ts => {
            const div = document.createElement('div');
            div.className = 'wizard-version-item';
            div.style.padding = '0.4rem 0.6rem';
            div.style.background = 'rgba(255,255,255,0.02)';
            div.style.border = '1px solid var(--glass-border)';
            div.style.borderRadius = '4px';
            div.style.marginBottom = '0.3rem';
            div.style.cursor = 'pointer';
            div.style.display = 'flex';
            div.style.justifyContent = 'space-between';
            div.style.alignItems = 'center';
            div.style.fontSize = '0.8rem';
            
            let formattedTime = ts;
            if (ts.length >= 13 && ts.includes('-')) {
                const y = ts.substring(0, 4);
                const m = ts.substring(4, 6);
                const d = ts.substring(6, 8);
                const h = ts.substring(9, 11);
                const min = ts.substring(11, 13);
                const sec = ts.length >= 15 ? ts.substring(13, 15) : '00';
                formattedTime = `${y}-${m}-${d} ${h}:${min}:${sec}`;
            }
            
            div.innerHTML = `<span>📅 ${formattedTime}</span>`;
            
            div.onclick = () => {
                // Clear active states
                document.querySelectorAll('.wizard-version-item').forEach(el => {
                    el.style.background = 'rgba(255,255,255,0.02)';
                    el.style.borderColor = 'var(--glass-border)';
                });
                div.style.background = 'rgba(56, 189, 248, 0.1)';
                div.style.borderColor = '#38bdf8';
                
                wizardSelectedTimestamp = ts;
                document.getElementById('btn-wizard-next').disabled = false;
            };
            
            container.appendChild(div);
            
            // Auto-select if requested
            if (wizardSelectedTimestamp === ts) {
                div.click();
            }
        });
    } catch (err) {
        container.innerHTML = `<div style="color: #ef4444; font-size: 0.8rem; padding: 0.5rem 0;">Error listing backups: ${err.message}</div>`;
    }
}

function wizardNextStep() {
    document.getElementById('wizard-step-1').style.display = 'none';
    document.getElementById('wizard-step-2').style.display = 'block';

    document.getElementById('wizard-domain-label').innerText = wizardDomain;
    document.getElementById('wizard-summary-source').innerText = (wizardSource === 'local') ? '📁 Local Storage' : '☁️ S3 Cloud Storage';
    
    let formattedTime = wizardSelectedTimestamp;
    if (wizardSelectedTimestamp.length >= 13 && wizardSelectedTimestamp.includes('-')) {
        const y = wizardSelectedTimestamp.substring(0, 4);
        const m = wizardSelectedTimestamp.substring(4, 6);
        const d = wizardSelectedTimestamp.substring(6, 8);
        const h = wizardSelectedTimestamp.substring(9, 11);
        const min = wizardSelectedTimestamp.substring(11, 13);
        const sec = wizardSelectedTimestamp.length >= 15 ? wizardSelectedTimestamp.substring(13, 15) : '00';
        formattedTime = `${y}-${m}-${d} ${h}:${min}:${sec}`;
    }
    document.getElementById('wizard-summary-version').innerText = formattedTime;

    // Reset inputs
    document.getElementById('wizard-confirm-domain').value = "";
    document.getElementById('wizard-confirm-checkbox').checked = false;
    
    validateWizardConfirmation();
}

function wizardPrevStep() {
    document.getElementById('wizard-step-2').style.display = 'none';
    document.getElementById('wizard-step-1').style.display = 'block';
}

function validateWizardConfirmation() {
    const typed = document.getElementById('wizard-confirm-domain').value.trim();
    const isChecked = document.getElementById('wizard-confirm-checkbox').checked;
    const btn = document.getElementById('btn-wizard-submit');
    
    const isMatch = typed.toLowerCase() === wizardDomain.toLowerCase();
    const isValid = isMatch && isChecked;
    
    btn.disabled = !isValid;
    btn.style.opacity = isValid ? '1' : '0.5';
}

function submitRestoreWizard() {
    document.getElementById('wizard-step-2').style.display = 'none';
    document.getElementById('wizard-step-3').style.display = 'block';
    
    triggerRestoreStream(wizardDomain, wizardSource, wizardSelectedTimestamp);
}

async function triggerRestoreStream(domain, source, timestamp) {
    const output = document.getElementById('wizard-log-output');
    const statusText = document.getElementById('wizard-status-text');
    const finishBtn = document.getElementById('btn-wizard-finish');

    output.innerHTML = "";
    statusText.innerText = "Connecting to restore stream...";
    finishBtn.disabled = true;

    const apiPath = (source === 'local') ? `/api/action` : `/api/sites/s3-restore?domain=${domain}&timestamp=${timestamp}`;
    
    try {
        let response;
        if (source === 'local') {
            response = await fetch(apiPath, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ action: 'site-restore', args: [domain, timestamp] })
            });
        } else {
            response = await fetch(apiPath);
        }

        if (!response.ok) {
            const errText = await response.text();
            throw new Error(errText || "Restore process failed to start");
        }

        const reader = response.body.getReader();
        const decoder = new TextDecoder("utf-8");
        statusText.innerText = "Restoring website - streaming output...";

        let done = false;
        let buffer = "";

        while (!done) {
            const { value, done: streamDone } = await reader.read();
            done = streamDone;
            
            if (value) {
                buffer += decoder.decode(value, { stream: !done });
                const lines = buffer.split("\n\n");
                buffer = lines.pop();

                lines.forEach(line => {
                    if (line.startsWith("data: ")) {
                        const content = line.substring(6);
                        const span = document.createElement('div');
                        if (content.startsWith("ERR: ")) {
                            span.style.color = '#f87171';
                            span.innerText = content.substring(5);
                        } else if (content.startsWith("Step") || content.startsWith("Running")) {
                            span.style.color = '#38bdf8';
                            span.innerText = `> ${content}`;
                        } else {
                            span.innerText = content;
                        }
                        output.appendChild(span);
                        output.scrollTop = output.scrollHeight;
                    }
                });
            }
        }

        statusText.innerText = "Restore process finished.";
        statusText.style.color = '#10b981';

    } catch (err) {
        const span = document.createElement('div');
        span.style.color = '#f87171';
        span.innerText = `ERROR: Restore pipeline failed: ${err.message}`;
        output.appendChild(span);
        statusText.innerText = "Restore pipeline failed.";
        statusText.style.color = '#ef4444';
    } finally {
        finishBtn.disabled = false;
        loadStatus();
        loadSites();
    }
}

// ----------------------------------------------------
// FILE MANAGER & EDITOR ENHANCEMENTS
// ----------------------------------------------------
function filterFiles() {
    fileCurrentPage = 1;
    renderFilesTable();
}

let lastSearchQuery = "";
let searchIndices = [];
let currentSearchMatchIndex = -1;

function toggleEditorSearch(show) {
    const bar = document.getElementById('editor-search-replace');
    if (!bar) return;
    
    if (show) {
        bar.classList.remove('hidden');
        const input = document.getElementById('editor-search-input');
        input.focus();
        input.select();
    } else {
        bar.classList.add('hidden');
        document.getElementById('editor-content').focus();
    }
}

function onEditorFind() {
    const query = document.getElementById('editor-search-input').value;
    const textarea = document.getElementById('editor-content');
    const text = textarea.value;
    
    if (!query) {
        searchIndices = [];
        currentSearchMatchIndex = -1;
        return;
    }
    
    searchIndices = [];
    let idx = text.indexOf(query);
    while (idx !== -1) {
        searchIndices.push(idx);
        idx = text.indexOf(query, idx + query.length);
    }
    
    if (searchIndices.length > 0) {
        currentSearchMatchIndex = 0;
        highlightCurrentMatch();
    } else {
        currentSearchMatchIndex = -1;
    }
}

function highlightCurrentMatch() {
    if (currentSearchMatchIndex < 0 || currentSearchMatchIndex >= searchIndices.length) return;
    const query = document.getElementById('editor-search-input').value;
    const textarea = document.getElementById('editor-content');
    const startIdx = searchIndices[currentSearchMatchIndex];
    
    textarea.focus();
    textarea.setSelectionRange(startIdx, startIdx + query.length);
    
    const lineCountBefore = textarea.value.substring(0, startIdx).split('\n').length;
    const lineHeight = 21.6;
    textarea.scrollTop = (lineCountBefore - 5) * lineHeight;
    syncEditorScroll();
}

function editorFindNext() {
    if (searchIndices.length === 0) {
        onEditorFind();
        return;
    }
    currentSearchMatchIndex = (currentSearchMatchIndex + 1) % searchIndices.length;
    highlightCurrentMatch();
}

function editorReplace() {
    const query = document.getElementById('editor-search-input').value;
    const replacement = document.getElementById('editor-replace-input').value;
    const textarea = document.getElementById('editor-content');
    
    if (!query || currentSearchMatchIndex < 0) return;
    
    const start = textarea.selectionStart;
    const end = textarea.selectionEnd;
    
    const selectedText = textarea.value.substring(start, end);
    if (selectedText === query) {
        textarea.value = textarea.value.substring(0, start) + replacement + textarea.value.substring(end);
        textarea.setSelectionRange(start, start + replacement.length);
        updateLineNumbers();
        onEditorFind();
    }
}

function editorReplaceAll() {
    const query = document.getElementById('editor-search-input').value;
    const replacement = document.getElementById('editor-replace-input').value;
    const textarea = document.getElementById('editor-content');
    
    if (!query) return;
    
    const text = textarea.value;
    const newText = text.split(query).join(replacement);
    textarea.value = newText;
    updateLineNumbers();
    
    onEditorFind();
}

// Sidebar Drawer Control Functions
function toggleSidebar() {
    const sidebar = document.querySelector('.sidebar');
    if (sidebar) {
        sidebar.classList.toggle('open');
    }
}

// Hoisted fallback definition to avoid errors
function closeSidebar() {
    const sidebar = document.querySelector('.sidebar');
    if (sidebar) {
        sidebar.classList.remove('open');
    }
}

// Global click handler to close the navigation drawer when clicking outside
window.addEventListener('click', function(e) {
    const sidebar = document.querySelector('.sidebar');
    const toggleBtn = document.getElementById('sidebar-toggle-btn');
    if (sidebar && sidebar.classList.contains('open')) {
        if (!sidebar.contains(e.target) && (!toggleBtn || !toggleBtn.contains(e.target))) {
            sidebar.classList.remove('open');
        }
    }
});

async function openSiteCaddyConfig(domain) {
    fileCurrentDomain = domain;
    fileCurrentPath = "conf";
    await openFileEditor("caddy.conf");
}

async function saveDbCredentials() {
    const domain = currentManageDomain;
    const dbUser = document.getElementById('manage-db-user').value.trim();
    const dbPass = document.getElementById('manage-db-pass').value.trim();
    
    try {
        const res = await fetch('/api/sites/update-db-credentials', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                domain: domain,
                db_user: dbUser,
                db_pass: dbPass
            })
        });
        if (!res.ok) throw new Error(await res.text());
        
        showToast("Database credentials saved successfully", "success");
        closeManageModal();
        loadSites();
    } catch (err) {
        showToast("Failed to save database credentials: " + err.message, "error");
    }
}
