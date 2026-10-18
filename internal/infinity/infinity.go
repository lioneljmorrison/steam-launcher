// Package infinity talks to a Disney Infinity base (Wii/PS3/Wii U version,
// USB 0E6F:0129) over HID, as a generic NFC tag reader.
//
// Protocol notes (from Dolphin's emulation, Source/Core/Core/IOS/USB/Emulated/Infinity.cpp):
// host packets are FF <len> <cmd> <seq> <data...> <checksum>, replies are
// AA <len> <seq> <data...> <checksum>, and tag events are
// AB 04 <pad> 09 <index> <00 added | 01 removed> <checksum>. All reports are 32 bytes.
package infinity

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

var hidInterfaceGUID = windows.GUID{Data1: 0x4D1E55B2, Data2: 0xF16F, Data3: 0x11CF, Data4: [8]byte{0x88, 0xCB, 0x00, 0x11, 0x11, 0x00, 0x00, 0x30}}

const (
	cmdActivate = 0x80
	cmdSetColor = 0x90
	cmdPresent  = 0xA1
	cmdTagUID   = 0xB4

	reportLen = 33 // report ID + 32 bytes
	timeout   = 2 * time.Second
)

// Pads, as reported in tag events and used for colors.
const (
	PadAll    = 0
	PadCenter = 1 // the hexagon
	PadLeft   = 2 // player 1
	PadRight  = 3 // player 2
)

var ErrNotFound = errors.New("no Disney Infinity base (0E6F:0129) connected")

type Event struct {
	Pad   byte
	Index byte // the base's handle for this tag while it stays on the pad
	Added bool
}

type Base struct {
	Events chan Event
	// Raw, if set before Activate, receives every packet read from the base.
	Raw func([]byte)

	rd, wr windows.Handle
	mu     sync.Mutex
	seq    byte
	wait   map[byte]chan []byte
	done   chan struct{}
}

// Open connects to the first Disney Infinity base found.
func Open() (*Base, error) {
	paths, err := windows.CM_Get_Device_Interface_List("", &hidInterfaceGUID, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		if strings.Contains(strings.ToLower(p), "vid_0e6f&pid_0129") {
			return openPath(p)
		}
	}
	return nil, ErrNotFound
}

func openPath(path string) (*Base, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	open := func(flags uint32) (windows.Handle, error) {
		return windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, flags, 0)
	}
	// Separate handles so writes don't queue behind the pending read. The
	// read handle is overlapped so Close can cancel that read.
	rd, err := open(windows.FILE_FLAG_OVERLAPPED)
	if err != nil {
		return nil, fmt.Errorf("open base: %w", err)
	}
	wr, err := open(0)
	if err != nil {
		windows.CloseHandle(rd)
		return nil, fmt.Errorf("open base: %w", err)
	}
	b := &Base{
		Events: make(chan Event, 16),
		rd:     rd,
		wr:     wr,
		wait:   map[byte]chan []byte{},
		done:   make(chan struct{}),
	}
	return b, nil
}

// Done is closed when the base is unplugged or Close is called.
func (b *Base) Done() <-chan struct{} { return b.done }

func (b *Base) Close() {
	windows.CancelIoEx(b.rd, nil)
	windows.CloseHandle(b.rd)
	windows.CloseHandle(b.wr)
}

// Activate starts the read loop and wakes the base up.
func (b *Base) Activate() error {
	go b.readLoop()
	_, err := b.send(cmdActivate, []byte("(c) Disney 2013")...)
	return err
}

// TagUID returns the 7-byte UID of the tag with the given event index.
func (b *Base) TagUID(index byte) ([]byte, error) {
	r, err := b.send(cmdTagUID, index)
	if err != nil {
		return nil, err
	}
	// AA 09 seq status uid[7] checksum
	if len(r) < 11 || r[3] != 0 {
		return nil, fmt.Errorf("tag uid: unexpected reply % x", r)
	}
	return append([]byte(nil), r[4:11]...), nil
}

// Ping asks the base which tags it holds and discards the answer. It fails
// when the base stops replying.
func (b *Base) Ping() error {
	_, err := b.send(cmdPresent)
	return err
}

func (b *Base) SetColor(pad, r, g, bl byte) error {
	_, err := b.send(cmdSetColor, pad, r, g, bl)
	return err
}

func (b *Base) send(cmd byte, data ...byte) ([]byte, error) {
	b.mu.Lock()
	b.seq++
	if b.seq == 0 {
		b.seq = 1
	}
	seq := b.seq
	ch := make(chan []byte, 1)
	b.wait[seq] = ch
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.wait, seq)
		b.mu.Unlock()
	}()

	pkt := make([]byte, reportLen)
	body := append([]byte{0xFF, byte(len(data) + 2), cmd, seq}, data...)
	copy(pkt[1:], body)
	pkt[1+len(body)] = checksum(body)

	var n uint32
	if err := windows.WriteFile(b.wr, pkt, &n, nil); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	select {
	case r := <-ch:
		return r, nil
	case <-b.done:
		return nil, errors.New("base disconnected")
	case <-time.After(timeout):
		return nil, fmt.Errorf("no reply to command %#x", cmd)
	}
}

func (b *Base) readLoop() {
	defer close(b.done)
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return
	}
	defer windows.CloseHandle(ev)
	buf := make([]byte, reportLen)
	for {
		var n uint32
		ov := windows.Overlapped{HEvent: ev}
		err := windows.ReadFile(b.rd, buf, &n, &ov)
		if errors.Is(err, windows.ERROR_IO_PENDING) {
			err = windows.GetOverlappedResult(b.rd, &ov, &n, true)
		}
		if err != nil {
			return
		}
		if n < 2 {
			continue
		}
		pkt := append([]byte(nil), buf[1:n]...)
		if b.Raw != nil {
			b.Raw(pkt)
		}
		switch pkt[0] {
		case 0xAA:
			b.mu.Lock()
			ch := b.wait[pkt[2]]
			b.mu.Unlock()
			if ch != nil {
				ch <- pkt
			}
		case 0xAB:
			if pkt[1] >= 4 {
				select {
				case b.Events <- Event{Pad: pkt[2], Index: pkt[4], Added: pkt[5] == 0}:
				default:
				}
			}
		}
	}
}

func checksum(b []byte) byte {
	var s byte
	for _, c := range b {
		s += c
	}
	return s
}
