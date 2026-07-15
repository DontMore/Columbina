package main

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"github.com/fsnotify/fsnotify"

	"docuploader-filewatcher/internal/ui"
)

func main() {
	initDB()

	logEnabled = loadSetting("enable_log", "true") == "true"

	loadWatchPaths()
	startHeartbeatLoop()
	startRetryLoop()

	myApp := app.New()
	myApp.Settings().SetTheme(ui.ModernTheme{})
	myWindow = myApp.NewWindow("File Watcher Client")
	myWindow.Resize(fyne.NewSize(720, 560))

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
	pathsTable.SetColumnWidth(0, 260)
	pathsTable.SetColumnWidth(1, 280)
	pathsTable.SetColumnWidth(2, 90)

	pathsTable.OnSelected = func(id widget.TableCellID) {
		if id.Row > 0 {
			selectedPathRow = id.Row - 1
		}
	}
	pathsTable.OnUnselected = func(id widget.TableCellID) {
		selectedPathRow = -1
	}

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

	// Inisialisasi awal lingkaran Vektor Grafis (Default Merah)
	serverStatusDot = canvas.NewCircle(color.NRGBA{R: 231, G: 76, B: 60, A: 255})

	// Membungkus lingkaran ke dalam GridWrap container untuk memaksakan ukurannya menjadi 14x14 piksel
	dotContainer := container.NewGridWrap(fyne.NewSize(14, 14), serverStatusDot)

	// Inisialisasi teks keterangan status
	serverStatusText = widget.NewLabel("Server Not Connected")

	// Menyatukan lingkaran grafis (yang disejajarkan ke tengah) dan teks keterangan status
	serverStatusBox := container.NewHBox(container.NewCenter(dotContainer), serverStatusText)

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

	watcherLog = widget.NewMultiLineEntry()
	watcherLog.Disable()
	watcherLog.Wrapping = fyne.TextWrapBreak
	scrollLogs := container.NewScroll(watcherLog)
	scrollLogs.SetMinSize(fyne.NewSize(0, 160))

	statusLabel := widget.NewLabel("🔴 Watcher Stopped")

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

	var startStopBtn *widget.Button
	startStopBtn = widget.NewButton("Start Watching", func() {
		watcherLock.Lock()
		defer watcherLock.Unlock()

		if isWatching {
			if watcher != nil {
				addWatcherLog("Stopping watcher core...")
				watcher.Close()
				isWatching = false
				statusLabel.SetText("🔴 Watcher Stopped")

				// Reset indikator warna menjadi merah saat core dimatikan secara manual
				serverStatusDot.FillColor = color.NRGBA{R: 231, G: 76, B: 60, A: 255}
				serverStatusText.SetText("Server Not Connected")
				serverStatusDot.Refresh()
				serverStatusText.Refresh()

				startStopBtn.SetText("Start Watching")
				serverBaseEntry.Enable()
				watcherNameEntry.Enable()
				addBtn.Enable()
				editBtn.Enable()
				toggleBtn.Enable()
				deleteBtn.Enable()
			}
		} else {
			serverBase := strings.TrimSpace(serverBaseEntry.Text)
			watcherName := strings.TrimSpace(watcherNameEntry.Text)

			if serverBase == "" {
				dialog.ShowInformation("Error", "Server Base URL is required to start watching", myWindow)
				return
			}

			saveSetting("server_base", serverBase)
			saveSetting("watcher_name", watcherName)

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
					addWatcherLog(fmt.Sprintf("Error adding folder %s: %v", wp.FolderPath, err))
				} else {
					addWatcherLog(fmt.Sprintf("Monitoring: %s", wp.FolderPath))
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

			go registerWatcher()
		}
	})

	topSettings := container.NewVBox(
		widget.NewLabelWithStyle("File Watcher & Central Ingestion Client", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		container.NewGridWithColumns(3,
			container.NewBorder(nil, nil, widget.NewLabel("Server Base:"), nil, serverBaseEntry),
			container.NewBorder(nil, nil, widget.NewLabel("Client Name:"), nil, watcherNameEntry),
			container.NewVBox(uuidLabel, serverStatusBox),
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
			logHeader,
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

	myWindow.SetCloseIntercept(func() {
		myWindow.Hide()
		addWatcherLog("Watcher hidden to System Tray. Restore from system tray menu.")
	})

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
