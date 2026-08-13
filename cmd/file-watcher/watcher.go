package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
)

func waitFileStable(filePath string) bool {
	var lastSize int64 = -1
	for i := 0; i < 10; i++ {
		info, err := os.Stat(filePath)
		if err != nil {
			return false
		}
		currentSize := info.Size()
		if currentSize == lastSize && currentSize > 0 {
			file, err := os.OpenFile(filePath, os.O_RDWR, 0)
			if err == nil {
				file.Close()
				return true
			}
		}
		lastSize = currentSize
		time.Sleep(500 * time.Millisecond)
	}
	return true
}

func uploadFile(filePath string, apiUrl string, token string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return err
	}
	_, err = io.Copy(part, file)
	if err != nil {
		return err
	}
	writer.Close()

	req, err := http.NewRequest("POST", apiUrl, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Watcher-UUID", loadSetting("watcher_uuid", "WATCHER-UNKNOWN"))

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server status %d: %s", resp.StatusCode, string(respBody))
	}

	type SuccessResponse struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}

	var res SuccessResponse
	err = json.Unmarshal(respBody, &res)
	if err != nil {
		return nil
	}

	if !res.Success {
		return fmt.Errorf("server rejected upload: %s", res.Message)
	}

	return nil
}

func processFileUpload(filePath string, apiUrl string, token string) {
	if _, loaded := uploadingFiles.LoadOrStore(filePath, true); loaded {
		return
	}
	defer uploadingFiles.Delete(filePath)
	if !waitFileStable(filePath) {
		return
	}

	addWatcherLog(fmt.Sprintf("Uploading: %s", filepath.Base(filePath)))
	err := uploadFile(filePath, apiUrl, token)
	if err != nil {
		addWatcherLog(fmt.Sprintf("Failed to upload %s: %v", filepath.Base(filePath), err))
		return
	}

	addWatcherLog(fmt.Sprintf("Successfully uploaded %s. Deleting local file...", filepath.Base(filePath)))

	err = os.Remove(filePath)
	if err != nil {
		addWatcherLog(fmt.Sprintf("Error deleting local file %s: %v", filepath.Base(filePath), err))
	} else {
		addWatcherLog(fmt.Sprintf("Deleted local file: %s", filepath.Base(filePath)))
	}
}

func registerWatcher() {
	serverBase := loadSetting("server_base", "http://localhost:8080")
	if serverBase == "" {
		return
	}
	type PathPayload struct {
		Folder   string `json:"folder"`
		Endpoint string `json:"endpoint"`
	}
	type RegisterPayload struct {
		WatcherUUID string        `json:"watcher_uuid"`
		WatcherName string        `json:"watcher_name"`
		Paths       []PathPayload `json:"paths"`
	}

	var payload RegisterPayload
	payload.WatcherUUID = loadSetting("watcher_uuid", "WATCHER-UNKNOWN")

	watcherName := loadSetting("watcher_name", "")
	if watcherName == "" {
		h, err := os.Hostname()
		if err == nil {
			watcherName = h
		} else {
			watcherName = "LAB-PC-01"
		}
	}
	payload.WatcherName = watcherName

	rows, err := db.Query("SELECT folder_path, api_url FROM watch_paths WHERE enabled = 1")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var folderPath, apiUrl string
			rows.Scan(&folderPath, &apiUrl)

			endpoint := apiUrl
			u, err := url.Parse(apiUrl)
			if err == nil && u.Path != "" {
				endpoint = u.Path
			}
			payload.Paths = append(payload.Paths, PathPayload{
				Folder:   folderPath,
				Endpoint: endpoint,
			})
		}
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return
	}

	urlStr := fmt.Sprintf("%s/watcher/register", strings.TrimSuffix(serverBase, "/"))
	req, err := http.NewRequest("POST", urlStr, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		addWatcherLog(fmt.Sprintf("Watcher registration failed: %v", err))
		updateServerStatus(false)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		addWatcherLog("Successfully registered watcher configuration on Central Server.")
		updateServerStatus(true)
	} else {
		addWatcherLog(fmt.Sprintf("Central Server registration failed with code: %d", resp.StatusCode))
		updateServerStatus(false)
	}
}

func startHeartbeatLoop() {
	go func() {
		for {
			time.Sleep(30 * time.Second)
			watcherLock.Lock()
			watching := isWatching
			watcherLock.Unlock()

			if watching {
				serverBase := loadSetting("server_base", "http://localhost:8080")
				if serverBase == "" {
					continue
				}

				type HeartbeatPayload struct {
					WatcherUUID string `json:"watcher_uuid"`
				}

				var payload HeartbeatPayload
				payload.WatcherUUID = loadSetting("watcher_uuid", "WATCHER-UNKNOWN")

				bodyBytes, _ := json.Marshal(payload)
				urlStr := fmt.Sprintf("%s/watcher/heartbeat", strings.TrimSuffix(serverBase, "/"))

				req, err := http.NewRequest("POST", urlStr, bytes.NewBuffer(bodyBytes))
				if err != nil {
					updateServerStatus(false)
					continue
				}
				req.Header.Set("Content-Type", "application/json")

				client := &http.Client{Timeout: 10 * time.Second}
				resp, err := client.Do(req)
				if err == nil {
					if resp.StatusCode == http.StatusOK {
						updateServerStatus(true)
					} else {
						updateServerStatus(false)
					}
					resp.Body.Close()
				} else {
					updateServerStatus(false)
				}
			}
		}
	}()
}

// Pembaruan status UI secara aman dan menggunakan warna solid vektor
func updateServerStatus(connected bool) {
	if serverStatusDot == nil || serverStatusText == nil {
		return
	}
	fyne.CurrentApp().Driver().DoFromGoroutine(func() {
		if connected {
			serverStatusDot.FillColor = color.NRGBA{R: 46, G: 204, B: 113, A: 255} // Hijau
			serverStatusText.SetText("Server Connected")
		} else {
			serverStatusDot.FillColor = color.NRGBA{R: 231, G: 76, B: 60, A: 255} // Merah
			serverStatusText.SetText("Server Not Connected")
		}
		serverStatusDot.Refresh()
		serverStatusText.Refresh()
	}, false)
}

func startRetryLoop() {
	go func() {
		for {
			time.Sleep(30 * time.Second)

			watcherLock.Lock()
			watching := isWatching
			watcherLock.Unlock()

			if !watching {
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
