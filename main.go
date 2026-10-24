// Command cardtridge is a Windows tray app: scan a QR code with your phone
// or a scanner, tap an NFC card or plug in a USB drive, and it launches the
// Steam, GOG or Epic game on this PC.
package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	appName = "Cardtridge"
	// productName is the name people see, with its trademark sign.
	productName = "Cardtridge™"
	runKey      = `Software\Microsoft\Windows\CurrentVersion\Run`
	runName     = "Cardtridge"
	// The app was called Steam QR Launcher, in steam-launcher folders.
	oldRunName = "SteamQRLauncher"
	oldDirName = "steam-launcher"
)

func main() {
	finishUpdate()
	// After finishUpdate, so a version handing over has let go of its folders.
	moveErr := moveOldDirs()
	setupLogging()
	log.Printf("starting %s %s", appName, version)
	if moveErr != nil {
		log.Printf("moving steam-launcher folders: %v; using them where they are", moveErr)
	}

	// The old name's mutex too, so this and a Steam QR Launcher don't both run.
	for _, mutex := range []string{`Local\Cardtridge`, `Local\SteamQRLauncher`} {
		name, _ := windows.UTF16PtrFromString(mutex)
		if _, err := windows.CreateMutex(nil, false, name); errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			fatal(productName + " is already running. Look for it in the system tray.")
		}
	}
	moveOldStartup()

	cfg, err := loadConfig()
	if err != nil {
		fatal("Couldn't read config: " + err.Error())
	}
	key, err := loadKey()
	if err != nil {
		fatal("Couldn't create key: " + err.Error())
	}
	launcher := NewLauncher()
	if err := launcher.Rescan(); err != nil {
		fatal("Couldn't read the Steam, GOG or Epic library: " + err.Error())
	}

	ln, err := net.Listen("tcp", ":"+strconv.Itoa(cfg.Port))
	if err != nil {
		fatal(fmt.Sprintf("Couldn't listen on port %d: %v", cfg.Port, err))
	}
	host := cfg.HostIP
	if host == "" {
		host = lanIP()
	}
	details := LoadDetails()
	go details.FetchMissing(launcher.Apps())
	var app *application.App
	srv := &Server{launcher: launcher, key: key, base: fmt.Sprintf("http://%s:%d", host, cfg.Port), details: details, artChoices: LoadArtChoices(), print: LoadPrintPrefs(), cfg: cfg, quit: func() { app.Quit() }}
	if !strings.EqualFold(cfg.NFCBase, "off") {
		cards, err := LoadCards()
		if err != nil {
			fatal("Couldn't read cards.json: " + err.Error())
		}
		srv.nfc = NewNFCManager(launcher, cards, nil)
	}
	go func() { log.Fatal(http.Serve(ln, srv.Handler())) }()
	if !strings.EqualFold(cfg.DriveLaunch, "off") {
		go watchDrives(launcher)
	}
	log.Printf("serving on :%d, codes point at %s", cfg.Port, srv.base)

	app = application.New(application.Options{
		Name:        appName,
		Description: "Launch your games by scanning a QR code or NFC card",
		Assets:      application.AssetOptions{Handler: http.NotFoundHandler()},
		Windows:     application.WindowsOptions{WebviewUserDataPath: filepath.Join(logDir(), "webview")},
	})
	pageURL := fmt.Sprintf("http://localhost:%d", cfg.Port)
	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            productName,
		Width:            1280,
		Height:           900,
		MinWidth:         640,
		MinHeight:        480,
		Hidden:           true,
		BackgroundColour: application.NewRGB(0x1b, 0x28, 0x38),
		URL:              pageURL + "/",
	})
	// Closing the window hides it to the tray rather than quitting.
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		win.Hide()
		e.Cancel()
	})
	show := func(path string) {
		win.SetURL(pageURL + path)
		win.Show()
		if win.IsMinimised() {
			win.Restore()
		}
		win.Focus()
	}
	setupTray(app, cfg, launcher, details, srv.nfc, show)
	if path := showArg(); path != "" {
		app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { show(path) })
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
	log.Print("exiting")
}

// showArg is the page passed by relaunch, to reopen the window after a restart.
func showArg() string {
	for i, arg := range os.Args {
		if arg == showFlag && i+1 < len(os.Args) && strings.HasPrefix(os.Args[i+1], "/") {
			return os.Args[i+1]
		}
	}
	return ""
}

// onMain runs fn on the UI thread; menu items are changed from the scanner,
// NFC and update goroutines. It only works once the app is running.
func onMain(fn func()) { application.InvokeAsync(fn) }

func setupTray(app *application.App, cfg Config, launcher *Launcher, details *DetailsStore, nfc *NFCManager, show func(path string)) {
	tray := app.SystemTray.New()
	tray.SetIcon(trayIcon())
	tray.SetTooltip(productName)
	tray.OnClick(func() { show("/") })
	menu := app.NewMenu()
	// The NFC and scanner watchers update their menu items through onMain,
	// which needs the app running.
	var workers []func()
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		for _, run := range workers {
			go run()
		}
	})

	if nfc != nil {
		mNFC := menu.Add("NFC base: looking…").SetEnabled(false)
		nfc.onStatus = func(s string) { onMain(func() { mNFC.SetLabel(s) }) }
		workers = append(workers, nfc.Run)
	}

	if !strings.EqualFold(cfg.ScannerPort, "off") {
		mScanner := menu.Add("Scanner: looking…").SetEnabled(false)
		menu.AddSeparator()
		scanners, err := NewScannerManager(launcher, cfg, func(s string) { onMain(func() { mScanner.SetLabel(s) }) })
		if err != nil {
			fatal("Bad scanner config: " + err.Error())
		}
		workers = append(workers, scanners.Run)
	}

	menu.Add("Open QR page").OnClick(func(*application.Context) { show("/") })
	menu.Add("Settings…").OnClick(func(*application.Context) { show("/settings") })
	menu.Add("Rescan games").OnClick(func(*application.Context) {
		if err := launcher.Rescan(); err != nil {
			log.Printf("rescan: %v", err)
		}
		go details.FetchMissing(launcher.Apps())
	})
	menu.Add("Open log folder").OnClick(func(*application.Context) { shellOpen(logDir()) })
	menu.AddSeparator()
	mUpdate := menu.Add(updateTitle())
	mUpdate.OnClick(func(*application.Context) { runUpdateCheck(app, cfg, mUpdate) })
	// Wails has already flipped the tick when OnClick runs, so Checked() is
	// what the user asked for; put it back if the registry change fails.
	mStartup := menu.AddCheckbox("Start with Windows", startupEnabled())
	mStartup.OnClick(func(*application.Context) {
		on := mStartup.Checked()
		if err := setStartup(on); err != nil {
			log.Printf("start with windows: %v", err)
			onMain(func() { mStartup.SetChecked(!on) })
		}
	})
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)
}

func startupEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runName)
	return err == nil
}

func setStartup(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		return k.DeleteValue(runName)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(runName, `"`+exe+`"`)
}

// moveOldStartup points a "Start with Windows" entry left by Steam QR
// Launcher at this exe, under the new name.
func moveOldStartup() {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	if _, _, err := k.GetStringValue(oldRunName); err != nil {
		return
	}
	if err := setStartup(true); err != nil {
		log.Printf("moving start with windows: %v", err)
		return
	}
	k.DeleteValue(oldRunName)
	log.Print("moved start with windows to the new name")
}

func cacheBase() string {
	dir, err := os.UserCacheDir() // %LOCALAPPDATA%
	if err != nil {
		dir = os.TempDir()
	}
	return dir
}

func logDir() string { return appDir(cacheBase()) }

// appDir is the app's folder under base. Until moveOldDirs has moved it,
// that's the old steam-launcher folder.
func appDir(base string) string {
	dir := filepath.Join(base, "cardtridge")
	if _, err := os.Stat(dir); err != nil {
		if old := filepath.Join(base, oldDirName); dirExists(old) {
			return old
		}
	}
	return dir
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// moveOldDirs renames the steam-launcher settings and cache folders to
// cardtridge. When one is in use, it's left for the next start.
func moveOldDirs() error {
	var errs []error
	for _, base := range []string{configBase(), cacheBase()} {
		old, dir := filepath.Join(base, oldDirName), filepath.Join(base, "cardtridge")
		if dirExists(old) && !dirExists(dir) {
			if err := os.Rename(old, dir); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func setupLogging() {
	os.MkdirAll(logDir(), 0o755)
	f, err := os.OpenFile(filepath.Join(logDir(), "launcher.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		log.SetOutput(f)
	}
}

func updateTitle() string { return "Check for updates (" + version + ")" }

// runUpdateCheck backs the tray's "Check for updates" item: it asks before
// installing, then hands over to the new version and quits.
func runUpdateCheck(app *application.App, cfg Config, item *application.MenuItem) {
	setItem := func(label string, enabled bool) {
		onMain(func() { item.SetLabel(label).SetEnabled(enabled) })
	}
	setItem("Checking for updates…", false)
	defer setItem(updateTitle(), true)

	m, newer, err := checkForUpdate(cfg)
	if err != nil {
		log.Printf("update check: %v", err)
		messageBox("Couldn't check for updates:\n\n"+err.Error(), windows.MB_OK|windows.MB_ICONWARNING)
		return
	}
	if !newer {
		messageBox(fmt.Sprintf("You're on the latest version (%s).", version), windows.MB_OK|windows.MB_ICONINFORMATION)
		return
	}
	msg := fmt.Sprintf("Version %s is available (you have %s).\n\nUpdate now? The launcher will restart.", m.Version, version)
	if messageBox(msg, windows.MB_YESNO|windows.MB_ICONQUESTION) != idYes {
		return
	}
	setItem("Updating to "+m.Version+"…", false)
	if err := applyUpdate(m, ""); err != nil {
		log.Printf("update: %v", err)
		messageBox("Update failed:\n\n"+err.Error(), windows.MB_OK|windows.MB_ICONERROR)
		return
	}
	app.Quit()
}

const idYes = 6

func messageBox(msg string, flags uint32) int32 {
	text, _ := windows.UTF16PtrFromString(msg)
	caption, _ := windows.UTF16PtrFromString(productName)
	ret, _ := windows.MessageBox(0, text, caption, flags|windows.MB_SETFOREGROUND)
	return ret
}

// fatal shows a message box, since a windowsgui app has no console.
func fatal(msg string) {
	log.Print(msg)
	messageBox(msg, windows.MB_OK|windows.MB_ICONERROR)
	os.Exit(1)
}
