package main

import (
	"database/sql"
	_ "embed"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
	"github.com/fsnotify/fsnotify"
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

	// Komponen status terpisah (Aman dari isu render font lintas OS)
	serverStatusDot  *canvas.Circle
	serverStatusText *widget.Label

	// Maps files currently in-flight to prevent duplicate uploads
	uploadingFiles sync.Map

	// Log Toggle Settings
	logEnabled   bool
	logEnabledMu sync.RWMutex
)
