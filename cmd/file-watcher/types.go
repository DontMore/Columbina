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

	// upload       = fitur lama, upload lalu hapus file lokal
	// sync_push    = PC sumber, push folder ke server, tidak hapus lokal
	// sync_pull    = PC tujuan, tarik folder dari server
	Mode       string
	SyncFolder string
}

var (
	watcher     *fsnotify.Watcher
	watcherLock sync.Mutex
	isWatching  bool

	db       *sql.DB
	myWindow fyne.Window

	watchPaths      []WatchPath
	pathsTable      *widget.Table
	selectedPathRow = -1

	// Komponen status terpisah (aman dari isu render font lintas OS)
	serverStatusDot  *canvas.Circle
	serverStatusText *widget.Label

	// Maps files currently in-flight to prevent duplicate uploads
	uploadingFiles sync.Map
)
