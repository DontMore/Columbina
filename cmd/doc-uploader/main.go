package main

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	_ "github.com/mattn/go-sqlite3"

	"docuploader-filewatcher/internal/ui"
)

//go:embed DocUploader.png
var defaultIconBytes []byte

// Data Models
type Endpoint struct {
	ID                int
	Name              string
	Endpoint          string
	AllowedExtension  string
	DestinationFolder string
	MaxSizeMB         int
	Enabled           bool
	AuthToken         string
}

type WatcherInfo struct {
	UUID     string
	Name     string
	IP       string
	LastSeen time.Time
	Status   string
}

type WatcherPath struct {
	FolderPath string
	Endpoint   string
}

type LogEntry struct {
	ID         int
	Watcher    string
	Endpoint   string
	Filename   string
	Status     string
	UploadedAt string
}

var (
	server         *http.Server
	serverLock     sync.Mutex
	isRunning      bool
	logEntry       *widget.Entry
	db             *sql.DB
	myWindow       fyne.Window

	// Data stores
	endpoints    []Endpoint
	watchersList []WatcherInfo
	logsList     []LogEntry

	// UI components that need refreshing
	endpointsTable      *widget.Table
	watchersTable       *widget.Table
	logsTable           *widget.Table
	selectedEndpointRow = -1
	selectedWatcherRow  = -1

	// Watcher detail UI elements
	watcherDetailsLabel *widget.Label
	watcherPathsList    *widget.Entry
)

func initDB() {
	_ = os.MkdirAll("data", 0755)
	var err error
	db, err = sql.Open("sqlite3", "./data/uploader_settings.db")
	if err != nil {
		fmt.Printf("Error opening DB: %v\n", err)
		return
	}

	// Settings Table
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT)")
	if err != nil {
		fmt.Printf("Error creating settings table: %v\n", err)
	}

	// Upload Endpoints Table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS upload_endpoints (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT,
		endpoint TEXT UNIQUE,
		allowed_extension TEXT,
		destination_folder TEXT,
		max_size_mb INTEGER,
		enabled INTEGER DEFAULT 1,
		auth_token TEXT
	)`)
	if err != nil {
		fmt.Printf("Error creating upload_endpoints table: %v\n", err)
	}

	// Watchers Table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS watchers (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		watcher_uuid TEXT UNIQUE,
		watcher_name TEXT,
		ip_address TEXT,
		last_seen DATETIME,
		status TEXT
	)`)
	if err != nil {
		fmt.Printf("Error creating watchers table: %v\n", err)
	}

	// Watcher Paths Table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS watcher_paths (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		watcher_uuid TEXT,
		folder_path TEXT,
		endpoint TEXT,
		UNIQUE(watcher_uuid, folder_path, endpoint)
	)`)
	if err != nil {
		fmt.Printf("Error creating watcher_paths table: %v\n", err)
	}

	// Upload Logs Table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS upload_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		watcher_uuid TEXT,
		endpoint TEXT,
		filename TEXT,
		status TEXT,
		uploaded_at DATETIME
	)`)
	if err != nil {
		fmt.Printf("Error creating upload_logs table: %v\n", err)
	}
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

func addLog(msg string) {
	timestamp := time.Now().Format("15:04:05")
	if logEntry != nil {
		logEntry.SetText(logEntry.Text + fmt.Sprintf("[%s] %s\n", timestamp, msg))
		logEntry.CursorColumn = 0
		logEntry.CursorRow = len(logEntry.Text)
	}
}

func loadEndpoints() {
	endpoints = nil
	rows, err := db.Query("SELECT id, name, endpoint, allowed_extension, destination_folder, max_size_mb, enabled, auth_token FROM upload_endpoints ORDER BY endpoint ASC")
	if err != nil {
		fmt.Printf("Error querying endpoints: %v\n", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var ep Endpoint
		var enabledInt int
		var allowedExt, authToken sql.NullString
		err := rows.Scan(&ep.ID, &ep.Name, &ep.Endpoint, &allowedExt, &ep.DestinationFolder, &ep.MaxSizeMB, &enabledInt, &authToken)
		if err != nil {
			continue
		}
		ep.AllowedExtension = allowedExt.String
		ep.AuthToken = authToken.String
		ep.Enabled = enabledInt == 1
		endpoints = append(endpoints, ep)
	}
}

func loadWatchers() {
	watchersList = nil
	rows, err := db.Query("SELECT watcher_uuid, watcher_name, ip_address, last_seen, status FROM watchers ORDER BY last_seen DESC")
	if err != nil {
		fmt.Printf("Error querying watchers: %v\n", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var w WatcherInfo
		var lastSeenStr string
		err := rows.Scan(&w.UUID, &w.Name, &w.IP, &lastSeenStr, &w.Status)
		if err != nil {
			continue
		}
		// Parsing SQLite datetimes
		t, err := time.Parse("2006-01-02T15:04:05Z07:00", lastSeenStr)
		if err != nil {
			t, err = time.Parse("2006-01-02 15:04:05.999999999-07:00", lastSeenStr)
		}
		if err != nil {
			t, err = time.Parse("2006-01-02 15:04:05", lastSeenStr)
		}
		if err == nil {
			w.LastSeen = t
		}
		watchersList = append(watchersList, w)
	}
}

func loadWatcherPaths(uuid string) []WatcherPath {
	var paths []WatcherPath
	rows, err := db.Query("SELECT folder_path, endpoint FROM watcher_paths WHERE watcher_uuid = ?", uuid)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var wp WatcherPath
			rows.Scan(&wp.FolderPath, &wp.Endpoint)
			paths = append(paths, wp)
		}
	}
	return paths
}

func loadLogs() {
	logsList = nil
	rows, err := db.Query("SELECT id, watcher_uuid, endpoint, filename, status, uploaded_at FROM upload_logs ORDER BY uploaded_at DESC LIMIT 150")
	if err != nil {
		fmt.Printf("Error querying logs: %v\n", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var log LogEntry
		var uploadedAtStr string
		err := rows.Scan(&log.ID, &log.Watcher, &log.Endpoint, &log.Filename, &log.Status, &uploadedAtStr)
		if err != nil {
			continue
		}
		t, err := time.Parse("2006-01-02T15:04:05Z07:00", uploadedAtStr)
		if err != nil {
			t, err = time.Parse("2006-01-02 15:04:05.999999999-07:00", uploadedAtStr)
		}
		if err != nil {
			t, err = time.Parse("2006-01-02 15:04:05", uploadedAtStr)
		}
		if err == nil {
			log.UploadedAt = t.Format("2006-01-02 15:04:05")
		} else {
			log.UploadedAt = uploadedAtStr
		}
		logsList = append(logsList, log)
	}
}

func logUploadAttempt(watcherUUID, endpoint, filename, status string, t time.Time) {
	_, err := db.Exec("INSERT INTO upload_logs (watcher_uuid, endpoint, filename, status, uploaded_at) VALUES (?, ?, ?, ?, ?)",
		watcherUUID, endpoint, filename, status, t)
	if err != nil {
		fmt.Printf("Error logging upload: %v\n", err)
	}
}

func startWatcherOfflineMonitor() {
	go func() {
		for {
			time.Sleep(15 * time.Second)
			serverLock.Lock()
			running := isRunning
			serverLock.Unlock()

			if running {
				cutoff := time.Now().Add(-75 * time.Second)
				_, err := db.Exec("UPDATE watchers SET status = 'Offline' WHERE last_seen < ? AND status = 'Online'", cutoff)
				if err != nil {
					fmt.Printf("Error checking offline watchers: %v\n", err)
				}
			}
		}
	}()
}

func handleUploadCentral(w http.ResponseWriter, r *http.Request, ep Endpoint) {
	w.Header().Set("Content-Type", "application/json")

	watcherUUID := r.Header.Get("X-Watcher-UUID")
	if watcherUUID == "" {
		watcherUUID = "Direct/Manual"
	}

	// Validate Auth
	if ep.AuthToken != "" {
		authHeader := r.Header.Get("Authorization")
		expectedAuth := "Bearer " + ep.AuthToken
		if authHeader != expectedAuth {
			w.WriteHeader(http.StatusUnauthorized)
			logUploadAttempt(watcherUUID, ep.Endpoint, "N/A", "Rejected: Unauthorized Bearer Token", time.Now())
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "Unauthorized Bearer Token"})
			return
		}
	}

	// Max upload size limit (maxSizeMB)
	r.Body = http.MaxBytesReader(w, r.Body, int64(ep.MaxSizeMB)<<20)
	err := r.ParseMultipartForm(int64(ep.MaxSizeMB) << 20)
	if err != nil {
		logUploadAttempt(watcherUUID, ep.Endpoint, "N/A", "Rejected: File size exceeds limit", time.Now())
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": fmt.Sprintf("File size exceeds limit of %d MB", ep.MaxSizeMB)})
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		addLog(fmt.Sprintf("Upload error retrieving file: %v", err))
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "Error retrieving the file form payload"})
		return
	}
	defer file.Close()

	filename := handler.Filename
	addLog(fmt.Sprintf("[%s] Incoming file to %s: %s (%d bytes)", watcherUUID, ep.Endpoint, filename, handler.Size))

	// File extension check
	ext := strings.ToLower(filepath.Ext(filename))
	allowed := false
	if ep.AllowedExtension == "" || ep.AllowedExtension == "*" {
		allowed = true
	} else {
		parts := strings.Split(ep.AllowedExtension, ",")
		for _, p := range parts {
			trimmed := strings.TrimSpace(strings.ToLower(p))
			if !strings.HasPrefix(trimmed, ".") {
				trimmed = "." + trimmed
			}
			if trimmed == ext {
				allowed = true
				break
			}
		}
	}

	if !allowed {
		logUploadAttempt(watcherUUID, ep.Endpoint, filename, fmt.Sprintf("Rejected: extension %s not allowed", ext), time.Now())
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "Extension not allowed"})
		return
	}

	// MIME type check
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	file.Seek(0, io.SeekStart)
	detectedMime := http.DetectContentType(buf[:n])
	
	if strings.Contains(detectedMime, "application/x-dosexec") || strings.Contains(detectedMime, "application/x-msdownload") {
		logUploadAttempt(watcherUUID, ep.Endpoint, filename, "Rejected: Executable MIME blocked", time.Now())
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "Executable files are blocked for security reasons"})
		return
	}

	// Create directory if not exists
	if _, err := os.Stat(ep.DestinationFolder); os.IsNotExist(err) {
		err := os.MkdirAll(ep.DestinationFolder, 0755)
		if err != nil {
			logUploadAttempt(watcherUUID, ep.Endpoint, filename, "Failed: Directory creation error", time.Now())
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "Failed to create destination directory"})
			return
		}
	}

	// Save original filename directly, overwriting existing
	destPath := filepath.Join(ep.DestinationFolder, filename)

	dst, err := os.Create(destPath)
	if err != nil {
		logUploadAttempt(watcherUUID, ep.Endpoint, filename, "Failed: File creation error", time.Now())
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "Failed to save file on server"})
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		logUploadAttempt(watcherUUID, ep.Endpoint, filename, "Failed: Copy error", time.Now())
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "Error copying file contents"})
		return
	}

	addLog(fmt.Sprintf("[%s] Saved file: %s", watcherUUID, filename))
	logUploadAttempt(watcherUUID, ep.Endpoint, filename, "Success", time.Now())

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Uploaded successfully"})
}

func handleRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
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
	err := json.NewDecoder(r.Body).Decode(&payload)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "Invalid register payload"})
		return
	}

	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}

	_, err = db.Exec(`INSERT INTO watchers (watcher_uuid, watcher_name, ip_address, last_seen, status) 
		VALUES (?, ?, ?, ?, 'Online') 
		ON CONFLICT(watcher_uuid) DO UPDATE SET 
			watcher_name=excluded.watcher_name, 
			ip_address=excluded.ip_address, 
			last_seen=excluded.last_seen, 
			status='Online'`,
		payload.WatcherUUID, payload.WatcherName, ip, time.Now())
	if err != nil {
		fmt.Printf("Error inserting/updating watcher: %v\n", err)
	}

	_, err = db.Exec("DELETE FROM watcher_paths WHERE watcher_uuid = ?", payload.WatcherUUID)
	if err != nil {
		fmt.Printf("Error clearing old watcher paths: %v\n", err)
	}

	for _, p := range payload.Paths {
		_, err = db.Exec("INSERT OR IGNORE INTO watcher_paths (watcher_uuid, folder_path, endpoint) VALUES (?, ?, ?)",
			payload.WatcherUUID, p.Folder, p.Endpoint)
		if err != nil {
			fmt.Printf("Error inserting watcher path: %v\n", err)
		}
	}

	addLog(fmt.Sprintf("Watcher registered: %s (%s) with %d paths", payload.WatcherName, payload.WatcherUUID, len(payload.Paths)))
	
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	type HeartbeatPayload struct {
		WatcherUUID string `json:"watcher_uuid"`
	}

	var payload HeartbeatPayload
	err := json.NewDecoder(r.Body).Decode(&payload)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "Invalid heartbeat payload"})
		return
	}

	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}

	var name string
	err = db.QueryRow("SELECT watcher_name FROM watchers WHERE watcher_uuid = ?", payload.WatcherUUID).Scan(&name)
	if err != nil {
		name = "Unknown Watcher"
	}

	_, err = db.Exec(`INSERT INTO watchers (watcher_uuid, watcher_name, ip_address, last_seen, status) 
		VALUES (?, ?, ?, ?, 'Online') 
		ON CONFLICT(watcher_uuid) DO UPDATE SET 
			ip_address=excluded.ip_address, 
			last_seen=excluded.last_seen, 
			status='Online'`,
		payload.WatcherUUID, name, ip, time.Now())
	if err != nil {
		fmt.Printf("Error updating heartbeat: %v\n", err)
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func refreshEndpointsTable() {
	loadEndpoints()
	endpointsTable.Refresh()
}

func refreshWatchersTable() {
	loadWatchers()
	watchersTable.Refresh()
	if selectedWatcherRow == -1 {
		watcherDetailsLabel.SetText("Select a watcher to view details")
		watcherPathsList.SetText("")
	}
}

func refreshLogsTable() {
	loadLogs()
	logsTable.Refresh()
}

func showAddEndpointDialog(parent fyne.Window, onSave func()) {
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("e.g. PEBJF Uploads")
	
	endpointEntry := widget.NewEntry()
	endpointEntry.SetPlaceHolder("e.g. /upload/pebjf")
	
	extEntry := widget.NewEntry()
	extEntry.SetPlaceHolder("e.g. .pdf,.txt (comma-separated or empty for all)")
	
	folderEntry := widget.NewEntry()
	folderEntry.SetPlaceHolder("Path to save uploaded files")
	
	sizeEntry := widget.NewEntry()
	sizeEntry.SetText("10")
	sizeEntry.SetPlaceHolder("e.g. 10")
	
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
		widget.NewFormItem("Name", nameEntry),
		widget.NewFormItem("Endpoint", endpointEntry),
		widget.NewFormItem("Allowed Extensions", extEntry),
		widget.NewFormItem("Destination Folder", folderBox),
		widget.NewFormItem("Max Size (MB)", sizeEntry),
		widget.NewFormItem("Bearer Token (Optional)", tokenEntry),
	)

	d := dialog.NewCustomConfirm("Add New Endpoint", "Save", "Cancel", form, func(save bool) {
		if !save {
			return
		}
		
		epStr := strings.TrimSpace(endpointEntry.Text)
		folderStr := strings.TrimSpace(folderEntry.Text)
		
		if epStr == "" || folderStr == "" {
			dialog.ShowError(fmt.Errorf("Endpoint and Destination Folder are required fields"), parent)
			return
		}
		if !strings.HasPrefix(epStr, "/") {
			epStr = "/" + epStr
		}
		
		var maxMB int
		fmt.Sscanf(sizeEntry.Text, "%d", &maxMB)
		if maxMB <= 0 {
			maxMB = 10
		}

		_, err := db.Exec(`INSERT INTO upload_endpoints (name, endpoint, allowed_extension, destination_folder, max_size_mb, enabled, auth_token) 
			VALUES (?, ?, ?, ?, ?, 1, ?)`,
			nameEntry.Text, epStr, extEntry.Text, folderStr, maxMB, tokenEntry.Text)
		if err != nil {
			dialog.ShowError(err, parent)
		} else {
			onSave()
		}
	}, parent)
	
	d.Resize(fyne.NewSize(500, 380))
	d.Show()
}

func showEditEndpointDialog(parent fyne.Window, ep Endpoint, onSave func()) {
	nameEntry := widget.NewEntry()
	nameEntry.SetText(ep.Name)
	
	endpointEntry := widget.NewEntry()
	endpointEntry.SetText(ep.Endpoint)
	
	extEntry := widget.NewEntry()
	extEntry.SetText(ep.AllowedExtension)
	
	folderEntry := widget.NewEntry()
	folderEntry.SetText(ep.DestinationFolder)
	
	sizeEntry := widget.NewEntry()
	sizeEntry.SetText(fmt.Sprintf("%d", ep.MaxSizeMB))
	
	tokenEntry := widget.NewEntry()
	tokenEntry.SetText(ep.AuthToken)

	folderBox := container.NewBorder(nil, nil, nil, widget.NewButton("Browse", func() {
		dialog.ShowFolderOpen(func(list fyne.ListableURI, err error) {
			if err == nil && list != nil {
				folderEntry.SetText(list.Path())
			}
		}, parent)
	}), folderEntry)

	form := widget.NewForm(
		widget.NewFormItem("Name", nameEntry),
		widget.NewFormItem("Endpoint", endpointEntry),
		widget.NewFormItem("Allowed Extensions", extEntry),
		widget.NewFormItem("Destination Folder", folderBox),
		widget.NewFormItem("Max Size (MB)", sizeEntry),
		widget.NewFormItem("Bearer Token (Optional)", tokenEntry),
	)

	d := dialog.NewCustomConfirm("Edit Endpoint", "Update", "Cancel", form, func(save bool) {
		if !save {
			return
		}
		
		epStr := strings.TrimSpace(endpointEntry.Text)
		folderStr := strings.TrimSpace(folderEntry.Text)
		
		if epStr == "" || folderStr == "" {
			dialog.ShowError(fmt.Errorf("Endpoint and Destination Folder are required fields"), parent)
			return
		}
		if !strings.HasPrefix(epStr, "/") {
			epStr = "/" + epStr
		}
		
		var maxMB int
		fmt.Sscanf(sizeEntry.Text, "%d", &maxMB)
		if maxMB <= 0 {
			maxMB = 10
		}

		_, err := db.Exec(`UPDATE upload_endpoints SET name=?, endpoint=?, allowed_extension=?, destination_folder=?, max_size_mb=?, auth_token=? WHERE id=?`,
			nameEntry.Text, epStr, extEntry.Text, folderStr, maxMB, tokenEntry.Text, ep.ID)
		if err != nil {
			dialog.ShowError(err, parent)
		} else {
			onSave()
		}
	}, parent)
	
	d.Resize(fyne.NewSize(500, 380))
	d.Show()
}

func main() {
	initDB()
	startWatcherOfflineMonitor()

	myApp := app.New()
	myApp.Settings().SetTheme(ui.ModernTheme{})
	myWindow = myApp.NewWindow("Central Upload Server & Dashboard")
	myWindow.Resize(fyne.NewSize(850, 550))

	// Global Server Control UI Elements
	portEntry := widget.NewEntry()
	portEntry.SetText(loadSetting("port", "8080"))
	portEntry.SetPlaceHolder("Port (e.g. 8080)")

	statusLabel := widget.NewLabel("🔴 Server Stopped")

	// Pre-load data
	loadEndpoints()
	loadWatchers()
	loadLogs()

	// ---------------- TAB 1: Endpoint Manager ----------------
	endpointsTable = widget.NewTable(
		func() (int, int) {
			return len(endpoints) + 1, 6 // 6 columns
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("Cell Placeholder text")
		},
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label := cell.(*widget.Label)
			if id.Row == 0 {
				label.TextStyle = fyne.TextStyle{Bold: true}
				switch id.Col {
				case 0:
					label.SetText("Endpoint")
				case 1:
					label.SetText("Name")
				case 2:
					label.SetText("Extensions")
				case 3:
					label.SetText("Max Size")
				case 4:
					label.SetText("Destination Folder")
				case 5:
					label.SetText("Status")
				}
				return
			}
			label.TextStyle = fyne.TextStyle{}
			if id.Row-1 >= len(endpoints) {
				return
			}
			ep := endpoints[id.Row-1]
			switch id.Col {
			case 0:
				label.SetText(ep.Endpoint)
			case 1:
				label.SetText(ep.Name)
			case 2:
				if ep.AllowedExtension == "" {
					label.SetText("Any (*)")
				} else {
					label.SetText(ep.AllowedExtension)
				}
			case 3:
				label.SetText(fmt.Sprintf("%d MB", ep.MaxSizeMB))
			case 4:
				label.SetText(ep.DestinationFolder)
			case 5:
				if ep.Enabled {
					label.SetText("Enabled")
				} else {
					label.SetText("Disabled")
				}
			}
		},
	)
	endpointsTable.SetColumnWidth(0, 110) // Endpoint
	endpointsTable.SetColumnWidth(1, 130) // Name
	endpointsTable.SetColumnWidth(2, 100) // Ext
	endpointsTable.SetColumnWidth(3, 80)  // Size
	endpointsTable.SetColumnWidth(4, 250) // Destination Folder
	endpointsTable.SetColumnWidth(5, 80)  // Status

	endpointsTable.OnSelected = func(id widget.TableCellID) {
		if id.Row > 0 {
			selectedEndpointRow = id.Row - 1
		}
	}
	endpointsTable.OnUnselected = func(id widget.TableCellID) {
		selectedEndpointRow = -1
	}

	addBtn := widget.NewButton("Add Endpoint", func() {
		showAddEndpointDialog(myWindow, refreshEndpointsTable)
	})

	editBtn := widget.NewButton("Edit Selected", func() {
		if selectedEndpointRow < 0 || selectedEndpointRow >= len(endpoints) {
			dialog.ShowInformation("Notification", "Please select an endpoint row from the table first", myWindow)
			return
		}
		showEditEndpointDialog(myWindow, endpoints[selectedEndpointRow], refreshEndpointsTable)
	})

	toggleBtn := widget.NewButton("Toggle Status", func() {
		if selectedEndpointRow < 0 || selectedEndpointRow >= len(endpoints) {
			dialog.ShowInformation("Notification", "Please select an endpoint row from the table first", myWindow)
			return
		}
		ep := endpoints[selectedEndpointRow]
		newEnabled := 1
		if ep.Enabled {
			newEnabled = 0
		}
		_, err := db.Exec("UPDATE upload_endpoints SET enabled = ? WHERE id = ?", newEnabled, ep.ID)
		if err != nil {
			dialog.ShowError(err, myWindow)
		} else {
			refreshEndpointsTable()
		}
	})

	deleteBtn := widget.NewButton("Delete Selected", func() {
		if selectedEndpointRow < 0 || selectedEndpointRow >= len(endpoints) {
			dialog.ShowInformation("Notification", "Please select an endpoint row from the table first", myWindow)
			return
		}
		ep := endpoints[selectedEndpointRow]
		dialog.ShowConfirm("Delete Endpoint", fmt.Sprintf("Are you sure you want to delete endpoint '%s'?", ep.Endpoint), func(confirm bool) {
			if confirm {
				_, err := db.Exec("DELETE FROM upload_endpoints WHERE id = ?", ep.ID)
				if err != nil {
					dialog.ShowError(err, myWindow)
				} else {
					selectedEndpointRow = -1
					refreshEndpointsTable()
				}
			}
		}, myWindow)
	})

	endpointControls := container.NewHBox(addBtn, editBtn, toggleBtn, deleteBtn)
	endpointsContainer := container.NewBorder(nil, endpointControls, nil, nil, endpointsTable)

	// ---------------- TAB 2: Watcher Monitor ----------------
	watchersTable = widget.NewTable(
		func() (int, int) {
			return len(watchersList) + 1, 4
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("Watcher Table Cell text placeholder")
		},
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label := cell.(*widget.Label)
			if id.Row == 0 {
				label.TextStyle = fyne.TextStyle{Bold: true}
				switch id.Col {
				case 0:
					label.SetText("Watcher Name")
				case 1:
					label.SetText("IP Address")
				case 2:
					label.SetText("Status")
				case 3:
					label.SetText("Last Seen")
				}
				return
			}
			label.TextStyle = fyne.TextStyle{}
			if id.Row-1 >= len(watchersList) {
				return
			}
			w := watchersList[id.Row-1]
			switch id.Col {
			case 0:
				label.SetText(w.Name)
			case 1:
				label.SetText(w.IP)
			case 2:
				label.SetText(w.Status)
			case 3:
				if w.LastSeen.IsZero() {
					label.SetText("Never")
				} else {
					label.SetText(w.LastSeen.Format("2006-01-02 15:04:05"))
				}
			}
		},
	)
	watchersTable.SetColumnWidth(0, 150) // Name
	watchersTable.SetColumnWidth(1, 110) // IP
	watchersTable.SetColumnWidth(2, 90)  // Status
	watchersTable.SetColumnWidth(3, 150) // Last Seen

	watcherDetailsLabel = widget.NewLabel("Select a watcher to view details")
	watcherDetailsLabel.TextStyle = fyne.TextStyle{Bold: true}
	watcherPathsList = widget.NewMultiLineEntry()
	watcherPathsList.Disable()

	watchersTable.OnSelected = func(id widget.TableCellID) {
		if id.Row > 0 && id.Row-1 < len(watchersList) {
			selectedWatcherRow = id.Row - 1
			w := watchersList[selectedWatcherRow]

			paths := loadWatcherPaths(w.UUID)
			var pathLines []string
			for _, p := range paths {
				pathLines = append(pathLines, fmt.Sprintf("• Folder: %s\n  Endpoint: %s\n", p.FolderPath, p.Endpoint))
			}

			watcherDetailsLabel.SetText(fmt.Sprintf("Name: %s\nUUID: %s\nIP: %s\nStatus: %s\nLast Seen: %s",
				w.Name, w.UUID, w.IP, w.Status, w.LastSeen.Format("2006-01-02 15:04:05")))

			if len(pathLines) == 0 {
				watcherPathsList.SetText("No monitored paths registered.")
			} else {
				watcherPathsList.SetText(strings.Join(pathLines, "\n"))
			}
		}
	}

	watchersTable.OnUnselected = func(id widget.TableCellID) {
		selectedWatcherRow = -1
		watcherDetailsLabel.SetText("Select a watcher to view details")
		watcherPathsList.SetText("")
	}

	watcherRightPanel := container.NewBorder(
		watcherDetailsLabel,
		nil, nil, nil,
		container.NewBorder(widget.NewLabel("Registered Monitored Paths:"), nil, nil, nil, watcherPathsList),
	)

	watcherSplit := container.NewHSplit(watchersTable, watcherRightPanel)
	watcherSplit.Offset = 0.55

	// ---------------- TAB 3: Upload Logs ----------------
	logsTable = widget.NewTable(
		func() (int, int) {
			return len(logsList) + 1, 5
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("Logs Table Cell text placeholder")
		},
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label := cell.(*widget.Label)
			if id.Row == 0 {
				label.TextStyle = fyne.TextStyle{Bold: true}
				switch id.Col {
				case 0:
					label.SetText("Timestamp")
				case 1:
					label.SetText("Filename")
				case 2:
					label.SetText("Watcher UUID")
				case 3:
					label.SetText("Endpoint")
				case 4:
					label.SetText("Status")
				}
				return
			}
			label.TextStyle = fyne.TextStyle{}
			if id.Row-1 >= len(logsList) {
				return
			}
			log := logsList[id.Row-1]
			switch id.Col {
			case 0:
				label.SetText(log.UploadedAt)
			case 1:
				label.SetText(log.Filename)
			case 2:
				label.SetText(log.Watcher)
			case 3:
				label.SetText(log.Endpoint)
			case 4:
				label.SetText(log.Status)
			}
		},
	)
	logsTable.SetColumnWidth(0, 140) // Timestamp
	logsTable.SetColumnWidth(1, 200) // Filename
	logsTable.SetColumnWidth(2, 130) // Watcher
	logsTable.SetColumnWidth(3, 110) // Endpoint
	logsTable.SetColumnWidth(4, 200) // Status

	logsControls := container.NewHBox(
		widget.NewButton("Clear Logs History", func() {
			dialog.ShowConfirm("Clear Logs", "Are you sure you want to clear ALL upload logs from the database?", func(confirm bool) {
				if confirm {
					_, err := db.Exec("DELETE FROM upload_logs")
					if err != nil {
						dialog.ShowError(err, myWindow)
					} else {
						refreshLogsTable()
					}
				}
			}, myWindow)
		}),
	)
	logsContainer := container.NewBorder(nil, logsControls, nil, nil, logsTable)

	// ---------------- TAB 4: System Logs ----------------
	logEntry = widget.NewMultiLineEntry()
	logEntry.Disable()
	logEntry.Wrapping = fyne.TextWrapBreak
	scrollSystemLogs := container.NewScroll(logEntry)

	systemLogsContainer := container.NewBorder(
		nil,
		widget.NewButton("Clear Console Logs", func() {
			logEntry.SetText("")
		}),
		nil, nil,
		scrollSystemLogs,
	)

	// Assemble Tabs
	tabs := container.NewAppTabs(
		container.NewTabItem("Endpoint Manager", endpointsContainer),
		container.NewTabItem("Watcher Monitor", watcherSplit),
		container.NewTabItem("Upload Logs", logsContainer),
		container.NewTabItem("System Logs", systemLogsContainer),
	)

	// Server Start/Stop Logic
	var startStopBtn *widget.Button
	startStopBtn = widget.NewButton("Start Server", func() {
		serverLock.Lock()
		defer serverLock.Unlock()

		if isRunning {
			// Stop Server
			if server != nil {
				addLog("Stopping HTTP server...")
				server.Close()
				isRunning = false
				statusLabel.SetText("🔴 Server Stopped")
				startStopBtn.SetText("Start Server")
				portEntry.Enable()
			}
		} else {
			// Start Server
			port := portEntry.Text
			if port == "" {
				dialog.ShowInformation("Error", "Server port is required", myWindow)
				return
			}

			// Save port setting
			saveSetting("port", port)

			server = &http.Server{
				Addr: ":" + port,
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					path := strings.TrimSuffix(r.URL.Path, "/")
					
					// Handle central API registration and heartbeats
					if path == "/watcher/register" {
						handleRegister(w, r)
						return
					}
					if path == "/watcher/heartbeat" {
						handleHeartbeat(w, r)
						return
					}

					// Dynamic endpoints lookup in DB
					var ep Endpoint
					var enabledInt int
					var allowedExt, authToken sql.NullString
					err := db.QueryRow("SELECT id, name, endpoint, allowed_extension, destination_folder, max_size_mb, enabled, auth_token FROM upload_endpoints WHERE endpoint = ?", path).
						Scan(&ep.ID, &ep.Name, &ep.Endpoint, &allowedExt, &ep.DestinationFolder, &ep.MaxSizeMB, &enabledInt, &authToken)
					
					if err != nil {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusNotFound)
						json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "API endpoint not found"})
						return
					}

					ep.AllowedExtension = allowedExt.String
					ep.AuthToken = authToken.String
					ep.Enabled = enabledInt == 1

					if !ep.Enabled {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusForbidden)
						json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "This endpoint is disabled"})
						return
					}

					// Process multipart file upload
					handleUploadCentral(w, r, ep)
				}),
			}

			go func() {
				addLog(fmt.Sprintf("Server launching on port %s...", port))
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					addLog(fmt.Sprintf("HTTP Server error: %v", err))
					serverLock.Lock()
					isRunning = false
					statusLabel.SetText("⚠️ Server Error / Stopped")
					startStopBtn.SetText("Start Server")
					portEntry.Enable()
					serverLock.Unlock()
				}
			}()

			isRunning = true
			statusLabel.SetText("🟢 Server Running")
			startStopBtn.SetText("Stop Server")
			portEntry.Disable()
		}
	})

	// Background UI Refresh loop for watchers and logs
	go func() {
		for {
			time.Sleep(3 * time.Second)
			serverLock.Lock()
			running := isRunning
			serverLock.Unlock()

			if running {
				refreshWatchersTable()
				refreshLogsTable()
			}
		}
	}()

	topLayout := container.NewVBox(
		widget.NewLabelWithStyle("Central Document Ingestion Server", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		container.NewGridWithColumns(3,
			container.NewBorder(nil, nil, widget.NewLabel("Port:"), nil, portEntry),
			startStopBtn,
			statusLabel,
		),
	)

	finalLayout := container.NewBorder(
		topLayout,
		nil, nil, nil,
		tabs,
	)

	myWindow.SetContent(finalLayout)

	// Close interception for System Tray (minimize to tray)
	myWindow.SetCloseIntercept(func() {
		myWindow.Hide()
		addLog("Dashboard hidden to System Tray. Restore from system tray menu.")
	})

	// Setup System Tray Icon & Menu
	if desk, ok := myApp.(desktop.App); ok {
		icon := fyne.NewStaticResource("DocUploader.png", defaultIconBytes)
		desk.SetSystemTrayIcon(icon)

		menu := fyne.NewMenu("DocUploader Server",
			fyne.NewMenuItem("Open Dashboard", func() {
				myWindow.Show()
				myWindow.RequestFocus()
			}),
			fyne.NewMenuItem("Exit Server", func() {
				myApp.Quit()
			}),
		)
		desk.SetSystemTrayMenu(menu)
	}

	myWindow.ShowAndRun()
}
