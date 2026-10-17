// Command steam-launcher is a Windows tray app: scan a QR code with your phone
// or a scanner, and it launches the Steam game on this PC.
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

	"fyne.io/systray"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	appName = "Steam QR Launcher"
	runKey  = `Software\Microsoft\Windows\CurrentVersion\Run`
	runName = "SteamQRLauncher"
)

func main() {
	setupLogging()

	name, _ := windows.UTF16PtrFromString(`Local\SteamQRLauncher`)
	if _, err := windows.CreateMutex(nil, false, name); errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		fatal("Steam QR Launcher is already running. Look for it in the system tray.")
	}

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
		fatal("Couldn't read the Steam library: " + err.Error())
	}

	ln, err := net.Listen("tcp", ":"+strconv.Itoa(cfg.Port))
	if err != nil {
		fatal(fmt.Sprintf("Couldn't listen on port %d: %v", cfg.Port, err))
	}
	host := cfg.HostIP
	if host == "" {
		host = lanIP()
	}
	srv := &Server{launcher: launcher, key: key, base: fmt.Sprintf("http://%s:%d", host, cfg.Port)}
	go func() { log.Fatal(http.Serve(ln, srv.Handler())) }()
	log.Printf("serving on :%d, codes point at %s", cfg.Port, srv.base)

	pageURL := fmt.Sprintf("http://localhost:%d/", cfg.Port)
	systray.Run(func() { onReady(cfg, launcher, pageURL) }, func() { log.Print("exiting") })
}

func onReady(cfg Config, launcher *Launcher, pageURL string) {
	systray.SetIcon(trayIcon())
	systray.SetTitle(appName)
	systray.SetTooltip(appName)
	systray.SetOnTapped(func() { shellOpen(pageURL) })

	if !strings.EqualFold(cfg.ScannerPort, "off") {
		mScanner := systray.AddMenuItem("Scanner: looking…", "")
		mScanner.Disable()
		systray.AddSeparator()
		scanners, err := NewScannerManager(launcher, cfg, mScanner.SetTitle)
		if err != nil {
			fatal("Bad scanner config: " + err.Error())
		}
		go scanners.Run()
	}

	mOpen := systray.AddMenuItem("Open QR page", "Show the QR codes for your games")
	mRescan := systray.AddMenuItem("Rescan games", "Re-read the Steam library")
	mLog := systray.AddMenuItem("Open log folder", "")
	systray.AddSeparator()
	mStartup := systray.AddMenuItemCheckbox("Start with Windows", "", startupEnabled())
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "")

	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				shellOpen(pageURL)
			case <-mRescan.ClickedCh:
				if err := launcher.Rescan(); err != nil {
					log.Printf("rescan: %v", err)
				}
			case <-mLog.ClickedCh:
				shellOpen(logDir())
			case <-mStartup.ClickedCh:
				if err := setStartup(!mStartup.Checked()); err != nil {
					log.Printf("start with windows: %v", err)
				} else if mStartup.Checked() {
					mStartup.Uncheck()
				} else {
					mStartup.Check()
				}
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
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

func logDir() string {
	dir, err := os.UserCacheDir() // %LOCALAPPDATA%
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "steam-launcher")
}

func setupLogging() {
	os.MkdirAll(logDir(), 0o755)
	f, err := os.OpenFile(filepath.Join(logDir(), "launcher.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		log.SetOutput(f)
	}
}

// fatal shows a message box, since a windowsgui app has no console.
func fatal(msg string) {
	log.Print(msg)
	text, _ := windows.UTF16PtrFromString(msg)
	caption, _ := windows.UTF16PtrFromString(appName)
	windows.MessageBox(0, text, caption, windows.MB_OK|windows.MB_ICONERROR)
	os.Exit(1)
}
