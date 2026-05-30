// Tab management
function switchTab(tabId) {
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
    if (tabId === 'sites') {
        loadSites();
    } else if (tabId === 'files') {
        initFileManagerContexts();
    } else if (tabId === 'security') {
        runSecurityDashboardScan();
    } else if (tabId === 'tools') {
        loadS3Settings();
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
        if (servicesContainer) {
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
                    <a href="${site.staging_url}" target="_blank" style="color: #60a5fa; font-size: 0.8rem; text-decoration: none; display: inline-flex; align-items: center; gap: 0.25rem; margin-top: 0.25rem; margin-bottom: 0.25rem;">🔍 Staging Link 🔗</a>
                    ${site.has_files_backup || site.has_db_backup ? `
                        <div style="font-size: 0.75rem; color: #a7f3d0; opacity: 0.9; margin-top: 0.25rem; display: flex; flex-direction: column; gap: 0.15rem;">
                            ${site.has_files_backup ? `<span>💾 Files Backup: <span style="color:#e2e8f0; font-family:var(--font-mono); font-weight:600;">${site.files_backup_time}</span></span>` : ''}
                            ${site.has_db_backup ? `<span>🗄️ DB Backup: <span style="color:#e2e8f0; font-family:var(--font-mono); font-weight:600;">${site.db_backup_time}</span></span>` : ''}
                        </div>
                    ` : ''}
                </td>
                <td><span class="badge" style="background:#1e293b; padding:0.2rem 0.5rem; border-radius:4px;">${site.type ? site.type.toUpperCase() : 'WP'}</span></td>
                <td>PHP ${site.php_version}</td>
                <td><code>${site.system_user}</code></td>
                <td>${lockBadge}</td>
                <td>
                    <div class="actions-cell">
                        ${filesBackupBtn}
                        ${dbBackupBtn}
                        
                        <button class="btn btn-secondary" onclick="openManageModal('${site.domain}', ${site.is_locked})" style="font-weight:600;">Manage ⚙️</button>
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
let fileCurrentPath = "";
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
    fileCurrentPath = ""; // reset back to domain root
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
    const isRoot = fileCurrentPath === "htdocs" || fileCurrentPath === "" || fileCurrentPath === "." || fileCurrentPath === "./";
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
            
            let icon = "📄";
            if (file.isDir) {
                icon = "📁";
            } else {
                const ext = file.name.split('.').pop().toLowerCase();
                switch (ext) {
                    case 'zip':
                    case 'tar':
                    case 'gz':
                    case 'rar':
                    case '7z':
                        icon = "📦";
                        break;
                    case 'html':
                    case 'htm':
                        icon = "🌐";
                        break;
                    case 'css':
                        icon = "🎨";
                        break;
                    case 'js':
                        icon = "⚡";
                        break;
                    case 'json':
                    case 'xml':
                    case 'yml':
                    case 'yaml':
                        icon = "⚙️";
                        break;
                    case 'php':
                        icon = "🐘";
                        break;
                    case 'png':
                    case 'jpg':
                    case 'jpeg':
                    case 'gif':
                    case 'svg':
                    case 'ico':
                    case 'webp':
                        icon = "🖼️";
                        break;
                    case 'sql':
                        icon = "🛢️";
                        break;
                    case 'md':
                    case 'txt':
                    case 'conf':
                    case 'ini':
                    case 'htaccess':
                        icon = "📝";
                        break;
                }
            }

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
            
            // Attach right-click context menu
            tr.addEventListener('contextmenu', (e) => {
                e.preventDefault();
                showFileContextMenu(e, file);
            });

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
    if (fileCurrentPath === "htdocs" || fileCurrentPath === "" || fileCurrentPath === ".") return;
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

function handleFileUploadSelected() {
    const input = document.getElementById('file-upload-input');
    if (!input.files || input.files.length === 0) return;

    const file = input.files[0];
    const formData = new FormData();
    formData.append("domain", fileCurrentDomain);
    formData.append("path", fileCurrentPath);
    formData.append("file", file);

    const progressModal = document.getElementById('upload-progress-modal');
    const progressFilename = document.getElementById('upload-progress-filename');
    const progressBar = document.getElementById('upload-progress-bar');
    const progressPct = document.getElementById('upload-progress-pct');
    const progressBytes = document.getElementById('upload-progress-bytes');

    progressFilename.innerText = file.name;
    progressBar.style.width = '0%';
    progressPct.innerText = '0%';
    progressBytes.innerText = `0.00 / ${(file.size / (1024*1024)).toFixed(2)} MB`;

    progressModal.classList.add('open');

    const xhr = new XMLHttpRequest();
    
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
            loadFiles();
        } else {
            alert("Upload failed: " + (xhr.responseText || "Unknown server error"));
        }
        input.value = '';
    };

    xhr.onerror = function() {
        progressModal.classList.remove('open');
        alert("Upload failed: network error occurred");
        input.value = '';
    };

    xhr.open("POST", "/api/files/upload");
    xhr.send(formData);
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

// Global key events for full screen editor shortcuts
window.addEventListener('keydown', (e) => {
    const modal = document.getElementById('editor-modal');
    if (modal && modal.classList.contains('open')) {
        // Ctrl+S / Cmd+S
        if ((e.ctrlKey || e.metaKey) && e.key === 's') {
            e.preventDefault();
            saveEditorContent();
        }
        // Ctrl+F / Cmd+F
        if ((e.ctrlKey || e.metaKey) && e.key === 'f') {
            e.preventDefault();
            toggleEditorSearch(true);
        }
        // Esc
        if (e.key === 'Escape') {
            e.preventDefault();
            const searchBar = document.getElementById('editor-search-replace');
            if (searchBar && !searchBar.classList.contains('hidden')) {
                toggleEditorSearch(false);
            } else {
                closeEditorModal();
            }
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
            if (guiAuthStatus.enabled && guiAuthStatus.authenticated) {
                logoutLink.style.display = 'flex';
            } else {
                logoutLink.style.display = 'none';
            }
        }
    } catch (err) {
        console.error("checkGuiAuthStatus failed:", err);
    }
}

async function triggerLogout() {
    if (confirm("Are you sure you want to log out of your session?")) {
        try {
            await fetch('/api/auth/logout', { method: 'POST' });
            location.reload();
        } catch (err) {
            console.error("Logout failed:", err);
            location.reload();
        }
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
    if (site) {
        document.getElementById('manage-staging-unlocked-toggle').checked = site.staging_unlocked || false;
        document.getElementById('manage-backup-interval-select').value = site.backup_interval || 'none';
        document.getElementById('manage-backup-destination-select').value = site.backup_destination || 'local';
        document.getElementById('manage-s3-versions-select').value = site.s3_backup_versions || 5;
        document.getElementById('manage-s3-enabled-toggle').checked = site.s3_enabled || false;
        toggleS3AccessUI(site.s3_enabled || false);
    } else {
        document.getElementById('manage-staging-unlocked-toggle').checked = false;
        document.getElementById('manage-backup-interval-select').value = 'none';
        document.getElementById('manage-backup-destination-select').value = 'local';
        document.getElementById('manage-s3-versions-select').value = 5;
        document.getElementById('manage-s3-enabled-toggle').checked = false;
        toggleS3AccessUI(false);
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
        triggerRestoreSite(domain);
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
    
    // Let's find context menu buttons to toggle status
    const items = menu.querySelectorAll('.context-item');
    items.forEach(btn => {
        const action = btn.getAttribute('onclick');
        if (action.includes("'edit'")) {
            const fileExtension = file.name.split('.').pop().toLowerCase();
            const editableExtensions = ['html', 'css', 'js', 'php', 'txt', 'json', 'conf', 'ini', 'htaccess'];
            btn.style.display = (!file.isDir && editableExtensions.includes(fileExtension)) ? 'block' : 'none';
        } else if (action.includes("'unzip'")) {
            // Cannot unzip inside virtual conf
            btn.style.display = (isZip && !isConfDir) ? 'block' : 'none';
        } else if (action.includes("'download'")) {
            btn.style.display = !file.isDir ? 'block' : 'none';
        } else if (action.includes("'zip'")) {
            // Cannot zip inside virtual conf
            btn.style.display = (!isZip && !isConfDir) ? 'block' : 'none';
        } else if (action.includes("'rename'")) {
            // Cannot rename inside virtual conf
            btn.style.display = !isConfDir ? 'block' : 'none';
        } else if (action.includes("'delete'")) {
            // Cannot delete inside virtual conf
            btn.style.display = !isConfDir ? 'block' : 'none';
        }
    });
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
        case 'edit':
            openFileEditor(file.name);
            break;
        case 'download':
            downloadFileContent(file.name);
            break;
        case 'rename':
            renameFilePrompt(filePath, file.name);
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
                <button class="btn btn-primary btn-sm" style="padding: 0.25rem 0.5rem; font-size: 0.7rem;" onclick="event.stopPropagation(); triggerS3RestoreConfirmation('${domain}', '${ts}')">Restore</button>
            `;
            container.appendChild(div);
        });
    } catch (err) {
        // Show both a user-friendly message and the actual error for debugging
        container.innerHTML = `<div style="color: var(--text-muted); font-size: 0.8rem; padding: 0.5rem 0;">☁️ Could not load S3 backup history: ${err.message}</div>`;
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
let s3RestoreTimestamp = "";

function triggerS3RestoreConfirmation(domain, timestamp) {
    s3RestoreDomain = domain;
    s3RestoreTimestamp = timestamp;
    
    document.getElementById('s3-restore-target-domain-label').innerText = domain;
    document.getElementById('s3-restore-confirm-domain').value = '';
    document.getElementById('s3-restore-double-check').checked = false;
    document.getElementById('btn-s3-restore-submit').disabled = true;
    document.getElementById('btn-s3-restore-submit').style.opacity = '0.5';
    
    closeManageModal();
    
    document.getElementById('s3-restore-modal').classList.add('open');
}

function closeS3RestoreModal() {
    document.getElementById('s3-restore-modal').classList.remove('open');
}

function checkS3RestoreFormValidity() {
    const typedDomain = document.getElementById('s3-restore-confirm-domain').value.trim();
    const isChecked = document.getElementById('s3-restore-double-check').checked;
    const btn = document.getElementById('btn-s3-restore-submit');
    
    const isDomainMatch = typedDomain.toLowerCase() === s3RestoreDomain.toLowerCase();
    const isValid = isDomainMatch && isChecked;
    
    btn.disabled = !isValid;
    btn.style.opacity = isValid ? '1' : '0.5';
}

async function submitS3Restore(e) {
    e.preventDefault();
    closeS3RestoreModal();
    triggerS3RestoreStream(s3RestoreDomain, s3RestoreTimestamp);
}

async function triggerS3RestoreStream(domain, timestamp) {
    openTerminalModal();
    const output = document.getElementById('terminal-output');
    const statusText = document.getElementById('terminal-status-text');
    const closeBtn = document.getElementById('terminal-close-btn');

    statusText.innerText = "Connecting to S3 restore server...";

    try {
        const response = await fetch(`/api/sites/s3-restore?domain=${domain}&timestamp=${timestamp}`);

        if (!response.ok) {
            const errText = await response.text();
            throw new Error(errText || "S3 restore failed to start");
        }

        const reader = response.body.getReader();
        const decoder = new TextDecoder("utf-8");
        statusText.innerText = "Restoring from S3 Cloud - streaming logs...";

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
                        } else if (content.startsWith("Step")) {
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

        statusText.innerText = "S3 Restore completed.";

    } catch (err) {
        const span = document.createElement('div');
        span.className = 'err-line';
        span.innerText = `ERROR: S3 Restore pipeline failed: ${err.message}`;
        output.appendChild(span);
        statusText.innerText = "S3 Restore failed.";
    } finally {
        closeBtn.disabled = false;
        loadStatus();
        loadSites();
    }
}

// ----------------------------------------------------
// FILE MANAGER & EDITOR ENHANCEMENTS
// ----------------------------------------------------
function filterFiles() {
    const query = document.getElementById('file-search-input').value.toLowerCase();
    const rows = document.querySelectorAll('#files-table-body tr');
    
    rows.forEach(row => {
        if (row.querySelector('.loading-placeholder')) return;
        
        const fileNameCell = row.cells[0];
        if (fileNameCell) {
            const name = fileNameCell.innerText.toLowerCase();
            if (name.includes(query)) {
                row.style.display = '';
            } else {
                row.style.display = 'none';
            }
        }
    });
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
