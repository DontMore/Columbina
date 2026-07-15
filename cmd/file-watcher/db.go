package main

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
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
