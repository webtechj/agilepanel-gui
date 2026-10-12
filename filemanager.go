package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func validateFilePath(domain string, path string) (string, error) {
	if domain != "" && !isValidDomain(domain) {
		return "", fmt.Errorf("access denied: invalid domain")
	}

	var rootDir string
	if runtime.GOOS == "windows" {
		rootDir = "./var/www"
	} else {
		rootDir = "/var/www"
	}
	absRootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve root directory: %w", err)
	}

	// 1. Check virtual configuration redirects
	slashedPath := strings.ReplaceAll(filepath.ToSlash(path), "\\", "/")
	if slashedPath == "conf/php-fpm.conf" {
		state, err := readState()
		if err == nil {
			for _, s := range state.Sites {
				if strings.EqualFold(s.Domain, domain) {
					var phpConf string
					if runtime.GOOS == "windows" {
						phpConf = filepath.Clean(fmt.Sprintf("./etc/php/%s/fpm/pool.d/%s.conf", s.PHPVersion, domain))
					} else {
						phpConf = fmt.Sprintf("/etc/php/%s/fpm/pool.d/%s.conf", s.PHPVersion, domain)
					}
					_ = os.MkdirAll(filepath.Dir(phpConf), 0755)
					absPhpConf, err := filepath.Abs(phpConf)
					if err != nil {
						return "", fmt.Errorf("invalid path: %w", err)
					}
					return absPhpConf, nil
				}
			}
		}
	}

	if slashedPath == "conf/caddy.conf" {
		var caddyFile string
		if runtime.GOOS == "windows" {
			caddyFile = filepath.Clean("./etc/caddy/Caddyfile")
		} else {
			caddyFile = "/etc/caddy/Caddyfile"
		}
		absCaddyFile, err := filepath.Abs(caddyFile)
		if err != nil {
			return "", fmt.Errorf("invalid path: %w", err)
		}
		return absCaddyFile, nil
	}

	baseDir := rootDir
	if domain != "" {
		baseDir = filepath.Join(rootDir, domain)
	}
	absBaseDir, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve base directory: %w", err)
	}

	var fullPath string
	if filepath.IsAbs(path) {
		fullPath = filepath.Clean(path)
	} else {
		fullPath = filepath.Clean(filepath.Join(absBaseDir, path))
	}

	absFullPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve full path: %w", err)
	}

	// Securely resolve symlinks to prevent path traversal bypasses
	var parts []string
	current := absFullPath
	for {
		eval, err := filepath.EvalSymlinks(current)
		if err == nil {
			absFullPath = eval
			for i := len(parts) - 1; i >= 0; i-- {
				absFullPath = filepath.Join(absFullPath, parts[i])
			}
			break
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("failed to evaluate path symlinks: %w", err)
		}
		dir := filepath.Dir(current)
		if dir == current || dir == "." || dir == "\\" || dir == "/" {
			break
		}
		parts = append(parts, filepath.Base(current))
		current = dir
	}

	// Whitelist check: absolute path must resolve within allowedRootDir
	if absFullPath != absRootDir {
		prefixRoot := absRootDir + string(filepath.Separator)
		if !strings.HasPrefix(absFullPath, prefixRoot) {
			return "", fmt.Errorf("access denied: path is outside the allowed root directory")
		}
	}

	// If domain is specified, absolute path must resolve within domain's baseDir
	if domain != "" {
		if absFullPath != absBaseDir {
			prefixBase := absBaseDir + string(filepath.Separator)
			if !strings.HasPrefix(absFullPath, prefixBase) {
				return "", fmt.Errorf("access denied: path is outside the domain directory")
			}
		}
	}

	return absFullPath, nil
}

func handleFileListAPI(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	relPath := r.URL.Query().Get("path")
	if domain == "" {
		http.Error(w, "Domain parameter required", http.StatusBadRequest)
		return
	}

	type FileItem struct {
		Name    string `json:"name"`
		Size    int64  `json:"size"`
		IsDir   bool   `json:"isDir"`
		ModTime string `json:"modTime"`
		Mode    string `json:"mode"`
	}

	// 1. Intercept listing the virtual "conf" folder
	slashedPath := strings.ReplaceAll(filepath.ToSlash(relPath), "\\", "/")
	if slashedPath == "conf" {
		var phpSize, caddySize int64
		phpSize = 1024
		caddySize = 2048
		
		state, err := readState()
		if err == nil {
			for _, s := range state.Sites {
				if strings.EqualFold(s.Domain, domain) {
					var phpConf string
					if runtime.GOOS == "windows" {
						phpConf = fmt.Sprintf("./etc/php/%s/fpm/pool.d/%s.conf", s.PHPVersion, domain)
					} else {
						phpConf = fmt.Sprintf("/etc/php/%s/fpm/pool.d/%s.conf", s.PHPVersion, domain)
					}
					if info, err := os.Stat(phpConf); err == nil {
						phpSize = info.Size()
					}
				}
			}
		}
		
		caddyPath := "/etc/caddy/Caddyfile"
		if runtime.GOOS == "windows" {
			caddyPath = "./etc/caddy/Caddyfile"
		}
		if info, err := os.Stat(caddyPath); err == nil {
			caddySize = info.Size()
		}
		
		nowStr := time.Now().Format("2006-01-02 15:04:05")
		items := []FileItem{
			{Name: "php-fpm.conf", Size: phpSize, IsDir: false, ModTime: nowStr, Mode: "-rw-r--r--"},
			{Name: "caddy.conf", Size: caddySize, IsDir: false, ModTime: nowStr, Mode: "-rw-r--r--"},
		}
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(items)
		return
	}

	fullPath, err := validateFilePath(domain, relPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	// Local development mock setup if directories don't exist
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		_ = os.MkdirAll(fullPath, 0755)
		if strings.HasSuffix(fullPath, "htdocs") {
			_ = os.WriteFile(filepath.Join(fullPath, "index.html"), []byte("<h1>Mock Webroot</h1>"), 0644)
		}
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read directory: %v", err), http.StatusInternalServerError)
		return
	}

	var items []FileItem
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		items = append(items, FileItem{
			Name:    entry.Name(),
			Size:    info.Size(),
			IsDir:   entry.IsDir(),
			ModTime: info.ModTime().Format("2006-01-02 15:04:05"),
			Mode:    info.Mode().String(),
		})
	}

	// 2. Inject virtual "conf" directory when listing root
	if relPath == "" || relPath == "." {
		hasConf := false
		for _, item := range items {
			if item.Name == "conf" {
				hasConf = true
				break
			}
		}
		if !hasConf {
			items = append(items, FileItem{
				Name:    "conf",
				Size:    4096,
				IsDir:   true,
				ModTime: time.Now().Format("2006-01-02 15:04:05"),
				Mode:    "drwxr-xr-x",
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

func handleFileReadAPI(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	relPath := r.URL.Query().Get("path")
	if domain == "" || relPath == "" {
		http.Error(w, "Domain and path parameters required", http.StatusBadRequest)
		return
	}

	fullPath, err := validateFilePath(domain, relPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	if r.URL.Query().Get("download") == "true" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filepath.Base(fullPath)))
	}

	http.ServeFile(w, r, fullPath)
}

func handleFileWriteAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Domain  string `json:"domain"`
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if payload.Domain == "" || payload.Path == "" {
		http.Error(w, "Domain and path required", http.StatusBadRequest)
		return
	}

	fullPath, err := validateFilePath(payload.Domain, payload.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	err = os.WriteFile(fullPath, []byte(payload.Content), 0640)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to write file: %v", err), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("File saved successfully"))
}

func handleFileCreateAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Domain string `json:"domain"`
		Path   string `json:"path"`
		Name   string `json:"name"`
		IsDir  bool   `json:"isDir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if payload.Domain == "" || payload.Name == "" {
		http.Error(w, "Domain and name required", http.StatusBadRequest)
		return
	}

	targetPath := filepath.Join(payload.Path, payload.Name)
	fullPath, err := validateFilePath(payload.Domain, targetPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	var createErr error
	if payload.IsDir {
		createErr = os.MkdirAll(fullPath, 0755)
	} else {
		parent := filepath.Dir(fullPath)
		_ = os.MkdirAll(parent, 0755)
		var file *os.File
		file, createErr = os.Create(fullPath)
		if createErr == nil {
			file.Close()
		}
	}

	if createErr != nil {
		http.Error(w, fmt.Sprintf("Failed to create: %v", createErr), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Created successfully"))
}

func handleFileDeleteAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Domain string `json:"domain"`
		Path   string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if payload.Domain == "" || payload.Path == "" {
		http.Error(w, "Domain and path required", http.StatusBadRequest)
		return
	}

	fullPath, err := validateFilePath(payload.Domain, payload.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	// Secure boundary
	if fullPath == "/var/www/"+payload.Domain || fullPath == "/var/www/"+payload.Domain+"/htdocs" || fullPath == "./var/www/"+payload.Domain || fullPath == "./var/www/"+payload.Domain+"/htdocs" {
		http.Error(w, "Access denied: cannot delete webroot folders", http.StatusForbidden)
		return
	}

	err = os.RemoveAll(fullPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to delete: %v", err), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Deleted successfully"))
}

func handleFileUploadAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ip := getClientIP(r)

	err := r.ParseMultipartForm(32 << 20)
	if err != nil {
		logAuditEvent(ip, "file-upload", "Failed to parse form", "error")
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	domain := r.FormValue("domain")
	relPath := r.FormValue("path")
	if domain == "" {
		http.Error(w, "Domain parameter required", http.StatusBadRequest)
		return
	}

	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		http.Error(w, "File parameter required", http.StatusBadRequest)
		return
	}

	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			continue // skip files that fail to open
		}

		safeFilename := filepath.Base(header.Filename)
		if safeFilename == "." || safeFilename == "/" || strings.Contains(header.Filename, "..") || strings.Contains(safeFilename, "..") {
			logAuditEvent(ip, "file-upload", fmt.Sprintf("Invalid filename attempt: %s", header.Filename), "denied")
			file.Close()
			continue
		}
		targetPath := filepath.Join(relPath, safeFilename)
		fullPath, err := validateFilePath(domain, targetPath)
		if err != nil {
			logAuditEvent(ip, "file-upload", fmt.Sprintf("Path validation failed: %v", err), "denied")
			file.Close()
			continue
		}

		out, err := os.Create(fullPath)
		if err != nil {
			logAuditEvent(ip, "file-upload", fmt.Sprintf("Create destination failed: %v", err), "error")
			file.Close()
			continue
		}

		_, err = io.Copy(out, file)
		out.Close()
		file.Close()
		
		if err != nil {
			logAuditEvent(ip, "file-upload", fmt.Sprintf("Save file failed: %v", err), "error")
			continue
		}

		// Virus scan the saved file
		if err := scanFileForViruses(fullPath); err != nil {
			os.Remove(fullPath)
			logAuditEvent(ip, "file-upload", fmt.Sprintf("Virus scan block: %s (%v)", safeFilename, err), "blocked")
			continue
		}

		logAuditEvent(ip, "file-upload", fmt.Sprintf("Successfully uploaded %s to %s", safeFilename, domain), "success")
	}

	w.Write([]byte("File(s) uploaded successfully"))
}

func zipFiles(source, target string) error {
	zipfile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer zipfile.Close()

	archive := zip.NewWriter(zipfile)
	defer archive.Close()

	info, err := os.Stat(source)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.Base(source)
		header.Method = zip.Deflate

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		file, err := os.Open(source)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(writer, file)
		return err
	}

	err = filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		header.Name = filepath.ToSlash(relPath)
		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(writer, file)
		return err
	})

	return err
}

func unzipFiles(source, target string) error {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer reader.Close()

	cleanTarget := filepath.Clean(target)
	prefix := cleanTarget + string(filepath.Separator)

	var totalExtracted int64
	const maxExtractSize = 5 * 1024 * 1024 * 1024 // 5GB limit to prevent zip bombs

	for _, file := range reader.File {
		if file.UncompressedSize64 > maxExtractSize {
			return fmt.Errorf("file %s is too large to extract", file.Name)
		}

		filePath := filepath.Join(target, file.Name)
		cleanFilePath := filepath.Clean(filePath)

		if cleanFilePath != cleanTarget && !strings.HasPrefix(cleanFilePath, prefix) {
			return fmt.Errorf("illegal file path inside zip: %s", file.Name)
		}

		if file.FileInfo().IsDir() {
			os.MkdirAll(cleanFilePath, os.ModePerm)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(cleanFilePath), os.ModePerm); err != nil {
			return err
		}

		dstFile, err := os.OpenFile(cleanFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			return err
		}

		srcFile, err := file.Open()
		if err != nil {
			dstFile.Close()
			return err
		}

		limitReader := io.LimitReader(srcFile, maxExtractSize-totalExtracted+1)
		written, err := io.Copy(dstFile, limitReader)
		totalExtracted += written

		srcFile.Close()
		dstFile.Close()
		
		if err != nil {
			return err
		}
		
		if totalExtracted > maxExtractSize {
			return fmt.Errorf("zip extraction exceeded maximum allowed size of %d bytes", maxExtractSize)
		}
	}
	return nil
}

func handleFileZipAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Domain string `json:"domain"`
		Path   string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if payload.Domain == "" || payload.Path == "" {
		http.Error(w, "Domain and path required", http.StatusBadRequest)
		return
	}

	fullPath, err := validateFilePath(payload.Domain, payload.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	zipPath := fullPath + ".zip"
	err = zipFiles(fullPath, zipPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to zip: %v", err), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Zipped successfully"))
}

func handleFileUnzipAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Domain string `json:"domain"`
		Path   string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if payload.Domain == "" || payload.Path == "" {
		http.Error(w, "Domain and path required", http.StatusBadRequest)
		return
	}

	fullPath, err := validateFilePath(payload.Domain, payload.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	destDir := filepath.Dir(fullPath)
	err = unzipFiles(fullPath, destDir)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to unzip: %v", err), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Unzipped successfully"))
}

func handleFileRenameAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Domain  string `json:"domain"`
		OldPath string `json:"oldPath"`
		NewPath string `json:"newPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if payload.Domain == "" || payload.OldPath == "" || payload.NewPath == "" {
		http.Error(w, "Domain, oldPath, and newPath required", http.StatusBadRequest)
		return
	}

	oldFullPath, err := validateFilePath(payload.Domain, payload.OldPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	newFullPath, err := validateFilePath(payload.Domain, payload.NewPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	_ = os.MkdirAll(filepath.Dir(newFullPath), 0755)

	err = os.Rename(oldFullPath, newFullPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to rename: %v", err), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Renamed successfully"))
}

func handleFileCopyAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Domain  string `json:"domain"`
		OldPath string `json:"oldPath"`
		NewPath string `json:"newPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if payload.Domain == "" || payload.OldPath == "" || payload.NewPath == "" {
		http.Error(w, "Domain, oldPath, and newPath required", http.StatusBadRequest)
		return
	}

	oldFullPath, err := validateFilePath(payload.Domain, payload.OldPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	newFullPath, err := validateFilePath(payload.Domain, payload.NewPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	_ = os.MkdirAll(filepath.Dir(newFullPath), 0755)

	src, err := os.Open(oldFullPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to open source: %v", err), http.StatusInternalServerError)
		return
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to stat source: %v", err), http.StatusInternalServerError)
		return
	}

	if info.IsDir() {
		http.Error(w, "Copying directories is not supported yet", http.StatusBadRequest)
		return
	}

	dst, err := os.OpenFile(newFullPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create destination: %v", err), http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		http.Error(w, fmt.Sprintf("Failed to copy data: %v", err), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Copied successfully"))
}
