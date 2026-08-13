package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

var activityLogMu sync.Mutex

// addWatcherLog menulis activity log ke data/file-watcher.log
func addWatcherLog(msg string) {
	activityLogMu.Lock()
	defer activityLogMu.Unlock()

	_ = os.MkdirAll("data", 0755)

	f, err := os.OpenFile(
		filepath.Join("data", "file-watcher.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open file-watcher.log: %v\n", err)
		return
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	_, _ = f.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, msg))
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
