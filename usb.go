package main

import (
	"log"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const usbPollInterval = 2 * time.Second

// watchDrives launches the game on a drive when it's plugged in, like a
// cartridge. Windows no longer runs autorun.inf from USB sticks, so it polls
// for new drive letters instead. A drive counts when it holds a Steam library
// folder with exactly one game; the letter it gets doesn't matter, since the
// library is matched against Steam's own list.
func watchDrives(l *Launcher) {
	prev, _ := windows.GetLogicalDrives()
	for {
		time.Sleep(usbPollInterval)
		now, err := windows.GetLogicalDrives()
		if err != nil || now == prev {
			continue
		}
		added := now &^ prev
		prev = now
		// Games on a drive that was added or removed come and go from the list.
		if err := l.Rescan(); err != nil {
			log.Printf("rescan after drive change: %v", err)
		}
		for i := 0; i < 26; i++ {
			if added&(1<<i) != 0 {
				driveAdded(l, string(rune('A'+i))+`:\`)
			}
		}
	}
}

func driveAdded(l *Launcher, root string) {
	libs, err := steamLibraries()
	if err != nil {
		return
	}
	games := map[string]string{}
	for _, lib := range libs {
		if strings.HasPrefix(strings.ToUpper(lib), root[:2]) {
			for id, name := range libraryApps(lib) {
				games[id] = name
			}
		}
	}
	if len(games) != 1 {
		if len(games) > 1 {
			log.Printf("drive %s has %d Steam games; not launching any", root, len(games))
		}
		return
	}
	for id := range games {
		if _, _, err := l.Launch(id, "drive "+root); err != nil {
			log.Print(err)
		}
	}
}
