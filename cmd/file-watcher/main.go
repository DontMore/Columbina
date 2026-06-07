package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"github.com/fsnotify/fsnotify"
	_ "github.com/mattn/go-sqlite3"

	"docuploader-filewatcher/internal/ui"
)

//go:embed FileWatcher.png
var defaultIconBytes []byte

type WatchPath struct {
	ID          int
	FolderPath  string
	ApiUrl      string
	Enabled     bool
	BearerToken string
}

var (
	watcher         *fsnotify.Watcher
	watcherLock     sync.Mutex
	isWatching      bool
	watcherLog      *widget.Entry
	db              *sql.DB
	myWindow        fyne.Window
	watchPaths      []WatchPath
	pathsTable      *widget.Table
	selectedPathRow = -1

	// Maps files currently in-flight to prevent duplicate uploads
	uploadingFiles sync.Map

	// Log Toggle Settings
	logEnabled   bool
	logEnabledMu sync.RWMutex
)

func initDB() {
	_ = os.MkdirAll("data", 0755)
	var err error
	db, err = sql.Open("sqlite3", "./data/watcher_settings.db")
	if err != nil {
		fmt.Printf("Error opening DB: %v\n", err)
		return
	}
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT)")
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS watch_paths ( id INTEGER PRIMARY KEY AUTOINCREMENT, folder_path TEXT UNIQUE, api_url TEXT, enabled INTEGER DEFAULT 1, bearer_token TEXT )`)
	if err != nil {
		fmt.Printf("Error creating tables: %v\n", err)
	}
	// Persist/Generate persistent WATCHER-XXXXX UUID if not exists
	uuid := loadSetting("watcher_uuid", "")
	if uuid == "" {
		uuid = generateUUID()
		saveSetting("watcher_uuid", uuid)
	}
}

func generateUUID() string {
	b := make([]byte, 3)
	_, err := rand.Read(b)
	if err != nil {
		return fmt.Sprintf("WATCHER-%d", time.Now().UnixNano()%100000)
	}
	return fmt.Sprintf("WATCHER-%05X", b)
}

func saveSetting(key, value string) {
	_, err := db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", key, value)
	if err != nil {
		fmt.Printf("Error saving setting %s: %v\n", key, err)
	}
}

func loadSetting(key, defaultValue string) string {
	var value string
	err := db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err != nil {
		return defaultValue
	}
	return value
}

func loadWatchPaths() {
	watchPaths = nil
	rows, err := db.Query("SELECT id, folder_path, api_url, enabled, bearer_token FROM watch_paths ORDER BY folder_path ASC")
	if err != nil {
		fmt.Printf("Error querying watch paths: %v\n", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var wp WatchPath
		var enabledInt int
		var token sql.NullString
		err := rows.Scan(&wp.ID, &wp.FolderPath, &wp.ApiUrl, &enabledInt, &token)
		if err != nil {
			continue
		}
		wp.Enabled = enabledInt == 1
		wp.BearerToken = token.String
		watchPaths = append(watchPaths, wp)
	}
}

func addWatcherLog(msg string) {
	// Cek apakah logging diaktifkan (thread-safe)
	logEnabledMu.RLock()
	enabled := logEnabled
	logEnabledMu.RUnlock()

	if !enabled {
		return // Lewati update UI jika logging dimatikan
	}

	timestamp := time.Now().Format("15:04:05")
	if watcherLog != nil {
		// Update UI dengan aman dari goroutine mana pun untuk mencegah race condition/panic pada Fyne
		fyne.CurrentApp().Driver().DoFromGoroutine(func() {
			watcherLog.SetText(watcherLog.Text + fmt.Sprintf("[%s] %s\n", timestamp, msg))
			watcherLog.CursorColumn = 0
			watcherLog.CursorRow = len(watcherLog.Text)
		}, false)
	}
}

func refreshPathsTable() {
	loadWatchPaths()
	pathsTable.Refresh()
}

func showAddPathDialog(parent fyne.Window, onSave func()) {
	folderEntry := widget.NewEntry()
	folderEntry.SetPlaceHolder("Local folder path to monitor")
	urlEntry := widget.NewEntry()
	urlEntry.SetPlaceHolder("e.g. http://localhost:8080/upload/pebjf")

	tokenEntry := widget.NewEntry()
	tokenEntry.SetPlaceHolder("Optional Bearer Token")

	folderBox := container.NewBorder(nil, nil, nil, widget.NewButton("Browse", func() {
		dialog.ShowFolderOpen(func(list fyne.ListableURI, err error) {
			if err == nil && list != nil {
				folderEntry.SetText(list.Path())
			}
		}, parent)
	}), folderEntry)

	form := widget.NewForm(
		widget.NewFormItem("Folder Path", folderBox),
		widget.NewFormItem("API Upload URL", urlEntry),
		widget.NewFormItem("Bearer Token (Optional)", tokenEntry),
	)

	d := dialog.NewCustomConfirm("Add Monitored Folder", "Save", "Cancel", form, func(save bool) {
		if !save {
			return
		}

		folder := strings.TrimSpace(folderEntry.Text)
		apiUrl := strings.TrimSpace(urlEntry.Text)

		if folder == "" || apiUrl == "" {
			dialog.ShowError(fmt.Errorf("Both Folder Path and API URL are required"), parent)
			return
		}

		_, err := db.Exec("INSERT INTO watch_paths (folder_path, api_url, enabled, bearer_token) VALUES (?, ?, 1, ?)",
			folder, apiUrl, tokenEntry.Text)
		if err != nil {
			dialog.ShowError(err, parent)
		} else {
			onSave()
		}
	}, parent)

	d.Resize(fyne.NewSize(500, 260))
	d.Show()
}

func showEditPathDialog(parent fyne.Window, wp WatchPath, onSave func()) {
	folderEntry := widget.NewEntry()
	folderEntry.SetText(wp.FolderPath)
	urlEntry := widget.NewEntry()
	urlEntry.SetText(wp.ApiUrl)

	tokenEntry := widget.NewEntry()
	tokenEntry.SetText(wp.BearerToken)

	folderBox := container.NewBorder(nil, nil, nil, widget.NewButton("Browse", func() {
		dialog.ShowFolderOpen(func(list fyne.ListableURI, err error) {
			if err == nil && list != nil {
				folderEntry.SetText(list.Path())
			}
		}, parent)
	}), folderEntry)

	form := widget.NewForm(
		widget.NewFormItem("Folder Path", folderBox),
		widget.NewFormItem("API Upload URL", urlEntry),
		widget.NewFormItem("Bearer Token (Optional)", tokenEntry),
	)

	d := dialog.NewCustomConfirm("Edit Monitored Folder", "Update", "Cancel", form, func(save bool) {
		if !save {
			return
		}

		folder := strings.TrimSpace(folderEntry.Text)
		apiUrl := strings.TrimSpace(urlEntry.Text)

		if folder == "" || apiUrl == "" {
			dialog.ShowError(fmt.Errorf("Both Folder Path and API URL are required"), parent)
			return
		}

		_, err := db.Exec("UPDATE watch_paths SET folder_path = ?, api_url = ?, bearer_token = ? WHERE id = ?",
			folder, apiUrl, tokenEntry.Text, wp.ID)
		if err != nil {
			dialog.ShowError(err, parent)
		} else {
			onSave()
		}
	}, parent)

	d.Resize(fyne.NewSize(500, 260))
	d.Show()
}

// File stability logic
func waitFileStable(filePath string) bool {
	var lastSize int64 = -1
	for i := 0; i < 10; i++ { // Check for up to 5 seconds
		info, err := os.Stat(filePath)
		if err != nil {
			return false
		}
		currentSize := info.Size()
		if currentSize == lastSize && currentSize > 0 {
			// Size is stable. Check if we can open it (not currently locked by another process)
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
		// Non-JSON backup success
		return nil
	}

	if !res.Success {
		return fmt.Errorf("server rejected upload: %s", res.Message)
	}

	return nil
}

func processFileUpload(filePath string, apiUrl string, token string) {
	if _, loaded := uploadingFiles.LoadOrStore(filePath, true); loaded {
		// File already uploading
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
		return
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		addWatcherLog("Successfully registered watcher configuration on Central Server.")
	} else {
		addWatcherLog(fmt.Sprintf("Central Server registration failed with code: %d", resp.StatusCode))
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
					continue
				}
				req.Header.Set("Content-Type", "application/json")

				client := &http.Client{Timeout: 10 * time.Second}
				resp, err := client.Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}
		}
	}()
}

// Background retry loop: Scans monitored paths for unsaved files
func startRetryLoop() {
	go func() {
		for {
			time.Sleep(30 * time.Second)
			watcherLock.Lock()
			watching := isWatching
			watcherLock.Unlock()

			if watching {
				var activePaths []WatchPath
				rows, err := db.Query("SELECT id, folder_path, api_url, enabled, bearer_token FROM watch_paths WHERE enabled = 1")
				if err == nil {
					for rows.Next() {
						var wp WatchPath
						var enabledInt int
						var token sql.NullString
						rows.Scan(&wp.ID, &wp.FolderPath, &wp.ApiUrl, &enabledInt, &token)
						wp.Enabled = enabledInt == 1
						wp.BearerToken = token.String
						activePaths = append(activePaths, wp)
					}
					rows.Close()
				}

				for _, wp := range activePaths {
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

func main() {
	initDB()

	// Muat pengaturan toggle log (default: true/aktif)
	logEnabled = loadSetting("enable_log", "true") == "true"

	loadWatchPaths()
	startHeartbeatLoop()
	startRetryLoop()

	myApp := app.New()
	myApp.Settings().SetTheme(ui.ModernTheme{})
	myWindow = myApp.NewWindow("File Watcher Client")
	myWindow.Resize(fyne.NewSize(700, 520))

	// Monitored Paths table
	pathsTable = widget.NewTable(
		func() (int, int) {
			return len(watchPaths) + 1, 3
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("Monitored Folder Path Column placeholder text")
		},
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label := cell.(*widget.Label)
			if id.Row == 0 {
				label.TextStyle = fyne.TextStyle{Bold: true}
				switch id.Col {
				case 0:
					label.SetText("Folder Path")
				case 1:
					label.SetText("API Upload URL")
				case 2:
					label.SetText("Status")
				}
				return
			}
			label.TextStyle = fyne.TextStyle{}
			if id.Row-1 >= len(watchPaths) {
				return
			}
			wp := watchPaths[id.Row-1]
			switch id.Col {
			case 0:
				label.SetText(wp.FolderPath)
			case 1:
				label.SetText(wp.ApiUrl)
			case 2:
				if wp.Enabled {
					label.SetText("Enabled")
				} else {
					label.SetText("Disabled")
				}
			}
		},
	)
	pathsTable.SetColumnWidth(0, 260) // Path
	pathsTable.SetColumnWidth(1, 280) // URL
	pathsTable.SetColumnWidth(2, 90)  // Status

	pathsTable.OnSelected = func(id widget.TableCellID) {
		if id.Row > 0 {
			selectedPathRow = id.Row - 1
		}
	}
	pathsTable.OnUnselected = func(id widget.TableCellID) {
		selectedPathRow = -1
	}

	// Settings section
	serverBaseEntry := widget.NewEntry()
	serverBaseEntry.SetText(loadSetting("server_base", "http://localhost:8080"))
	serverBaseEntry.SetPlaceHolder("Server Base URL (e.g. http://localhost:8080)")

	hostNameDefault := "LAB-PC-01"
	if h, err := os.Hostname(); err == nil {
		hostNameDefault = h
	}
	watcherNameEntry := widget.NewEntry()
	watcherNameEntry.SetText(loadSetting("watcher_name", hostNameDefault))
	watcherNameEntry.SetPlaceHolder("Watcher Name")

	uuidLabel := widget.NewLabel("Watcher UUID: " + loadSetting("watcher_uuid", "N/A"))

	// Folder controls
	addBtn := widget.NewButton("Add Folder", func() {
		showAddPathDialog(myWindow, refreshPathsTable)
	})

	editBtn := widget.NewButton("Edit Selected", func() {
		if selectedPathRow < 0 || selectedPathRow >= len(watchPaths) {
			dialog.ShowInformation("Notification", "Please select a folder row from the table first", myWindow)
			return
		}
		showEditPathDialog(myWindow, watchPaths[selectedPathRow], refreshPathsTable)
	})

	toggleBtn := widget.NewButton("Toggle Status", func() {
		if selectedPathRow < 0 || selectedPathRow >= len(watchPaths) {
			dialog.ShowInformation("Notification", "Please select a folder row from the table first", myWindow)
			return
		}
		wp := watchPaths[selectedPathRow]
		newVal := 1
		if wp.Enabled {
			newVal = 0
		}
		_, err := db.Exec("UPDATE watch_paths SET enabled = ? WHERE id = ?", newVal, wp.ID)
		if err != nil {
			dialog.ShowError(err, myWindow)
		} else {
			refreshPathsTable()
		}
	})

	deleteBtn := widget.NewButton("Delete Selected", func() {
		if selectedPathRow < 0 || selectedPathRow >= len(watchPaths) {
			dialog.ShowInformation("Notification", "Please select a folder row from the table first", myWindow)
			return
		}
		wp := watchPaths[selectedPathRow]
		dialog.ShowConfirm("Delete Folder", fmt.Sprintf("Are you sure you want to delete '%s'?", wp.FolderPath), func(confirm bool) {
			if confirm {
				_, err := db.Exec("DELETE FROM watch_paths WHERE id = ?", wp.ID)
				if err != nil {
					dialog.ShowError(err, myWindow)
				} else {
					selectedPathRow = -1
					refreshPathsTable()
				}
			}
		}, myWindow)
	})

	folderControls := container.NewHBox(addBtn, editBtn, toggleBtn, deleteBtn)

	// Logging pane
	watcherLog = widget.NewMultiLineEntry()
	watcherLog.Disable()
	watcherLog.Wrapping = fyne.TextWrapBreak
	scrollLogs := container.NewScroll(watcherLog)
	scrollLogs.SetMinSize(fyne.NewSize(0, 160))

	statusLabel := widget.NewLabel("🔴 Watcher Stopped")

	// --- TOMBOL TOGGLE LOG & CLEAR ---
	enableLogCheck := widget.NewCheck("Enable Activity Logs", func(checked bool) {
		logEnabledMu.Lock()
		logEnabled = checked
		logEnabledMu.Unlock()
		saveSetting("enable_log", fmt.Sprintf("%t", checked))

		if checked {
			addWatcherLog("Activity logging has been enabled.")
		}
	})
	enableLogCheck.SetChecked(logEnabled)

	clearLogBtn := widget.NewButton("Clear", func() {
		if watcherLog != nil {
			watcherLog.SetText("")
		}
	})

	logHeader := container.NewHBox(
		widget.NewLabel("Activity Logs:"),
		enableLogCheck,
		clearLogBtn,
	)

	// Watch Start/Stop Logic
	var startStopBtn *widget.Button
	startStopBtn = widget.NewButton("Start Watching", func() {
		watcherLock.Lock()
		defer watcherLock.Unlock()

		if isWatching {
			// Stop Watching
			if watcher != nil {
				addWatcherLog("Stopping watcher core...")
				watcher.Close()
				isWatching = false
				statusLabel.SetText("🔴 Watcher Stopped")
				startStopBtn.SetText("Start Watching")
				serverBaseEntry.Enable()
				watcherNameEntry.Enable()
				addBtn.Enable()
				editBtn.Enable()
				toggleBtn.Enable()
				deleteBtn.Enable()
			}
		} else {
			// Start Watching
			serverBase := strings.TrimSpace(serverBaseEntry.Text)
			watcherName := strings.TrimSpace(watcherNameEntry.Text)

			if serverBase == "" {
				dialog.ShowInformation("Error", "Server Base URL is required to start watching", myWindow)
				return
			}

			saveSetting("server_base", serverBase)
			saveSetting("watcher_name", watcherName)

			// Load watch paths
			loadWatchPaths()
			var activePaths []WatchPath
			for _, wp := range watchPaths {
				if wp.Enabled {
					activePaths = append(activePaths, wp)
				}
			}

			if len(activePaths) == 0 {
				dialog.ShowInformation("Error", "At least one enabled folder is required to start monitoring", myWindow)
				return
			}

			var err error
			watcher, err = fsnotify.NewWatcher()
			if err != nil {
				dialog.ShowError(err, myWindow)
				return
			}

			addWatcherLog("Starting watcher core...")

			for _, wp := range activePaths {
				err = watcher.Add(wp.FolderPath)
				if err != nil {
					// PERBAIKAN: Menggunakan wp.FolderPath, bukan wp.FAdapterPath
					addWatcherLog(fmt.Sprintf("Error adding folder %s: %v", wp.FolderPath, err))
				} else {
					addWatcherLog(fmt.Sprintf("Monitoring: %s", wp.FolderPath))

					// Scan existing files and upload
					files, err := os.ReadDir(wp.FolderPath)
					if err == nil {
						for _, f := range files {
							if !f.IsDir() {
								filePath := filepath.Join(wp.FolderPath, f.Name())
								go processFileUpload(filePath, wp.ApiUrl, wp.BearerToken)
							}
						}
					}
				}
			}

			// Watcher Event Router Loop
			go func() {
				for {
					select {
					case event, ok := <-watcher.Events:
						if !ok {
							return
						}
						if event.Op&fsnotify.Create == fsnotify.Create {
							info, err := os.Stat(event.Name)
							if err == nil && !info.IsDir() {
								filePath := event.Name
								dirPath := filepath.Dir(filePath)

								var wp WatchPath
								var found bool
								for _, p := range activePaths {
									if filepath.Clean(p.FolderPath) == filepath.Clean(dirPath) {
										wp = p
										found = true
										break
									}
								}

								if found {
									addWatcherLog(fmt.Sprintf("File detected: %s", filepath.Base(filePath)))
									go processFileUpload(filePath, wp.ApiUrl, wp.BearerToken)
								}
							}
						}
					case err, ok := <-watcher.Errors:
						if !ok {
							return
						}
						addWatcherLog(fmt.Sprintf("Watcher core error: %v", err))
					}
				}
			}()

			isWatching = true
			statusLabel.SetText("🟢 Monitoring Active...")
			startStopBtn.SetText("Stop Watching")
			serverBaseEntry.Disable()
			watcherNameEntry.Disable()
			addBtn.Disable()
			editBtn.Disable()
			toggleBtn.Disable()
			deleteBtn.Disable()

			// Register on server
			go registerWatcher()
		}
	})

	topSettings := container.NewVBox(
		widget.NewLabelWithStyle("File Watcher & Central Ingestion Client", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		container.NewGridWithColumns(3,
			container.NewBorder(nil, nil, widget.NewLabel("Server Base:"), nil, serverBaseEntry),
			container.NewBorder(nil, nil, widget.NewLabel("Client Name:"), nil, watcherNameEntry),
			uuidLabel,
		),
	)

	bodyLayout := container.NewBorder(
		container.NewVBox(
			widget.NewLabel("Monitored Folders:"),
			folderControls,
		),
		container.NewVBox(
			startStopBtn,
			statusLabel,
			logHeader, // <-- TOMBOL TOGGLE LOG ADA DI SINI
			scrollLogs,
		),
		nil, nil,
		pathsTable,
	)

	finalLayout := container.NewBorder(
		topSettings,
		nil, nil, nil,
		bodyLayout,
	)

	myWindow.SetContent(finalLayout)

	// Close interception for System Tray (minimize to tray)
	myWindow.SetCloseIntercept(func() {
		myWindow.Hide()
		addWatcherLog("Watcher hidden to System Tray. Restore from system tray menu.")
	})

	// Setup System Tray Icon & Menu
	if desk, ok := myApp.(desktop.App); ok {
		icon := fyne.NewStaticResource("FileWatcher.png", defaultIconBytes)
		desk.SetSystemTrayIcon(icon)

		menu := fyne.NewMenu("FileWatcher Client",
			fyne.NewMenuItem("Open Watcher", func() {
				myWindow.Show()
				myWindow.RequestFocus()
			}),
			fyne.NewMenuItem("Exit Watcher", func() {
				myApp.Quit()
			}),
		)
		desk.SetSystemTrayMenu(menu)
	}

	myWindow.ShowAndRun()
}
