# Graph Report - agilepanel-gui  (2026-06-02)

## Corpus Check
- 6 files · ~23,809 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 218 nodes · 482 edges · 21 communities (19 shown, 2 thin omitted)
- Extraction: 99% EXTRACTED · 1% INFERRED · 0% AMBIGUOUS · INFERRED: 7 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `a82b7895`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- [[_COMMUNITY_Community 0|Community 0]]
- [[_COMMUNITY_Community 1|Community 1]]
- [[_COMMUNITY_Community 2|Community 2]]
- [[_COMMUNITY_Community 3|Community 3]]
- [[_COMMUNITY_Community 4|Community 4]]
- [[_COMMUNITY_Community 5|Community 5]]
- [[_COMMUNITY_Community 6|Community 6]]
- [[_COMMUNITY_Community 7|Community 7]]
- [[_COMMUNITY_Community 8|Community 8]]
- [[_COMMUNITY_Community 9|Community 9]]
- [[_COMMUNITY_Community 10|Community 10]]
- [[_COMMUNITY_Community 11|Community 11]]
- [[_COMMUNITY_Community 12|Community 12]]
- [[_COMMUNITY_Community 13|Community 13]]
- [[_COMMUNITY_Community 14|Community 14]]
- [[_COMMUNITY_Community 15|Community 15]]
- [[_COMMUNITY_Community 16|Community 16]]
- [[_COMMUNITY_Community 17|Community 17]]
- [[_COMMUNITY_Community 18|Community 18]]
- [[_COMMUNITY_Community 19|Community 19]]
- [[_COMMUNITY_Community 20|Community 20]]

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

## Communities (21 total, 2 thin omitted)

### Community 0 - "Community 0"
Cohesion: 0.25
Nodes (19): handleAuthLogoutAPI(), handleAuthStatusAPI(), handleBackupDownloadAPI(), handleFileCreateAPI(), handleFileDeleteAPI(), handleFileListAPI(), handleFileReadAPI(), handleFileRenameAPI() (+11 more)

### Community 1 - "Community 1"
Cohesion: 0.08
Nodes (13): checkGuiAuthStatus(), drawSvgLineChart(), guiAuthStatus, loadMetricsHistory(), metricsHistoryData, renderAuthOverlay(), renderLockSettingsUI(), renderResourceGraphs() (+5 more)

### Community 2 - "Community 2"
Cohesion: 0.39
Nodes (8): TestGetCPUWindows(), TestGetDiskWindows(), TestGetRAMWindows(), TestGetServiceStatusWindows(), TestGuiAuthSignupAndLogin(), TestServeStaticNotFound(), TestValidateFilePathTraversal(), T

### Community 3 - "Community 3"
Cohesion: 0.16
Nodes (15): deleteFileConfirm(), downloadFileContent(), enterFolder(), goUpFolder(), handleContextAction(), handleFileUploadSelected(), loadFiles(), onFileSiteChange() (+7 more)

### Community 4 - "Community 4"
Cohesion: 0.22
Nodes (13): HistoryPoint, SiteConfig, HandlerFunc, basicAuth(), getHistoryPath(), loadOrCreateHistory(), main(), recordCurrentMetrics() (+5 more)

### Community 5 - "Community 5"
Cohesion: 0.24
Nodes (10): closeManageModal(), confirmDeleteSite(), loadWizardVersions(), openRestoreWizard(), openTerminalModal(), runManageAction(), setWizardSource(), triggerAction() (+2 more)

### Community 6 - "Community 6"
Cohesion: 0.22
Nodes (10): deleteS3Backup(), loadS3BackupList(), loadSites(), openManageModal(), toggleS3AccessUI(), toggleS3Enabled(), toggleStagingUnlock(), updateBackupDestination() (+2 more)

### Community 7 - "Community 7"
Cohesion: 0.24
Nodes (3): getDisk(), getRAM(), MemInfo

### Community 8 - "Community 8"
Cohesion: 0.24
Nodes (3): getDisk(), getRAM(), MemInfo

### Community 9 - "Community 9"
Cohesion: 0.20
Nodes (9): 1. Compile the Binary, 2. Configure the Systemd Daemon, 3. Open UFW Access Port, 👑 AgilePanel GUI — Interactive Web Dashboard, 📥 Installation & Setup, ⚡ Key Features, 📄 License, 🔒 Security Practices (+1 more)

### Community 10 - "Community 10"
Cohesion: 0.28
Nodes (9): editorFindNext(), editorReplace(), editorReplaceAll(), highlightCurrentMatch(), onEditorFind(), openFileEditor(), syncEditorScroll(), toggleEditorSearch() (+1 more)

### Community 11 - "Community 11"
Cohesion: 0.33
Nodes (9): GuiAuthConfig, createSession(), getClientIP(), getGuiAuthPath(), handleAuthLoginAPI(), handleAuthSignupAPI(), handleAuthToggleAPI(), readGuiAuth() (+1 more)

### Community 12 - "Community 12"
Cohesion: 0.33
Nodes (7): closeCreateModal(), goToCreateStep(), onSiteTypeChange(), openCreateModal(), submitCreateSite(), toggleCreateMode(), uploadImportFile()

### Community 13 - "Community 13"
Cohesion: 0.33
Nodes (6): closeSidebar(), initFileManagerContexts(), loadS3Settings(), loadTelegramSettings(), runSecurityDashboardScan(), switchTab()

### Community 14 - "Community 14"
Cohesion: 0.50
Nodes (4): formatGB(), loadStatus(), submitRestoreWizard(), triggerRestoreStream()

### Community 15 - "Community 15"
Cohesion: 0.67
Nodes (3): TelegramMessage, TelegramChat, TelegramUser

### Community 18 - "Community 18"
Cohesion: 0.25
Nodes (15): checkAndTriggerScheduledBackups(), getServiceStatusText(), getStatePath(), handleS3SettingsAPI(), handleSitesToggleS3EnabledAPI(), handleSitesToggleStagingUnlockAPI(), handleSitesUpdateBackupDestinationAPI(), handleSitesUpdateBackupIntervalAPI() (+7 more)

### Community 19 - "Community 19"
Cohesion: 0.22
Nodes (12): GlobalConfig, State, TelegramChat, TelegramUser, getDatabaseSizes(), getPublicIP(), handleFileUnzipAPI(), handleFileZipAPI() (+4 more)

### Community 20 - "Community 20"
Cohesion: 0.31
Nodes (10): handleCommandExecuteAPI(), handleSiteRestoreAPI(), handleSitesS3DeleteAPI(), handleSitesS3RestoreAPI(), isValidImportPath(), isValidService(), isValidTimestamp(), isValidTool() (+2 more)

## Knowledge Gaps
- **16 isolated node(s):** `guiAuthStatus`, `searchIndices`, `TelegramUpdate`, `TelegramMessage`, `TelegramUser` (+11 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **2 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `TestValidationHelpers()` connect `Community 20` to `Community 0`, `Community 2`?**
  _High betweenness centrality (0.017) - this node is a cross-community bridge._
- **Why does `validateFilePath()` connect `Community 0` to `Community 18`, `Community 2`, `Community 19`?**
  _High betweenness centrality (0.010) - this node is a cross-community bridge._
- **Why does `T` connect `Community 2` to `Community 20`?**
  _High betweenness centrality (0.010) - this node is a cross-community bridge._
- **What connects `guiAuthStatus`, `searchIndices`, `TelegramUpdate` to the rest of the system?**
  _16 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Community 1` be split into smaller, more focused modules?**
  _Cohesion score 0.07661290322580645 - nodes in this community are weakly interconnected._