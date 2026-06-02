# Graph Report - agilepanel-gui  (2026-06-02)

## Corpus Check
- 6 files · ~23,809 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 226 nodes · 487 edges · 20 communities (15 shown, 5 thin omitted)
- Extraction: 98% EXTRACTED · 2% INFERRED · 0% AMBIGUOUS · INFERRED: 9 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `9c04930b`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- [[_COMMUNITY_UI Utility & Navigation|UI Utility & Navigation]]
- [[_COMMUNITY_File Operations API Handlers|File Operations API Handlers]]
- [[_COMMUNITY_Core Server APIs|Core Server APIs]]
- [[_COMMUNITY_File Explorer Interactions|File Explorer Interactions]]
- [[_COMMUNITY_Command & Input Validation|Command & Input Validation]]
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
- [[_COMMUNITY_Telegram Core Models|Telegram Core Models]]
- [[_COMMUNITY_Telegram Updates Model|Telegram Updates Model]]
- [[_COMMUNITY_UI File Manager Views|UI File Manager Views]]
- [[_COMMUNITY_Daemon Services Layout|Daemon Services Layout]]
- [[_COMMUNITY_Performance Tuning Features|Performance Tuning Features]]

## God Nodes (most connected - your core abstractions)
1. `Request` - 33 edges
2. `ResponseWriter` - 32 edges
3. `readState()` - 22 edges
4. `isValidDomain()` - 15 edges
5. `loadFiles()` - 14 edges
6. `validateFilePath()` - 13 edges
7. `writeState()` - 11 edges
8. `handleCommandExecuteAPI()` - 11 edges
9. `triggerAction()` - 10 edges
10. `loadSites()` - 9 edges

## Surprising Connections (you probably didn't know these)
- `TestServeStaticNotFound()` --calls--> `serveStatic()`  [INFERRED]
  main_test.go → main.go
- `TestValidationHelpers()` --calls--> `isValidDomain()`  [INFERRED]
  main_test.go → main.go
- `TestValidateFilePathTraversal()` --calls--> `validateFilePath()`  [INFERRED]
  main_test.go → main.go
- `TestValidationHelpers()` --calls--> `isValidTimestamp()`  [INFERRED]
  main_test.go → main.go
- `TestValidationHelpers()` --calls--> `isValidService()`  [INFERRED]
  main_test.go → main.go

## Import Cycles
- None detected.

## Communities (20 total, 5 thin omitted)

### Community 0 - "UI Utility & Navigation"
Cohesion: 0.08
Nodes (12): checkGuiAuthStatus(), drawSvgLineChart(), guiAuthStatus, loadMetricsHistory(), renderAuthOverlay(), renderLockSettingsUI(), renderResourceGraphs(), searchIndices (+4 more)

### Community 1 - "File Operations API Handlers"
Cohesion: 0.13
Nodes (52): GlobalConfig, GuiAuthConfig, State, TelegramChat, TelegramUser, createSession(), getClientIP(), getDatabaseSizes() (+44 more)

### Community 3 - "File Explorer Interactions"
Cohesion: 0.16
Nodes (15): deleteFileConfirm(), downloadFileContent(), enterFolder(), goUpFolder(), handleContextAction(), handleFileUploadSelected(), loadFiles(), onFileSiteChange() (+7 more)

### Community 4 - "Command & Input Validation"
Cohesion: 0.21
Nodes (15): handleCommandExecuteAPI(), isValidImportPath(), isValidService(), isValidTimestamp(), isValidTool(), stripANSI(), TestGetCPUWindows(), TestGetDiskWindows() (+7 more)

### Community 6 - "Metrics Logging & Middleware"
Cohesion: 0.15
Nodes (19): HistoryPoint, SiteConfig, HandlerFunc, basicAuth(), checkAndTriggerScheduledBackups(), getHistoryPath(), getServiceStatusText(), loadOrCreateHistory() (+11 more)

### Community 7 - "Site Management Modals"
Cohesion: 0.24
Nodes (10): closeManageModal(), confirmDeleteSite(), loadWizardVersions(), openRestoreWizard(), openTerminalModal(), runManageAction(), setWizardSource(), triggerAction() (+2 more)

### Community 8 - "S3 Backups & S3 UI"
Cohesion: 0.22
Nodes (10): deleteS3Backup(), loadS3BackupList(), loadSites(), openManageModal(), toggleS3AccessUI(), toggleS3Enabled(), toggleStagingUnlock(), updateBackupDestination() (+2 more)

### Community 10 - "Linux Metrics Collector"
Cohesion: 0.24
Nodes (3): getDisk(), getRAM(), MemInfo

### Community 11 - "Windows Metrics Collector"
Cohesion: 0.24
Nodes (3): getDisk(), getRAM(), MemInfo

### Community 12 - "Web File Editor"
Cohesion: 0.28
Nodes (9): editorFindNext(), editorReplace(), editorReplaceAll(), highlightCurrentMatch(), onEditorFind(), openFileEditor(), syncEditorScroll(), toggleEditorSearch() (+1 more)

### Community 13 - "Site Creation Wizard"
Cohesion: 0.33
Nodes (7): closeCreateModal(), goToCreateStep(), onSiteTypeChange(), openCreateModal(), submitCreateSite(), toggleCreateMode(), uploadImportFile()

### Community 14 - "Dashboard Tabs Setup"
Cohesion: 0.33
Nodes (6): closeSidebar(), initFileManagerContexts(), loadS3Settings(), loadTelegramSettings(), runSecurityDashboardScan(), switchTab()

### Community 15 - "System Architecture Docs"
Cohesion: 0.13
Nodes (14): Dashboard Tab, Security Audit Tab, Websites Management Tab, 1. Compile the Binary, 2. Configure the Systemd Daemon, 3. Open UFW Access Port, AgilePanel GUI Dashboard, 👑 AgilePanel GUI — Interactive Web Dashboard (+6 more)

### Community 16 - "Status & Restore Utilities"
Cohesion: 0.50
Nodes (4): formatGB(), loadStatus(), submitRestoreWizard(), triggerRestoreStream()

### Community 17 - "Telegram Core Models"
Cohesion: 0.67
Nodes (3): TelegramMessage, TelegramChat, TelegramUser

## Knowledge Gaps
- **19 isolated node(s):** `guiAuthStatus`, `searchIndices`, `TelegramUpdate`, `TelegramMessage`, `TelegramUser` (+14 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **5 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `TestValidationHelpers()` connect `Command & Input Validation` to `File Operations API Handlers`?**
  _High betweenness centrality (0.015) - this node is a cross-community bridge._
- **Why does `validateFilePath()` connect `File Operations API Handlers` to `Command & Input Validation`?**
  _High betweenness centrality (0.009) - this node is a cross-community bridge._
- **What connects `guiAuthStatus`, `searchIndices`, `TelegramUpdate` to the rest of the system?**
  _20 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `UI Utility & Navigation` be split into smaller, more focused modules?**
  _Cohesion score 0.07956989247311828 - nodes in this community are weakly interconnected._
- **Should `File Operations API Handlers` be split into smaller, more focused modules?**
  _Cohesion score 0.12844702467343977 - nodes in this community are weakly interconnected._
- **Should `System Architecture Docs` be split into smaller, more focused modules?**
  _Cohesion score 0.13333333333333333 - nodes in this community are weakly interconnected._