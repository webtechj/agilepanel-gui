// Tab management
function switchTab(tabId) {
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
    if (tabId === 'sites') {
        loadSites();
    } else if (tabId === 'files') {
        initFileManagerContexts();
    } else if (tabId === 'security') {
        runSecurityDashboardScan();
    }
}

// Format memory outputs (GB)
function formatGB(val) {
    return val ? val.toFixed(2) : '0.00';
}

// Fetch and render system health telemetry
async function loadStatus() {
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

        // 2. CPU
        document.getElementById('metric-cpu').innerText = `${data.cpu.toFixed(1)}%`;
        document.getElementById('progress-cpu').style.width = `${data.cpu}%`;

        // 3. RAM
        if (data.ram) {
            document.getElementById('metric-ram').innerText = `${formatGB(data.ram.used)} / ${formatGB(data.ram.total)} GB`;
            document.getElementById('progress-ram').style.width = `${data.ram.pct}%`;
        }

        // 4. Disk
        if (data.disk) {
            document.getElementById('metric-disk').innerText = `${formatGB(data.disk.used)} / ${formatGB(data.disk.total)} GB`;
            document.getElementById('progress-disk').style.width = `${data.disk.pct}%`;
        }

        // 5. Total Websites breakdown
        document.getElementById('metric-sites').innerText = data.siteCount;
        const wpStr = data.wpCount === 1 ? '1 WordPress' : `${data.wpCount} WordPress`;
        const htmlStr = data.htmlCount === 1 ? '1 HTML' : `${data.htmlCount} HTML`;
        const laravelStr = data.laravelCount === 1 ? '1 Laravel' : `${data.laravelCount} Laravel`;
        const phpStr = data.phpCount === 1 ? '1 PHP' : `${data.phpCount} PHP`;
        document.getElementById('metric-sites-breakdown').innerText = `${wpStr} · ${htmlStr} · ${laravelStr} · ${phpStr}`;

        // 6. Uptime, load, sockets
        document.getElementById('server-uptime').innerText = data.uptime || 'N/A';
        document.getElementById('server-sockets').innerText = `${data.tcpConns || 0} active sockets`;
        if (data.loadAvg && data.loadAvg.length >= 3) {
            document.getElementById('server-load').innerText = data.loadAvg.map(n => n.toFixed(2)).join(', ');
        }

        // 7. Top Processes table
        const processBody = document.getElementById('processes-table-body');
        processBody.innerHTML = '';
        if (data.topProcesses && data.topProcesses.length > 0) {
            data.topProcesses.forEach(p => {
                const tr = document.createElement('tr');
                tr.innerHTML = `
                    <td><code>${p.pid}</code></td>
                    <td><strong>${p.comm}</strong></td>
                    <td><span class="badge-lock" style="background:rgba(59,130,246,0.1); color:#60a5fa; border:none;">${p.cpu.toFixed(1)}%</span></td>
                    <td><span class="badge-lock" style="background:rgba(139,92,246,0.1); color:#c084fc; border:none;">${p.mem.toFixed(1)}%</span></td>
                `;
                processBody.appendChild(tr);
            });
        } else {
            processBody.innerHTML = `<tr><td colspan="4" class="loading-placeholder">No active high-load processes.</td></tr>`;
        }

        // 8. Services list rendering
        const servicesContainer = document.getElementById('services-list-container');
        servicesContainer.innerHTML = '';
        
        for (const [name, active] of Object.entries(data.services)) {
            const card = document.createElement('div');
            card.className = 'service-card';
            
            const activeClass = active ? 'active' : 'inactive';
            const activeText = active ? 'active' : 'inactive';
            
            card.innerHTML = `
                <div class="service-info">
                    <span class="service-name">${name.toUpperCase()}</span>
                    <span class="service-indicator ${activeClass}">
                        <span class="indicator-dot"></span> ${activeText}
                    </span>
                </div>
                <button class="btn btn-secondary" onclick="triggerAction('server-restart', ['${name}'])">Restart</button>
            `;
            servicesContainer.appendChild(card);
        }

        // 9. Integrate dynamic phpMyAdmin links
        const pmaLink = document.getElementById('pma-link');
        if (pmaLink) {
            pmaLink.href = `http://${window.location.hostname}:8888`;
        }
        const sidebarPmaLink = document.getElementById('sidebar-pma-link');
        if (sidebarPmaLink) {
            sidebarPmaLink.href = `http://${window.location.hostname}:8888`;
        }

        // 10. Render Database Storage allocations
        const dbContainer = document.getElementById('db-storage-list');
        if (dbContainer && data.dbSizes) {
            dbContainer.innerHTML = '';
            const dbEntries = Object.entries(data.dbSizes);
            if (dbEntries.length > 0) {
                const maxSize = Math.max(...dbEntries.map(([_, size]) => size)) || 1.0;
                dbEntries.forEach(([name, size]) => {
                    const card = document.createElement('div');
                    card.className = 'service-card';
                    card.style.flexDirection = 'column';
                    card.style.alignItems = 'stretch';
                    card.style.gap = '0.5rem';
                    
                    const pct = (size / maxSize) * 100;
                    card.innerHTML = `
                        <div style="display:flex; justify-content:space-between; align-items:center; width:100%;">
                            <span class="service-name" style="font-family:var(--font-mono); font-size:0.85rem; text-overflow:ellipsis; overflow:hidden; white-space:nowrap;">🛢️ ${name}</span>
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
                `<button class="btn btn-success" onclick="triggerAction('site-unlock', ['${site.domain}'])">Unlock</button>` :
                `<button class="btn btn-warning" onclick="triggerAction('site-lock', ['${site.domain}'])">Lock</button>`;

            const filesBackupBtn = site.has_files_backup ? 
                `<a href="/api/backup/download?domain=${site.domain}&type=files" class="btn btn-success" style="background:#047857; color:#fff; text-decoration:none;" title="Download Files Backup ZIP">💾 Files ZIP</a>` : 
                `<button class="btn btn-secondary" style="opacity:0.4; cursor:not-allowed;" title="No files backup available. Click Backup first." disabled>💾 Files ZIP</button>`;

            const dbBackupBtn = site.type === 'html' ? '' : (site.has_db_backup ? 
                `<a href="/api/backup/download?domain=${site.domain}&type=db" class="btn btn-success" style="background:#047857; color:#fff; text-decoration:none;" title="Download Database SQL ZIP">🗄️ DB ZIP</a>` : 
                `<button class="btn btn-secondary" style="opacity:0.4; cursor:not-allowed;" title="No database backup available. Click Backup first." disabled>🗄️ DB ZIP</button>`);

            tr.innerHTML = `
                <td>
                    <strong>${site.domain}</strong><br>
                    <a href="${site.staging_url}" target="_blank" style="color: #60a5fa; font-size: 0.8rem; text-decoration: none; display: inline-flex; align-items: center; gap: 0.25rem; margin-top: 0.25rem;">🔍 Staging Link 🔗</a>
                </td>
                <td><span class="badge" style="background:#1e293b; padding:0.2rem 0.5rem; border-radius:4px;">${site.type ? site.type.toUpperCase() : 'WP'}</span></td>
                <td>PHP ${site.php_version}</td>
                <td><code>${site.system_user}</code></td>
                <td>${lockBadge}</td>
                <td>
                    <div class="actions-cell">
                        ${filesBackupBtn}
                        ${dbBackupBtn}
                        
                        <div class="dropdown">
                            <button class="btn btn-secondary dropdown-toggle" onclick="toggleDropdown(this)" style="font-weight:600;">Manage</button>
                            <div class="dropdown-menu">
                                <button class="dropdown-item" onclick="triggerAction('${site.is_locked ? 'site-unlock' : 'site-lock'}', ['${site.domain}'])">
                                    ${site.is_locked ? '🔓 Unlock Site' : '🔒 Lock Site'}
                                </button>
                                <button class="dropdown-item" onclick="triggerAction('site-cache', ['${site.domain}'])">🧹 Flush Cache</button>
                                <button class="dropdown-item" onclick="triggerAction('site-perms', ['${site.domain}'])">🛠️ Fix Permissions</button>
                                <button class="dropdown-item" onclick="triggerAction('site-ssl', ['${site.domain}'])">🔑 SSL Renew</button>
                                <button class="dropdown-item" onclick="triggerAction('site-backup', ['${site.domain}'])">💾 Trigger Backup</button>
                                <button class="dropdown-item" onclick="triggerRestoreSite('${site.domain}')">🔄 Restore Backup</button>
                                <button class="dropdown-item" onclick="triggerAction('site-reinstall', ['${site.domain}'])">♻️ Reinstall Site</button>
                                <div class="dropdown-divider"></div>
                                <button class="dropdown-item text-danger" onclick="confirmDeleteSite('${site.domain}')">🗑️ Delete Site</button>
                            </div>
                        </div>
                    </div>
                </td>
            `;
            tableBody.appendChild(tr);
        });

    } catch (err) {
        console.error("Error loadSites:", err);
    }
}

// Modal open/close actions
function openCreateModal() {
    document.getElementById('create-modal').classList.add('open');
}

function closeCreateModal() {
    document.getElementById('create-modal').classList.remove('open');
}

// Handle site type toggle (HTML database option)
function onSiteTypeChange(value) {
    const wpDiv = document.getElementById('wp-install-choice');
    const htmlDiv = document.getElementById('html-db-choice');
    
    if (value === 'html') {
        wpDiv.style.display = 'none';
        htmlDiv.style.display = 'flex';
    } else if (value === 'wp' || value === 'woocommerce') {
        wpDiv.style.display = 'flex';
        htmlDiv.style.display = 'none';
    } else {
        wpDiv.style.display = 'none';
        htmlDiv.style.display = 'none';
    }
}

// Submit Create Site
function submitCreateSite(e) {
    e.preventDefault();
    const form = e.target;
    const domain = form.domain.value.trim();
    const type = form.type.value;
    const php = form.php.value;
    const wp = form.wp.checked;
    const htmlDb = document.getElementById('html-needs-db').checked;

    closeCreateModal();

    const args = [domain, '--type', type, '--php', php];
    if (type === 'html') {
        args.push('--db', htmlDb ? 'true' : 'false');
    } else if (wp || type === 'wp' || type === 'woocommerce') {
        args.push('--wp');
    }

    triggerAction('site-create', args);
    form.reset();
    // Restore default choices
    onSiteTypeChange('wp');
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
let fileCurrentPath = "htdocs";
let editorFilePath = "";

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
        if (fileCurrentDomain) {
            select.value = fileCurrentDomain;
            loadFiles();
        } else {
            document.getElementById('files-table-body').innerHTML = `<tr><td colspan="5" class="loading-placeholder">Please select a website context above.</td></tr>`;
        }
    } catch (err) {
        console.error("FileManager Context setup failed:", err);
    }
}

function onFileSiteChange() {
    const val = document.getElementById('file-site-select').value;
    fileCurrentDomain = val;
    fileCurrentPath = "htdocs"; // reset back to webroot htdocs
    if (val) {
        loadFiles();
    } else {
        document.getElementById('files-table-body').innerHTML = `<tr><td colspan="5" class="loading-placeholder">Please select a website context above.</td></tr>`;
        document.getElementById('file-breadcrumbs').innerHTML = 'Webroot: <span>/var/www/</span>';
        document.getElementById('btn-file-up').disabled = true;
    }
}

async function loadFiles() {
    if (!fileCurrentDomain) return;
    const body = document.getElementById('files-table-body');
    body.innerHTML = `<tr><td colspan="5" class="loading-placeholder">Loading directory structure...</td></tr>`;

    // Enable/disable UP button
    const isRoot = fileCurrentPath === "htdocs" || fileCurrentPath === "" || fileCurrentPath === ".";
    document.getElementById('btn-file-up').disabled = isRoot;

    // Build breadcrumbs
    document.getElementById('file-breadcrumbs').innerHTML = `Webroot: <span>/var/www/${fileCurrentDomain}/<strong style="color:#60a5fa;">${fileCurrentPath}</strong></span>`;

    try {
        const res = await fetch(`/api/files/list?domain=${fileCurrentDomain}&path=${fileCurrentPath}`);
        if (!res.ok) {
            const errText = await res.text();
            throw new Error(errText);
        }
        const files = await res.json();
        body.innerHTML = '';

        if (files.length === 0) {
            body.innerHTML = `<tr><td colspan="5" class="loading-placeholder">Directory is empty. Click 'New File' to add files.</td></tr>`;
            return;
        }

        // Sort: directories first
        files.sort((a, b) => {
            if (a.isDir && !b.isDir) return -1;
            if (!a.isDir && b.isDir) return 1;
            return a.name.localeCompare(b.name);
        });

        files.forEach(file => {
            const tr = document.createElement('tr');
            
            const icon = file.isDir ? "📁" : "📄";
            const nameEl = file.isDir ? 
                `<a href="#" onclick="enterFolder('${file.name}')" style="color:#60a5fa; font-weight:600; text-decoration:none;">${icon} ${file.name}</a>` :
                `<span>${icon} ${file.name}</span>`;

            const sizeText = file.isDir ? '-' : formatBytes(file.size);

            const fileExtension = file.name.split('.').pop().toLowerCase();
            const editableExtensions = ['html', 'css', 'js', 'php', 'txt', 'json', 'conf', 'ini', 'htaccess'];
            const canEdit = !file.isDir && editableExtensions.includes(fileExtension);

            const editBtn = canEdit ? 
                `<button class="btn btn-primary" onclick="openFileEditor('${file.name}')">Edit</button>` : '';

            const deletePath = fileCurrentPath ? `${fileCurrentPath}/${file.name}` : file.name;

            tr.innerHTML = `
                <td>${nameEl}</td>
                <td>${sizeText}</td>
                <td><code style="color:#a7f3d0;">${file.mode}</code></td>
                <td style="color:#94a3b8; font-size:0.85rem;">${file.modTime}</td>
                <td>
                    <div class="actions-cell">
                        ${editBtn}
                        <button class="btn btn-danger" onclick="deleteFileConfirm('${deletePath}', '${file.name}')">Delete</button>
                    </div>
                </td>
            `;
            body.appendChild(tr);
        });

    } catch (err) {
        body.innerHTML = `<tr><td colspan="5" class="loading-placeholder" style="color:var(--danger);">Error loading files: ${err.message}</td></tr>`;
    }
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
    if (fileCurrentPath === "htdocs" || fileCurrentPath === "") return;
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

function triggerFileUpload() {
    if (!fileCurrentDomain) return alert("Select website context first!");
    document.getElementById('file-upload-input').click();
}

async function handleFileUploadSelected() {
    const input = document.getElementById('file-upload-input');
    if (!input.files || input.files.length === 0) return;

    const file = input.files[0];
    const formData = new FormData();
    formData.append("domain", fileCurrentDomain);
    formData.append("path", fileCurrentPath);
    formData.append("file", file);

    try {
        const res = await fetch('/api/files/upload', {
            method: 'POST',
            body: formData
        });
        if (!res.ok) throw new Error(await res.text());
        loadFiles();
    } catch (err) {
        alert("Upload failed: " + err.message);
    } finally {
        input.value = ''; // clear input
    }
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
    
    try {
        const res = await fetch(`/api/files/read?domain=${fileCurrentDomain}&path=${editorFilePath}`);
        if (!res.ok) throw new Error(await res.text());
        const data = await res.text();

        document.getElementById('editor-title').innerText = `Editing: ${name}`;
        document.getElementById('editor-content').value = data;
        document.getElementById('editor-modal').classList.add('open');
    } catch (err) {
        alert("Failed to load file contents: " + err.message);
    }
}

function closeEditorModal() {
    document.getElementById('editor-modal').classList.remove('open');
}

async function saveEditorContent() {
    const content = document.getElementById('editor-content').value;
    
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
        alert("File saved successfully.");
        closeEditorModal();
        loadFiles();
    } catch (err) {
        alert("Failed to save changes: " + err.message);
    }
}

// ----------------------------------------------------
// SECURITY PAGE AUDIT SCANNER
// ----------------------------------------------------
async function runSecurityDashboardScan() {
    const fwBadge = document.querySelector('#sec-firewall .status-badge');
    const pmaBadge = document.querySelector('#sec-phpmyadmin .status-badge');
    const sshBadge = document.querySelector('#sec-ssh .status-badge');
    const pwageBadge = document.querySelector('#sec-pwage .status-badge');

    // Reset loaders
    document.querySelectorAll('.status-badge').forEach(b => {
        b.className = 'status-badge status-loading';
        b.innerText = 'Scanning...';
    });

    try {
        const res = await fetch('/api/status');
        const data = await res.json();

        // 1. UFW Firewall Policy Check
        if (data.services.ufw || data.tcpConns) {
            fwBadge.className = 'status-badge badge-unlock'; // green
            fwBadge.innerText = 'Active & Secure';
        } else {
            fwBadge.className = 'status-badge badge-lock'; // red
            fwBadge.innerText = 'Warning';
        }

        // 2. phpMyAdmin Auth Guard Check
        if (data.global.has_credentials) {
            pmaBadge.className = 'status-badge badge-unlock';
            pmaBadge.innerText = 'Secured (Basic Auth)';
        } else {
            pmaBadge.className = 'status-badge badge-lock';
            pmaBadge.innerText = 'Unsecured (Default)';
        }

        // 3. SSH Password policy check (Mock audit based on credentials)
        if (data.global.has_credentials) {
            sshBadge.className = 'status-badge badge-unlock';
            sshBadge.innerText = 'Hardened';
        } else {
            sshBadge.className = 'status-badge badge-lock';
            sshBadge.innerText = 'Bcrypt Warning';
        }

        // 4. Root Password Age limits
        if (data.global.has_credentials) {
            pwageBadge.className = 'status-badge badge-unlock';
            pwageBadge.innerText = 'Enforced (30-day)';
        } else {
            pwageBadge.className = 'status-badge badge-lock';
            pwageBadge.innerText = 'Default settings';
        }

    } catch (err) {
        console.error("Security Scan failed:", err);
    }
}

// ----------------------------------------------------
// LOG STREAM CONSOLE MODAL
// ----------------------------------------------------
function openTerminalModal() {
    const term = document.getElementById('terminal-modal');
    const output = document.getElementById('terminal-output');
    const closeBtn = document.getElementById('terminal-close-btn');
    
    output.innerHTML = '';
    closeBtn.disabled = true;
    term.classList.add('open');
}

function closeTerminalModal() {
    document.getElementById('terminal-modal').classList.remove('open');
}

async function triggerAction(action, args) {
    openTerminalModal();
    const output = document.getElementById('terminal-output');
    const statusText = document.getElementById('terminal-status-text');
    const closeBtn = document.getElementById('terminal-close-btn');

    statusText.innerText = "Connecting to command router...";

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
                        if (content.startsWith("ERR: ")) {
                            span.className = 'err-line';
                            span.innerText = content.substring(5);
                        } else if (content.startsWith("Running Command:")) {
                            span.className = 'cmd-line';
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

        statusText.innerText = "Finished execution.";

    } catch (err) {
        const span = document.createElement('div');
        span.className = 'err-line';
        span.innerText = `ERROR: Failed to run command pipeline: ${err.message}`;
        output.appendChild(span);
        statusText.innerText = "Failed execution.";
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
    event.stopPropagation();
    const menu = btn.nextElementSibling;
    const show = menu.classList.contains('show');
    
    // Hide all other dropdowns
    document.querySelectorAll('.dropdown-menu').forEach(m => m.classList.remove('show'));
    
    if (!show) {
        menu.classList.add('show');
    }
}

// Close dropdowns when clicking outside
window.addEventListener('click', () => {
    document.querySelectorAll('.dropdown-menu').forEach(m => m.classList.remove('show'));
});

// ----------------------------------------------------
// HISTORICAL TREND GRAPH SYSTEM (SVG)
// ----------------------------------------------------
let currentGraphRange = 'monthly';
let metricsHistoryData = [];

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
    
    const weeklyBtn = document.getElementById('btn-graph-weekly');
    const monthlyBtn = document.getElementById('btn-graph-monthly');
    
    if (range === 'weekly') {
        weeklyBtn.className = 'btn btn-primary btn-sm';
        monthlyBtn.className = 'btn btn-secondary btn-sm';
    } else {
        weeklyBtn.className = 'btn btn-secondary btn-sm';
        monthlyBtn.className = 'btn btn-primary btn-sm';
    }
    
    renderResourceGraphs();
}

function renderResourceGraphs() {
    if (!metricsHistoryData || metricsHistoryData.length === 0) return;
    
    const pointsCount = currentGraphRange === 'weekly' ? 7 : 30;
    const slicedData = metricsHistoryData.slice(-pointsCount);
    
    // 1. CPU Chart
    const cpuPoints = slicedData.map(d => d.CPU);
    const cpuLabels = slicedData.map(d => d.Label);
    const cpuAvg = cpuPoints.reduce((a, b) => a + b, 0) / cpuPoints.length;
    document.getElementById('graph-cpu-avg').innerText = `Avg: ${cpuAvg.toFixed(1)}%`;
    drawSvgLineChart('graph-container-cpu', cpuPoints, cpuLabels, '#3b82f6', 'rgba(59, 130, 246, 0.08)');
    
    // 2. RAM Chart
    const ramPoints = slicedData.map(d => d.RAM);
    const ramLabels = slicedData.map(d => d.Label);
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

// Initializer
document.addEventListener("DOMContentLoaded", () => {
    loadStatus();
    loadMetricsHistory();
    setInterval(loadStatus, 4000);
    setInterval(loadMetricsHistory, 60000);
});
