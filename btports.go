package main

import (
	"errors"
	"regexp"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	guidPortsClass     = windows.GUID{Data1: 0x4D36E978, Data2: 0xE325, Data3: 0x11CE, Data4: [8]byte{0xBF, 0xC1, 0x08, 0x00, 0x2B, 0xE1, 0x03, 0x18}}
	guidBluetoothClass = windows.GUID{Data1: 0xE0CBF06C, Data2: 0xCD8B, Data3: 0x4647, Data4: [8]byte{0xBB, 0x8A, 0x26, 0x3B, 0x43, 0xF0, 0xF9, 0x74}}

	// An SPP port's instance ID ends with the remote device's address, e.g.
	// BTHENUM\{00001101-...}_VID&000205AC_PID&0239\B&2E7D18CF&0&AAA8A31C4B8F_C00000000.
	// Incoming ports carry an all-zero address.
	sppPortRe = regexp.MustCompile(`(?i)^BTHENUM\\\{00001101-0000-1000-8000-00805F9B34FB\}_.*&([0-9A-F]{12})_[0-9A-F]+$`)
	btDevRe   = regexp.MustCompile(`(?i)^BTHENUM\\DEV_([0-9A-F]{12})\\`)
)

// BTSerialPort is an outgoing Bluetooth SPP COM port and the paired device it belongs to.
type BTSerialPort struct {
	Port   string // e.g. "COM5"
	Device string // Bluetooth name, e.g. "BarCode Scanner spp"
}

func bluetoothSerialPorts() ([]BTSerialPort, error) {
	names := map[string]string{}
	err := eachDevice(&guidBluetoothClass, 0, func(set windows.DevInfo, d *windows.DevInfoData, id string) {
		m := btDevRe.FindStringSubmatch(id)
		if m == nil {
			return
		}
		if v, err := set.DeviceRegistryProperty(d, windows.SPDRP_FRIENDLYNAME); err == nil {
			if s, ok := v.(string); ok {
				names[strings.ToUpper(m[1])] = s
			}
		}
	})
	if err != nil {
		return nil, err
	}

	var ports []BTSerialPort
	err = eachDevice(&guidPortsClass, windows.DIGCF_PRESENT, func(set windows.DevInfo, d *windows.DevInfoData, id string) {
		m := sppPortRe.FindStringSubmatch(id)
		if m == nil || strings.Trim(m[1], "0") == "" {
			return
		}
		h, err := set.OpenDevRegKey(d, windows.DICS_FLAG_GLOBAL, 0, windows.DIREG_DEV, windows.KEY_READ)
		if err != nil {
			return
		}
		k := registry.Key(h)
		defer k.Close()
		port, _, err := k.GetStringValue("PortName")
		if err != nil {
			return
		}
		ports = append(ports, BTSerialPort{Port: port, Device: names[strings.ToUpper(m[1])]})
	})
	return ports, err
}

func eachDevice(class *windows.GUID, flags windows.DIGCF, fn func(windows.DevInfo, *windows.DevInfoData, string)) error {
	set, err := windows.SetupDiGetClassDevsEx(class, "", 0, flags, 0, "")
	if err != nil {
		return err
	}
	defer set.Close()
	for i := 0; ; i++ {
		d, err := set.EnumDeviceInfo(i)
		if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			return nil
		}
		if err != nil {
			continue
		}
		if id, err := set.DeviceInstanceID(d); err == nil {
			fn(set, d, id)
		}
	}
}
