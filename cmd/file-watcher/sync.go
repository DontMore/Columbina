package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/fsnotify/fsnotify"
)

type SyncFileMeta struct {
	RelPath string    `json:"rel_path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// startUnifiedRetryLoop menggantikan retry loop lama agar bisa menangani:
// - upload biasa
// - sync_push
// - sync_pull
func startUnifiedRetryLoop() {
	go func() {
		for {
			time.Sleep(30 * time.Second)

			watcherLock.Lock()
			watching := isWatching
			watcherLock.Unlock()

			if !watching {
				continue
			}

			if db == nil {
				continue
			}

			var activePaths []WatchPath

			rows, err := db.Query(`
				SELECT
					id,
					folder_path,
					api_url,
					enabled,
					COALESCE(bearer_token, ''),
					COALESCE(mode, 'upload'),
					COALESCE(sync_folder, '')
				FROM watch_paths
				WHERE enabled = 1
			`)
			if err == nil {
				for rows.Next() {
					var wp WatchPath
					var enabledInt int

					rows.Scan(
						&wp.ID,
						&wp.FolderPath,
						&wp.ApiUrl,
						&enabledInt,
						&wp.BearerToken,
						&wp.Mode,
						&wp.SyncFolder,
					)

					wp.Enabled = enabledInt == 1

					if wp.Mode == "" {
						wp.Mode = "upload"
					}

					activePaths = append(activePaths, wp)
				}
				rows.Close()
			}

			for _, wp := range activePaths {
				switch wp.Mode {
				case "sync_push":
					runSyncPush(wp)
				case "sync_pull":
					runSyncPull(wp)
				default:
					files, err := os.ReadDir(wp.FolderPath)
					if err != nil {
						continue
					}

					for _, f := range files {
						if !f.IsDir() {
							filePath := filepath.Join(wp.FolderPath, f.Name())
							go processFileUpload(filePath, wp.ApiUrl, wp.BearerToken)
						}
					}
				}
			}
		}
	}()
}

// showAddPathDialogSync adalah dialog Add Folder yang mendukung mode upload dan sync.
func showAddPathDialogSync(parent fyne.Window, onSave func()) {
	folderEntry := widget.NewEntry()
	folderEntry.SetPlaceHolder("Local folder path to monitor")

	urlEntry := widget.NewEntry()
	urlEntry.SetPlaceHolder("e.g. http://localhost:8080/upload/pebjf")

	tokenEntry := widget.NewEntry()
	tokenEntry.SetPlaceHolder("Optional Bearer Token")

	modeSelect := widget.NewSelect([]string{"upload", "sync_push", "sync_pull"}, nil)
	modeSelect.SetSelected("upload")

	syncFolderEntry := widget.NewEntry()
	syncFolderEntry.SetPlaceHolder("Sync folder name on server, e.g. photos")

	folderBox := container.NewBorder(nil, nil, nil, widget.NewButton("Browse", func() {
		dialog.ShowFolderOpen(func(list fyne.ListableURI, err error) {
			if err == nil && list != nil {
				folderEntry.SetText(list.Path())
			}
		}, parent)
	}), folderEntry)

	form := widget.NewForm(
		widget.NewFormItem("Folder Path", folderBox),
		widget.NewFormItem("Mode", modeSelect),
		widget.NewFormItem("API Upload URL", urlEntry),
		widget.NewFormItem("Sync Folder Name", syncFolderEntry),
		widget.NewFormItem("Bearer Token (Optional)", tokenEntry),
	)

	d := dialog.NewCustomConfirm("Add Monitored Folder", "Save", "Cancel", form, func(save bool) {
		if !save {
			return
		}

		folder := strings.TrimSpace(folderEntry.Text)
		apiUrl := strings.TrimSpace(urlEntry.Text)
		token := strings.TrimSpace(tokenEntry.Text)
		syncFolder := strings.TrimSpace(syncFolderEntry.Text)

		mode := modeSelect.Selected
		if mode == "" {
			mode = "upload"
		}

		if folder == "" {
			dialog.ShowError(fmt.Errorf("Folder Path is required"), parent)
			return
		}

		if mode == "upload" {
			if apiUrl == "" {
				dialog.ShowError(fmt.Errorf("API Upload URL is required for upload mode"), parent)
				return
			}
		} else {
			if syncFolder == "" {
				dialog.ShowError(fmt.Errorf("Sync Folder Name is required for sync mode"), parent)
				return
			}

			apiUrl = "/sync/" + syncFolder
		}

		_, err := db.Exec(`
			INSERT INTO watch_paths (
				folder_path,
				api_url,
				enabled,
				bearer_token,
				mode,
				sync_folder
			) VALUES (?, ?, 1, ?, ?, ?)
		`, folder, apiUrl, token, mode, syncFolder)
		if err != nil {
			dialog.ShowError(err, parent)
		} else {
			onSave()
		}
	}, parent)

	d.Resize(fyne.NewSize(520, 330))
	d.Show()
}

// showEditPathDialogSync adalah dialog Edit Folder yang mendukung mode upload dan sync.
func showEditPathDialogSync(parent fyne.Window, wp WatchPath, onSave func()) {
	folderEntry := widget.NewEntry()
	folderEntry.SetText(wp.FolderPath)

	urlEntry := widget.NewEntry()
	urlEntry.SetText(wp.ApiUrl)

	tokenEntry := widget.NewEntry()
	tokenEntry.SetText(wp.BearerToken)

	modeSelect := widget.NewSelect([]string{"upload", "sync_push", "sync_pull"}, nil)

	if wp.Mode == "" {
		modeSelect.SetSelected("upload")
	} else {
		modeSelect.SetSelected(wp.Mode)
	}

	syncFolderEntry := widget.NewEntry()
	syncFolderEntry.SetText(wp.SyncFolder)

	folderBox := container.NewBorder(nil, nil, nil, widget.NewButton("Browse", func() {
		dialog.ShowFolderOpen(func(list fyne.ListableURI, err error) {
			if err == nil && list != nil {
				folderEntry.SetText(list.Path())
			}
		}, parent)
	}), folderEntry)

	form := widget.NewForm(
		widget.NewFormItem("Folder Path", folderBox),
		widget.NewFormItem("Mode", modeSelect),
		widget.NewFormItem("API Upload URL", urlEntry),
		widget.NewFormItem("Sync Folder Name", syncFolderEntry),
		widget.NewFormItem("Bearer Token (Optional)", tokenEntry),
	)

	d := dialog.NewCustomConfirm("Edit Monitored Folder", "Update", "Cancel", form, func(save bool) {
		if !save {
			return
		}

		folder := strings.TrimSpace(folderEntry.Text)
		apiUrl := strings.TrimSpace(urlEntry.Text)
		token := strings.TrimSpace(tokenEntry.Text)
		syncFolder := strings.TrimSpace(syncFolderEntry.Text)

		mode := modeSelect.Selected
		if mode == "" {
			mode = "upload"
		}

		if folder == "" {
			dialog.ShowError(fmt.Errorf("Folder Path is required"), parent)
			return
		}

		if mode == "upload" {
			if apiUrl == "" {
				dialog.ShowError(fmt.Errorf("API Upload URL is required for upload mode"), parent)
				return
			}
		} else {
			if syncFolder == "" {
				dialog.ShowError(fmt.Errorf("Sync Folder Name is required for sync mode"), parent)
				return
			}

			apiUrl = "/sync/" + syncFolder
		}

		_, err := db.Exec(`
			UPDATE watch_paths
			SET
				folder_path = ?,
				api_url = ?,
				bearer_token = ?,
				mode = ?,
				sync_folder = ?
			WHERE id = ?
		`, folder, apiUrl, token, mode, syncFolder, wp.ID)
		if err != nil {
			dialog.ShowError(err, parent)
		} else {
			onSave()
		}
	}, parent)

	d.Resize(fyne.NewSize(520, 330))
	d.Show()
}

func syncServerBase() string {
	return strings.TrimSuffix(loadSetting("server_base", "http://localhost:8080"), "/")
}

func syncManifestURL(wp WatchPath) string {
	return fmt.Sprintf("%s/sync/%s/manifest", syncServerBase(), url.PathEscape(wp.SyncFolder))
}

func syncUploadURL(wp WatchPath) string {
	return fmt.Sprintf("%s/sync/%s/upload", syncServerBase(), url.PathEscape(wp.SyncFolder))
}

func syncDownloadURL(wp WatchPath, relPath string) string {
	return fmt.Sprintf(
		"%s/sync/%s/download?path=%s",
		syncServerBase(),
		url.PathEscape(wp.SyncFolder),
		url.QueryEscape(relPath),
	)
}

func setSyncAuth(req *http.Request, wp WatchPath) {
	if wp.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+wp.BearerToken)
	}

	req.Header.Set("X-Watcher-UUID", loadSetting("watcher_uuid", "WATCHER-UNKNOWN"))
}

func getSyncManifest(wp WatchPath) (map[string]SyncFileMeta, error) {
	if strings.TrimSpace(wp.SyncFolder) == "" {
		return nil, fmt.Errorf("sync folder is empty")
	}

	req, err := http.NewRequest(http.MethodGet, syncManifestURL(wp), nil)
	if err != nil {
		return nil, err
	}

	setSyncAuth(req, wp)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server status %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Success bool           `json:"success"`
		Files   []SyncFileMeta `json:"files"`
	}

	err = json.Unmarshal(body, &payload)
	if err != nil {
		return nil, err
	}

	if !payload.Success {
		return nil, fmt.Errorf("server rejected sync manifest")
	}

	result := make(map[string]SyncFileMeta)
	for _, f := range payload.Files {
		f.RelPath = filepath.ToSlash(f.RelPath)
		result[f.RelPath] = f
	}

	return result, nil
}

func scanLocalSyncFolder(root string) (map[string]SyncFileMeta, error) {
	result := make(map[string]SyncFileMeta)

	if _, err := os.Stat(root); err != nil {
		return result, err
	}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}

		if d.IsDir() {
			return nil
		}

		base := filepath.Base(path)

		// Skip file temporary yang sering dibuat oleh aplikasi Office.
		if strings.HasPrefix(base, "~$") {
			return nil
		}

		lowerBase := strings.ToLower(base)
		if strings.HasSuffix(lowerBase, ".tmp") || strings.HasSuffix(lowerBase, ".synctmp") {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		relSlash := filepath.ToSlash(rel)

		result[relSlash] = SyncFileMeta{
			RelPath: relSlash,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		}

		return nil
	})

	return result, err
}

func uploadSyncFile(wp WatchPath, absPath string, relPath string) error {
	if !waitFileStable(absPath) {
		return fmt.Errorf("file is still being written")
	}

	file, err := os.Open(absPath)
	if err != nil {
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", filepath.Base(absPath))
	if err != nil {
		return err
	}

	_, err = io.Copy(part, file)
	if err != nil {
		return err
	}

	_ = writer.WriteField("rel_path", filepath.ToSlash(relPath))
	_ = writer.WriteField("mod_time", info.ModTime().Format(time.RFC3339))

	err = writer.Close()
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, syncUploadURL(wp), body)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	setSyncAuth(req, wp)

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server status %d: %s", resp.StatusCode, string(respBody))
	}

	var res struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}

	err = json.Unmarshal(respBody, &res)
	if err != nil {
		return nil
	}

	if !res.Success {
		return fmt.Errorf("server rejected sync upload: %s", res.Message)
	}

	return nil
}

func downloadSyncFile(wp WatchPath, localRoot string, meta SyncFileMeta) error {
	relLocal := filepath.FromSlash(meta.RelPath)
	destPath := filepath.Join(localRoot, relLocal)

	_ = os.MkdirAll(filepath.Dir(destPath), 0755)

	req, err := http.NewRequest(http.MethodGet, syncDownloadURL(wp, meta.RelPath), nil)
	if err != nil {
		return err
	}

	setSyncAuth(req, wp)

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server status %d: %s", resp.StatusCode, string(body))
	}

	tmpPath := destPath + ".synctmp"

	out, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		out.Close()
		_ = os.Remove(tmpPath)
		return err
	}

	out.Close()

	_ = os.Chtimes(tmpPath, meta.ModTime, meta.ModTime)

	_ = os.Remove(destPath)

	err = os.Rename(tmpPath, destPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	return nil
}

func runSyncPush(wp WatchPath) {
	if wp.SyncFolder == "" {
		return
	}

	if _, err := os.Stat(wp.FolderPath); err != nil {
		addWatcherLog(fmt.Sprintf("Sync push folder not found: %s", wp.FolderPath))
		return
	}

	localFiles, err := scanLocalSyncFolder(wp.FolderPath)
	if err != nil {
		addWatcherLog(fmt.Sprintf("Sync push scan failed: %v", err))
		return
	}

	serverFiles, err := getSyncManifest(wp)
	if err != nil {
		addWatcherLog(fmt.Sprintf("Sync push manifest failed: %v", err))
		return
	}

	for rel, local := range localFiles {
		server, exists := serverFiles[rel]

		shouldUpload := false

		if !exists {
			shouldUpload = true
		} else if local.Size != server.Size {
			shouldUpload = true
		} else if local.ModTime.After(server.ModTime.Add(2 * time.Second)) {
			shouldUpload = true
		}

		if !shouldUpload {
			continue
		}

		absPath := filepath.Join(wp.FolderPath, filepath.FromSlash(rel))

		addWatcherLog(fmt.Sprintf("Sync push: %s", rel))

		err := uploadSyncFile(wp, absPath, rel)
		if err != nil {
			addWatcherLog(fmt.Sprintf("Sync push failed: %s: %v", rel, err))
		}
	}
}

func runSyncPull(wp WatchPath) {
	if wp.SyncFolder == "" {
		return
	}

	_ = os.MkdirAll(wp.FolderPath, 0755)

	serverFiles, err := getSyncManifest(wp)
	if err != nil {
		addWatcherLog(fmt.Sprintf("Sync pull manifest failed: %v", err))
		return
	}

	localFiles, err := scanLocalSyncFolder(wp.FolderPath)
	if err != nil {
		localFiles = make(map[string]SyncFileMeta)
	}

	for rel, server := range serverFiles {
		local, exists := localFiles[rel]

		shouldDownload := false

		if !exists {
			shouldDownload = true
		} else if server.Size != local.Size {
			shouldDownload = true
		} else if server.ModTime.After(local.ModTime.Add(2 * time.Second)) {
			shouldDownload = true
		}

		if !shouldDownload {
			continue
		}

		addWatcherLog(fmt.Sprintf("Sync pull: %s", rel))

		err := downloadSyncFile(wp, wp.FolderPath, server)
		if err != nil {
			addWatcherLog(fmt.Sprintf("Sync pull failed: %s: %v", rel, err))
		}
	}
}

func processSyncPushFile(absPath string, wp WatchPath) {
	key := "sync:" + absPath

	if _, loaded := uploadingFiles.LoadOrStore(key, true); loaded {
		return
	}
	defer uploadingFiles.Delete(key)

	rel, err := filepath.Rel(wp.FolderPath, absPath)
	if err != nil {
		return
	}

	relSlash := filepath.ToSlash(rel)

	err = uploadSyncFile(wp, absPath, relSlash)
	if err != nil {
		addWatcherLog(fmt.Sprintf("Sync push failed: %s: %v", relSlash, err))
	} else {
		addWatcherLog(fmt.Sprintf("Sync push success: %s", relSlash))
	}
}

func addWatchRecursive(w *fsnotify.Watcher, root string) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			if err := w.Add(path); err != nil {
				addWatcherLog(fmt.Sprintf("Error adding sync folder %s: %v", path, err))
			}
		}

		return nil
	})
}

func isUnderFolder(targetPath string, root string) bool {
	rel, err := filepath.Rel(root, targetPath)
	if err != nil {
		return false
	}

	rel = filepath.ToSlash(rel)

	if rel == "." {
		return true
	}

	if rel == ".." {
		return false
	}

	if strings.HasPrefix(rel, "../") {
		return false
	}

	return true
}

func findWatchPathForPath(activePaths []WatchPath, targetPath string) (WatchPath, bool) {
	for _, p := range activePaths {
		mode := p.Mode
		if mode == "" {
			mode = "upload"
		}

		if mode == "sync_push" {
			if isUnderFolder(targetPath, p.FolderPath) {
				return p, true
			}
		} else if mode == "upload" {
			if filepath.Clean(filepath.Dir(targetPath)) == filepath.Clean(p.FolderPath) {
				return p, true
			}
		}
	}

	return WatchPath{}, false
}
