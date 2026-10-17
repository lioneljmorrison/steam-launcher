package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/lmorrison/steam-launcher/internal/infinity"
)

const (
	nfcRetryInterval = 3 * time.Second
	pendingTimeout   = 2 * time.Minute
)

type rgb struct{ r, g, b byte }

var (
	colorOff     = rgb{0, 0, 0}
	colorLaunch  = rgb{0, 80, 0}
	colorUnknown = rgb{90, 40, 0}
	colorError   = rgb{90, 0, 0}
)

// PendingCard is an unlinked tag waiting to be assigned a game on the QR page.
type PendingCard struct {
	UID  string    `json:"uid"`
	Seen time.Time `json:"-"`
}

// NFCManager connects to a Disney Infinity base whenever one is plugged in,
// launches the game linked to each tag placed on it, and holds unknown tags
// as pending so they can be linked from the QR page.
type NFCManager struct {
	launcher *Launcher
	cards    *CardStore
	onStatus func(string)

	mu      sync.Mutex
	pending *PendingCard
	base    *infinity.Base
	padOf   map[string]byte // uid -> pad, for tags currently on the base
}

func NewNFCManager(l *Launcher, cards *CardStore, onStatus func(string)) *NFCManager {
	return &NFCManager{launcher: l, cards: cards, onStatus: onStatus, padOf: map[string]byte{}}
}

func (m *NFCManager) Run() {
	missing := false
	for {
		base, err := infinity.Open()
		if err == nil {
			err = m.serve(base)
		}
		if errors.Is(err, infinity.ErrNotFound) {
			if !missing {
				m.onStatus("NFC base: not connected")
				missing = true
			}
		} else {
			if err != nil {
				log.Printf("nfc: %v", err)
			}
			m.onStatus("NFC base: not connected")
			missing = true
		}
		time.Sleep(nfcRetryInterval)
	}
}

func (m *NFCManager) serve(base *infinity.Base) error {
	defer base.Close()
	if err := base.Activate(); err != nil {
		return fmt.Errorf("activate: %w", err)
	}
	m.mu.Lock()
	m.base = base
	m.padOf = map[string]byte{}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.base = nil
		m.mu.Unlock()
	}()

	log.Print("nfc: Disney Infinity base connected")
	m.onStatus("NFC base: connected")
	setColor(base, infinity.PadAll, colorOff)

	removed := map[byte]string{} // event index -> uid, to clear lights and state on removal
	for {
		select {
		case ev := <-base.Events:
			if !ev.Added {
				m.mu.Lock()
				if uid, ok := removed[ev.Index]; ok {
					delete(m.padOf, uid)
					delete(removed, ev.Index)
				}
				m.mu.Unlock()
				setColor(base, ev.Pad, colorOff)
				continue
			}
			raw, err := base.TagUID(ev.Index)
			if err != nil {
				log.Printf("nfc: %v", err)
				setColor(base, ev.Pad, colorError)
				continue
			}
			uid, random := normalizeUID(raw)
			if random {
				// Phones and payment cards use a new random UID on every read,
				// so they can never match a linked card.
				continue
			}
			m.mu.Lock()
			removed[ev.Index] = uid
			m.padOf[uid] = ev.Pad
			m.mu.Unlock()
			setColor(base, ev.Pad, m.tagPlaced(uid))
		case <-base.Done():
			log.Print("nfc: Disney Infinity base disconnected")
			return nil
		}
	}
}

// tagPlaced launches the linked game, or marks the tag as pending. It returns
// the pad color to show.
func (m *NFCManager) tagPlaced(uid string) rgb {
	appID, ok := m.cards.Get(uid)
	if !ok {
		log.Printf("nfc: unknown card %s, waiting to be linked", uid)
		m.mu.Lock()
		m.pending = &PendingCard{UID: uid, Seen: time.Now()}
		m.mu.Unlock()
		return colorUnknown
	}
	_, res, err := m.launcher.Launch(appID, "nfc card "+uid)
	switch {
	case err != nil:
		log.Print(err)
		return colorError
	case res == NotInstalled:
		log.Printf("nfc: card %s is linked to %s, which isn't installed", uid, appID)
		return colorError
	}
	return colorLaunch
}

// Pending returns the unlinked tag most recently placed, if any.
func (m *NFCManager) Pending() *PendingCard {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending != nil && time.Since(m.pending.Seen) > pendingTimeout {
		m.pending = nil
	}
	if m.pending == nil {
		return nil
	}
	p := *m.pending
	return &p
}

// Link assigns a game to a card and, if the card is still on the base, shows it as ready.
func (m *NFCManager) Link(uid, appID string) error {
	if err := m.cards.Link(uid, appID); err != nil {
		return err
	}
	log.Printf("nfc: linked card %s to %s", uid, appID)
	m.mu.Lock()
	if m.pending != nil && m.pending.UID == uid {
		m.pending = nil
	}
	base := m.base
	pad, onBase := m.padOf[uid]
	m.mu.Unlock()
	if base != nil && onBase {
		setColor(base, pad, colorLaunch)
	}
	return nil
}

// CancelPending forgets the unlinked card and turns its pad off if it's still on the base.
func (m *NFCManager) CancelPending() {
	m.mu.Lock()
	p := m.pending
	m.pending = nil
	base := m.base
	var pad byte
	onBase := false
	if p != nil {
		pad, onBase = m.padOf[p.UID]
	}
	m.mu.Unlock()
	if p == nil {
		return
	}
	log.Printf("nfc: cancelled linking card %s", p.UID)
	if base != nil && onBase {
		setColor(base, pad, colorOff)
	}
}

func (m *NFCManager) Unlink(uid string) error {
	log.Printf("nfc: unlinked card %s", uid)
	return m.cards.Unlink(uid)
}

func setColor(base *infinity.Base, pad byte, c rgb) {
	if err := base.SetColor(pad, c.r, c.g, c.b); err != nil {
		log.Printf("nfc: set color: %v", err)
	}
}

// normalizeUID formats a tag UID as hex, trimming the zero padding the base
// adds to 4-byte UIDs. random reports a randomly generated UID (first byte
// 0x08), which changes on every read.
func normalizeUID(raw []byte) (uid string, random bool) {
	if len(raw) == 7 && bytes.Equal(raw[4:], []byte{0, 0, 0}) {
		raw = raw[:4]
	}
	return fmt.Sprintf("%X", raw), len(raw) == 4 && raw[0] == 0x08
}
