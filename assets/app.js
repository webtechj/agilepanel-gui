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
        'services': 'Daemon Control Panel',
        'tools': 'System Tuning & Maintenance'
    };
    document.getElementById('page-title').innerText = titles[tabId] || 'AgilePanel';

    // Immediate update
    if (tabId === 'sites') {
        loadSites();
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

        // 5. Site count
        document.getElementById('metric-sites').innerText = data.siteCount;

        // 6. Services list rendering
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

            tr.innerHTML = `
                <td><strong>${site.domain}</strong></td>
                <td><span class="badge" style="background:#1e293b; padding:0.2rem 0.5rem; border-radius:4px;">${site.type ? site.type.toUpperCase() : 'WP'}</span></td>
                <td>PHP ${site.php_version}</td>
                <td><code>${site.system_user}</code></td>
                <td>${lockBadge}</td>
                <td>
                    <div class="actions-cell">
                        ${lockButton}
                        <button class="btn btn-primary" onclick="triggerAction('site-cache', ['${site.domain}'])">Flush Cache</button>
                        <button class="btn btn-secondary" onclick="triggerAction('site-perms', ['${site.domain}'])">Fix Perms</button>
                        <button class="btn btn-secondary" onclick="triggerAction('site-ssl', ['${site.domain}'])">SSL Renew</button>
                        <button class="btn btn-secondary" onclick="triggerAction('site-backup', ['${site.domain}'])">Backup</button>
                        <button class="btn btn-secondary" onclick="triggerAction('site-reinstall', ['${site.domain}'])">Reinstall</button>
                        <button class="btn btn-danger" onclick="confirmDeleteSite('${site.domain}')">Delete</button>
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

// Submit Create Site
function submitCreateSite(e) {
    e.preventDefault();
    const form = e.target;
    const domain = form.domain.value.trim();
    const type = form.type.value;
    const php = form.php.value;
    const wp = form.wp.checked;

    closeCreateModal();

    const args = [domain, '--type', type, '--php', php];
    if (wp || type === 'wp' || type === 'woocommerce') {
        args.push('--wp');
    }

    triggerAction('site-create', args);
    form.reset();
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

// Open CLI console
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

// Universal API runner with Live SSE Log Stream
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
                // Split lines matching SSE "data: " lines
                const lines = buffer.split("\n\n");
                // Save residual line
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
                        output.scrollTop = output.scrollHeight; // Scroll to bottom
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
        // Reload system resources states
        loadStatus();
        loadSites();
    }
}

// Initializer
document.addEventListener("DOMContentLoaded", () => {
    // Initial fetches
    loadStatus();
    
    // Refresh stats every 4 seconds
    setInterval(loadStatus, 4000);
});
