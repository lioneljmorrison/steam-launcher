package main

import (
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const usbPollInterval = 500 * time.Millisecond

// watchDrives launches the game on a drive when it's plugged in, like a
// cartridge. Windows no longer runs autorun.inf from USB sticks, so it polls
// for new drive letters instead. A drive counts when it holds a Steam library
// folder with exactly one game; the letter it gets doesn't matter, since the
// library is matched against Steam's own list.
//
// Cards in a card reader keep the reader's drive letter, so removable drives
// are also watched for a change of volume serial. The serial is set when the
// card is formatted, which makes it the card's ID, like an NFC tag's UID: a
// card is linked to a game like an NFC card, and launches it when inserted.
func watchDrives(l *Launcher, cards *NFCManager) {
	prev, _ := windows.GetLogicalDrives()
	prevSerials := removableSerials(prev)
	for {
		time.Sleep(usbPollInterval)
		now, err := windows.GetLogicalDrives()
		if err != nil {
			continue
		}
		serials := removableSerials(now)
		added := now &^ prev
		changed := now != prev || len(serials) != len(prevSerials)
		var inserted []string
		for root, serial := range serials {
			if old, ok := prevSerials[root]; !ok || old != serial {
				inserted = append(inserted, root)
				changed = true
			}
		}
		prev, prevSerials = now, serials
		if !changed {
			continue
		}
		// Games on a drive that was added or removed come and go from the list.
		if err := l.Rescan(); err != nil {
			log.Printf("rescan after drive change: %v", err)
		}
		for _, root := range inserted {
			cardInserted(l, cards, root, serials[root])
		}
		for i := 0; i < 26; i++ {
			root := string(rune('A'+i)) + `:\`
			if _, removable := serials[root]; added&(1<<i) != 0 && !removable {
				driveAdded(l, root)
			}
		}
	}
}

// removableSerials returns the volume serial of each removable drive with
// media in it, by root (`F:\`).
func removableSerials(mask uint32) map[string]uint32 {
	out := map[string]uint32{}
	for i := 0; i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		p, _ := windows.UTF16PtrFromString(root)
		if windows.GetDriveType(p) != windows.DRIVE_REMOVABLE {
			continue
		}
		var serial uint32
		// Fails with ERROR_NOT_READY when the reader is empty.
		if windows.GetVolumeInformation(p, nil, 0, &serial, nil, nil, nil, 0) == nil {
			out[root] = serial
		}
	}
	return out
}

// cardIDFile holds the ID of a card whose volume serial can't be used.
const cardIDFile = "cardtridge-id"

// cardInserted launches the game linked to a card or stick, else the one game
// on it, else holds it as pending so it can be linked on the QR page.
func cardInserted(l *Launcher, cards *NFCManager, root string, serial uint32) {
	uid := cardID(root, serial)
	if uid == "" {
		log.Printf("drive %s: card with no ID inserted", root)
	} else {
		log.Printf("drive %s: card %s inserted", root, uid)
	}
	if uid != "" && cards != nil {
		if _, ok := cards.cards.Get(uid); ok {
			cards.tagPlaced(uid)
			return
		}
	}
	if driveAdded(l, root) || cards == nil {
		return
	}
	if uid == "" {
		var err error
		if uid, err = writeCardID(root); err != nil {
			log.Printf("drive %s: can't give card an ID: %v", root, err)
			return
		}
		log.Printf("drive %s: gave card the ID %s", root, uid)
	}
	cards.tagPlaced(uid)
}

// cardID returns a card's ID: the one in its ID file, else its volume serial.
// Cameras often format cards with serial 0000-0000, which every such card
// shares, so those have no ID until one is written to them.
func cardID(root string, serial uint32) string {
	if b, err := os.ReadFile(root + cardIDFile); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id
		}
	}
	if serial == 0 {
		return ""
	}
	return fmt.Sprintf("SD-%08X", serial)
}

// writeCardID gives a card a random ID in a hidden file, which lasts until
// the card is formatted.
func writeCardID(root string) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := fmt.Sprintf("SD-%X", b)
	path := root + cardIDFile
	if err := os.WriteFile(path, []byte(id+"\r\n"), 0o644); err != nil {
		return "", err
	}
	if p, err := windows.UTF16PtrFromString(path); err == nil {
		windows.SetFileAttributes(p, windows.FILE_ATTRIBUTE_HIDDEN)
	}
	return id, nil
}

// driveAdded launches the one Steam game on a drive, and reports whether it did.
func driveAdded(l *Launcher, root string) bool {
	libs, err := steamLibraries()
	if err != nil {
		return false
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
		return false
	}
	for id := range games {
		if _, _, err := l.Launch(id, "drive "+root); err != nil {
			log.Print(err)
		}
	}
	return true
}
