# Analisis Struktur Ketergantungan main.go

Dokumen ini menjelaskan hubungan dan ketergantungan file `main.go` terhadap modul internal, pustaka eksternal, serta aset sistem berdasarkan analisis kode sumber.

## 1. Modul Internal (Project Local)
File ini terhubung langsung dengan paket internal proyek:

-   **`docuploader-filewatcher/internal/ui`**
    -   **Fungsi:** Menyediakan tema antarmuka kustom.
    -   **Penggunaan:** `ui.ModernTheme{}` digunakan pada `myApp.Settings().SetTheme()` untuk menerapkan gaya visual modern pada aplikasi Fyne.
-   **Database & Konfigurasi (Implisit)**
    -   Meskipun tidak ada import path database spesifik di blok `import`, fungsi-fungsi berikut mengindikasikan keberadaan file manajemen data lokal (kemungkinan SQLite):
        -   `initDB()`: Inisialisasi koneksi database.
        -   `loadSetting() / saveSetting()`: Manajemen konfigurasi persisten.
        -   `loadWatchPaths()`: Mengambil daftar folder yang dipantau dari tabel `watch_paths`.
        -   Query SQL Langsung: `db.Exec("UPDATE watch_paths...")`, `db.Exec("DELETE FROM watch_paths...")`.

## 2. Pustaka Eksternal (Third-Party Libraries)
Ketergantungan utama yang diambil melalui Go Modules:

| Package | Fungsi Utama dalam main.go |
| :--- | :--- |
| `fyne.io/fyne/v2` | Framework GUI utama (Window, App, Size, TextStyle). |
| `fyne.io/fyne/v2/app` | Membuat instance aplikasi baru (`app.New()`). |
| `fyne.io/fyne/v2/canvas` | Menggambar elemen grafis primitif (`canvas.NewCircle` untuk indikator status server). |
| `fyne.io/fyne/v2/container` | Layout management (Border, VBox, HBox, GridWrap, Scroll). |
| `fyne.io/fyne/v2/dialog` | Menampilkan popup interaktif (Confirm, Error, Information). |
| `fyne.io/fyne/v2/driver/desktop` | Fitur khusus desktop seperti System Tray Icon dan Menu. |
| `fyne.io/fyne/v2/widget` | Komponen UI interaktif (Table, Entry, Label, Button, Check, MultiLineEntry). |
| `github.com/fsnotify/fsnotify` | Inti pemantauan file sistem (File Watcher) untuk mendeteksi event `Create`. |

## 3. Standard Library Go
Paket bawaan yang digunakan untuk logika bisnis dan utilitas:

-   **`fmt`**: Formating string untuk log dan pesan dialog.
-   **`image/color`**: Mendefinisikan warna RGBA untuk indikator status (Merah/Hijau).
-   **`os`**: Interaksi sistem operasi (`os.Hostname`, `os.Stat`, `os.ReadDir`).
-   **`path/filepath`**: Manipulasi path file (`filepath.Join`, `filepath.Dir`, `filepath.Clean`) untuk mencocokkan event watcher dengan folder konfigurasi.
-   **`strings`**: Pembersihan input teks (`strings.TrimSpace`).

## 4. Aset & File Eksternal Runtime
File atau resource yang direferensikan secara dinamis saat runtime:

-   **`FileWatcher.png`**: Ikon untuk System Tray (dimuat via `fyne.NewStaticResource` dengan fallback ke `defaultIconBytes`).
-   **Database File**: File database fisik (biasanya `.db` atau `.sqlite`) yang diakses oleh variabel global `db`.
-   **Folder Target**: Direktori eksternal yang path-nya disimpan dalam database dan dipantau oleh `fsnotify`.

## 5. Arsitektur Singkat
# Pemetaan Struktur & Ketergantungan File (File Watcher)

Dokumen ini menjelaskan hubungan antara `main.go` dengan file-file lain dalam direktori proyek berdasarkan analisis kode sumber dan struktur folder.

## 📂 Struktur Direktori
```text
file-watcher/
├── data/               # Folder runtime (kemungkinan berisi database .db)
├── db.go               # Logika Database (Inisialisasi & Query)
├── file-watcher.exe    # Binary hasil kompilasi (Windows)
├── file-watcher.zip    # Arsip distribusi
├── FileWatcher.ico     # Ikon aplikasi (Windows Executable)
├── FileWatcher.png     # Aset ikon untuk System Tray (Runtime)
├── main.go             # Entry point & UI Utama
├── types.go            # Definisi Struct & Variabel Global
├── ui_helpers.go       # Fungsi pembantu UI (Dialog, Refresh)
└── watcher.go          # Logika bisnis inti (Upload, Heartbeat, Retry)