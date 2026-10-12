# Graph Report - agilepanel-gui  (2026-06-16)

## Corpus Check
- 8 files · ~27,474 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 266 nodes · 667 edges · 19 communities (16 shown, 3 thin omitted)
- Extraction: 97% EXTRACTED · 3% INFERRED · 0% AMBIGUOUS · INFERRED: 20 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `ae2664bf`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- [[_COMMUNITY_UI Utility & Navigation|UI Utility & Navigation]]
- [[_COMMUNITY_File Operations API Handlers|File Operations API Handlers]]
- [[_COMMUNITY_Community 2|Community 2]]
- [[_COMMUNITY_File Explorer Interactions|File Explorer Interactions]]
- [[_COMMUNITY_Command & Input Validation|Command & Input Validation]]
- [[_COMMUNITY_Community 5|Community 5]]
- [[_COMMUNITY_Metrics Logging & Middleware|Metrics Logging & Middleware]]
- [[_COMMUNITY_Site Management Modals|Site Management Modals]]
- [[_COMMUNITY_S3 Backups & S3 UI|S3 Backups & S3 UI]]
- [[_COMMUNITY_Linux Metrics Collector|Linux Metrics Collector]]
- [[_COMMUNITY_Windows Metrics Collector|Windows Metrics Collector]]
- [[_COMMUNITY_Web File Editor|Web File Editor]]
- [[_COMMUNITY_Site Creation Wizard|Site Creation Wizard]]
- [[_COMMUNITY_Dashboard Tabs Setup|Dashboard Tabs Setup]]
- [[_COMMUNITY_System Architecture Docs|System Architecture Docs]]
- [[_COMMUNITY_Status & Restore Utilities|Status & Restore Utilities]]
- [[_COMMUNITY_UI File Manager Views|UI File Manager Views]]
- [[_COMMUNITY_Daemon Services Layout|Daemon Services Layout]]
- [[_COMMUNITY_Performance Tuning Features|Performance Tuning Features]]

## God Nodes (most connected - your core abstractions)
1. `Request` - 34 edges
2. `ResponseWriter` - 33 edges
3. `readState()` - 25 edges
4. `Request` - 25 edges
5. `ResponseWriter` - 24 edges
6. `getClientIP()` - 17 edges
7. `isValidDomain()` - 17 edges
8. `validateFilePath()` - 15 edges
9. `handleCommandExecuteAPI()` - 15 edges
10. `loadFiles()` - 14 edges

## Surprising Connections (you probably didn't know these)
- `validateFilePath()` --calls--> `isValidDomain()`  [INFERRED]
  filemanager.go → main.go
- `validateFilePath()` --calls--> `readState()`  [INFERRED]
  filemanager.go → main.go
- `TestSecurityPathWhitelistValidation()` --calls--> `validateFilePath()`  [INFERRED]
  main_security_test.go → filemanager.go
- `TestValidateFilePathTraversal()` --calls--> `validateFilePath()`  [INFERRED]
  main_test.go → filemanager.go
- `handleFileListAPI()` --calls--> `readState()`  [INFERRED]
  filemanager.go → main.go

## Import Cycles
- None detected.

## Communities (19 total, 3 thin omitted)

### Community 0 - "UI Utility & Navigation"
Cohesion: 0.08
Nodes (12): checkGuiAuthStatus(), drawSvgLineChart(), guiAuthStatus, loadMetricsHistory(), renderAuthOverlay(), renderLockSettingsUI(), renderResourceGraphs(), searchIndices (+4 more)

### Community 1 - "File Operations API Handlers"
Cohesion: 0.16
Nodes (50): getClientIP(), getStatePath(), Request, ResponseWriter, handleAuthLogoutAPI(), handleAuthStatusAPI(), handleBackupDownloadAPI(), handleCommandExecuteAPI() (+42 more)

### Community 2 - "Community 2"
Cohesion: 0.34
Nodes (16): Request, ResponseWriter, handleFileCopyAPI(), handleFileCreateAPI(), handleFileDeleteAPI(), handleFileListAPI(), handleFileReadAPI(), handleFileRenameAPI() (+8 more)

### Community 3 - "File Explorer Interactions"
Cohesion: 0.16
Nodes (15): deleteFileConfirm(), downloadFileContent(), enterFolder(), goUpFolder(), handleContextAction(), handleFileUploadSelected(), loadFiles(), onFileSiteChange() (+7 more)

### Community 4 - "Command & Input Validation"
Cohesion: 0.47
Nodes (9): T, TestGetCPUWindows(), TestGetDiskWindows(), TestGetRAMWindows(), TestGetServiceStatusWindows(), TestGuiAuthSignupAndLogin(), TestServeStaticNotFound(), TestValidateFilePathTraversal() (+1 more)

### Community 5 - "Community 5"
Cohesion: 0.53
Nodes (5): T, TestSecurityCommandArgumentSanitization(), TestSecurityPathWhitelistValidation(), TestSecurityRateLimiter(), TestSecuritySessionGeneration()

### Community 6 - "Metrics Logging & Middleware"
Cohesion: 0.09
Nodes (43): GlobalConfig, GuiAuthConfig, HistoryPoint, SiteConfig, State, TelegramChat, TelegramMessage, TelegramUpdate (+35 more)

### Community 7 - "Site Management Modals"
Cohesion: 0.24
Nodes (10): closeManageModal(), confirmDeleteSite(), loadWizardVersions(), openRestoreWizard(), openTerminalModal(), runManageAction(), setWizardSource(), triggerAction() (+2 more)

### Community 8 - "S3 Backups & S3 UI"
Cohesion: 0.18
Nodes (12): deleteS3Backup(), loadS3BackupList(), loadSites(), openManageModal(), saveDbCredentials(), showToast(), toggleS3AccessUI(), toggleS3Enabled() (+4 more)

### Community 10 - "Linux Metrics Collector"
Cohesion: 0.24
Nodes (3): getDisk(), getRAM(), MemInfo

### Community 11 - "Windows Metrics Collector"
Cohesion: 0.27
Nodes (5): getCPU(), getDisk(), getLoadAverages(), getRAM(), MemInfo

### Community 12 - "Web File Editor"
Cohesion: 0.24
Nodes (10): editorFindNext(), editorReplace(), editorReplaceAll(), highlightCurrentMatch(), onEditorFind(), openFileEditor(), openSiteCaddyConfig(), syncEditorScroll() (+2 more)

### Community 13 - "Site Creation Wizard"
Cohesion: 0.33
Nodes (7): closeCreateModal(), goToCreateStep(), onSiteTypeChange(), openCreateModal(), submitCreateSite(), toggleCreateMode(), uploadImportFile()

### Community 14 - "Dashboard Tabs Setup"
Cohesion: 0.33
Nodes (6): closeSidebar(), initFileManagerContexts(), loadS3Settings(), loadTelegramSettings(), runSecurityDashboardScan(), switchTab()

### Community 15 - "System Architecture Docs"
Cohesion: 0.10
Nodes (19): Dashboard Tab, Security Audit Tab, Websites Management Tab, 1. Compile the Binary, 1. Install the Web GUI, 1. Installation, 2. Configure the Systemd Daemon, 2. Service Management Commands (+11 more)

### Community 16 - "Status & Restore Utilities"
Cohesion: 0.40
Nodes (5): escapeHTML(), formatGB(), loadStatus(), submitRestoreWizard(), triggerRestoreStream()

## Knowledge Gaps
- **23 isolated node(s):** `guiAuthStatus`, `searchIndices`, `TelegramUpdate`, `TelegramMessage`, `TelegramUser` (+18 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **3 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `validateFilePath()` connect `Community 2` to `File Operations API Handlers`, `Command & Input Validation`, `Community 5`?**
  _High betweenness centrality (0.035) - this node is a cross-community bridge._
- **Why does `readState()` connect `File Operations API Handlers` to `Community 2`, `Metrics Logging & Middleware`?**
  _High betweenness centrality (0.025) - this node is a cross-community bridge._
- **Why does `isValidDomain()` connect `File Operations API Handlers` to `Community 2`, `Metrics Logging & Middleware`?**
  _High betweenness centrality (0.019) - this node is a cross-community bridge._
- **Are the 2 inferred relationships involving `readState()` (e.g. with `handleFileListAPI()` and `validateFilePath()`) actually correct?**
  _`readState()` has 2 INFERRED edges - model-reasoned connections that need verification._
- **What connects `guiAuthStatus`, `searchIndices`, `TelegramUpdate` to the rest of the system?**
  _24 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `UI Utility & Navigation` be split into smaller, more focused modules?**
  _Cohesion score 0.08275862068965517 - nodes in this community are weakly interconnected._
- **Should `Metrics Logging & Middleware` be split into smaller, more focused modules?**
  _Cohesion score 0.0919661733615222 - nodes in this community are weakly interconnected._