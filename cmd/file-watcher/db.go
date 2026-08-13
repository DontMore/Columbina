package main

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// initDB membuka database FileWatcher dan membuat tabel yang dibutuhkan.
func initDB() {
	_ = os.MkdirAll("data", 0755)

	var err error
	db, err = sql.Open("sqlite3", "./data/filewatcher_settings.db")
	if err != nil {
		fmt.Printf("Error opening DB: %v\n", err)
		return
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT
		)
	`)
	if err != nil {
		fmt.Printf("Error creating settings table: %v\n", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS watch_paths (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			folder_path TEXT,
			api_url TEXT,
			enabled INTEGER DEFAULT 1,
			bearer_token TEXT DEFAULT '',
			mode TEXT DEFAULT 'upload',
			sync_folder TEXT DEFAULT ''
		)
	`)
	if err != nil {
		fmt.Printf("Error creating watch_paths table: %v\n", err)
	}

	ensureWatchPathSyncColumns()

	// Buat watcher UUID otomatis jika belum ada.
	if loadSetting("watcher_uuid", "") == "" {
		saveSetting("watcher_uuid", generateWatcherUUID())
	}
}

// generateWatcherUUID membuat UUID sederhana berdasarkan hostname dan timestamp.
func generateWatcherUUID() string {
	host, err := os.Hostname()
	if err != nil {
		host = "WATCHER"
	}

	return fmt.Sprintf("%s-%d", host, time.Now().Unix())
}

// ensureWatchPathSyncColumns menambahkan kolom sync ke tabel lama jika belum ada.
func ensureWatchPathSyncColumns() {
	if db == nil {
		return
	}

	// Error diabaikan karena jika kolom sudah ada, ALTER TABLE akan gagal.
	_, _ = db.Exec("ALTER TABLE watch_paths ADD COLUMN bearer_token TEXT DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE watch_paths ADD COLUMN mode TEXT DEFAULT 'upload'")
	_, _ = db.Exec("ALTER TABLE watch_paths ADD COLUMN sync_folder TEXT DEFAULT ''")
}

func saveSetting(key, value string) {
	if db == nil {
		return
	}

	_, err := db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", key, value)
	if err != nil {
		fmt.Printf("Error saving setting %s: %v\n", key, err)
	}
}

func loadSetting(key, defaultValue string) string {
	if db == nil {
		return defaultValue
	}

	var value string
	err := db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err != nil {
		return defaultValue
	}

	return value
}

// loadWatchPaths membaca semua folder yang dipantau, termasuk mode sync.
func loadWatchPaths() {
	watchPaths = nil

	if db == nil {
		return
	}

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
		ORDER BY id ASC
	`)
	if err != nil {
		fmt.Printf("Error loading watch paths: %v\n", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var wp WatchPath
		var enabledInt int

		err := rows.Scan(
			&wp.ID,
			&wp.FolderPath,
			&wp.ApiUrl,
			&enabledInt,
			&wp.BearerToken,
			&wp.Mode,
			&wp.SyncFolder,
		)
		if err != nil {
			continue
		}

		wp.Enabled = enabledInt == 1

		if wp.Mode == "" {
			wp.Mode = "upload"
		}

		watchPaths = append(watchPaths, wp)
	}
}
