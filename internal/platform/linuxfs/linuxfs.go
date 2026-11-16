// Package linuxfs reads host facts from the Linux proc and sysfs trees.
//
// It is the only code in pwsgw that opens anything under /proc or /sys, and it
// is only imported by the platform adapters. Every reader takes the tree root
// as a parameter so tests point it at a fixture directory instead of the live
// kernel. Readers return ("", false) or an error for anything missing; they
// never invent a value.
package linuxfs

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Host points the readers at a filesystem root. The zero value reads the live
// system.
type Host struct {
	// Root is prefixed to every path. "" or "/" means the real root.
	Root string
}

func (h Host) path(rel string) string {
	if h.Root == "" {
		return "/" + rel
	}
	return filepath.Join(h.Root, rel)
}

// readTrimmed returns the file content with surrounding whitespace and NUL
// bytes removed. Device tree strings are NUL terminated.
func (h Host) readTrimmed(rel string) (string, error) {
	b, err := os.ReadFile(h.path(rel))
	if err != nil {
		return "", err
	}
	return strings.Trim(string(b), " \t\r\n\x00"), nil
}

// Exists reports whether the relative path exists.
func (h Host) Exists(rel string) bool {
	_, err := os.Stat(h.path(rel))
	return err == nil
}

// DeviceTreeModel returns /proc/device-tree/model, the human readable board
// name. ok is false on hosts without a device tree (x86 PCs).
func (h Host) DeviceTreeModel() (model string, ok bool) {
	s, err := h.readTrimmed("proc/device-tree/model")
	if err != nil || s == "" {
		return "", false
	}
	return s, true
}

// DeviceTreeCompatible returns the NUL separated compatible strings, for
// example ["raspberrypi,4-model-b", "brcm,bcm2711"].
func (h Host) DeviceTreeCompatible() []string {
	b, err := os.ReadFile(h.path("proc/device-tree/compatible"))
	if err != nil {
		return nil
	}
	var out []string
	for _, part := range bytes.Split(b, []byte{0}) {
		if s := strings.TrimSpace(string(part)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// DeviceTreeSerial returns /proc/device-tree/serial-number when present.
func (h Host) DeviceTreeSerial() string {
	s, err := h.readTrimmed("proc/device-tree/serial-number")
	if err != nil {
		return ""
	}
	return s
}

// MemInfo holds the two /proc/meminfo lines pwsgw cares about, in mebibytes.
type MemInfo struct {
	TotalMB     int
	AvailableMB int
}

// MemInfo parses /proc/meminfo.
func (h Host) MemInfo() (MemInfo, error) {
	f, err := os.Open(h.path("proc/meminfo"))
	if err != nil {
		return MemInfo{}, err
	}
	defer f.Close()
	var mi MemInfo
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			mi.TotalMB = kb / 1024
		case "MemAvailable:":
			mi.AvailableMB = kb / 1024
		}
	}
	if err := sc.Err(); err != nil {
		return MemInfo{}, err
	}
	if mi.TotalMB == 0 {
		return MemInfo{}, errors.New("linuxfs: MemTotal missing from meminfo")
	}
	return mi, nil
}

// Uptime returns whole seconds since boot from /proc/uptime.
func (h Host) Uptime() (int64, error) {
	s, err := h.readTrimmed("proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0, errors.New("linuxfs: empty uptime")
	}
	f, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("linuxfs: parse uptime %q: %w", fields[0], err)
	}
	return int64(f), nil
}

// CPUTempC reads thermal_zone0 in millidegrees and returns degrees Celsius.
// On every Raspberry Pi zone 0 is the SoC sensor.
func (h Host) CPUTempC() (float64, error) {
	s, err := h.readTrimmed("sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0, err
	}
	milli, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("linuxfs: parse temp %q: %w", s, err)
	}
	return float64(milli) / 1000, nil
}

// throttledPaths are the known locations of the firmware get_throttled
// attribute. The first is the classic path (Pi 3 and 4). On the Pi 5 the SoC
// node is named with its bus address, so a glob covers that layout.
var throttledPaths = []string{
	"sys/devices/platform/soc/soc:firmware/get_throttled",
	"sys/devices/platform/*/*:firmware/get_throttled",
}

// ThrottledRaw returns the text of the firmware get_throttled attribute.
// ok is false when the host has no Raspberry Pi firmware node.
func (h Host) ThrottledRaw() (string, bool) {
	for _, p := range throttledPaths {
		matches, _ := filepath.Glob(h.path(p))
		sort.Strings(matches)
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err == nil {
				return strings.TrimSpace(string(b)), true
			}
		}
	}
	return "", false
}

// USBDevice is a raw sysfs USB device record.
type USBDevice struct {
	Port         string
	Bus, Dev     int
	Vendor       uint16
	Product      uint16
	Manufacturer string
	ProductName  string
	Serial       string
}

// USBDevices walks /sys/bus/usb/devices and returns the real devices, skipping
// root hubs (usbN) and interface nodes (those containing a colon).
func (h Host) USBDevices() ([]USBDevice, error) {
	dir := h.path("sys/bus/usb/devices")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []USBDevice
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "usb") || strings.Contains(name, ":") {
			continue
		}
		base := filepath.Join(dir, name)
		vendor, err := readHex16(filepath.Join(base, "idVendor"))
		if err != nil {
			continue
		}
		product, err := readHex16(filepath.Join(base, "idProduct"))
		if err != nil {
			continue
		}
		d := USBDevice{
			Port:         name,
			Vendor:       vendor,
			Product:      product,
			Manufacturer: readOptional(filepath.Join(base, "manufacturer")),
			ProductName:  readOptional(filepath.Join(base, "product")),
			Serial:       readOptional(filepath.Join(base, "serial")),
		}
		d.Bus, _ = readInt(filepath.Join(base, "busnum"))
		d.Dev, _ = readInt(filepath.Join(base, "devnum"))
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bus != out[j].Bus {
			return out[i].Bus < out[j].Bus
		}
		return out[i].Dev < out[j].Dev
	})
	return out, nil
}

// TimesyncdSynchronized reports whether systemd-timesyncd has reached sync
// since boot. The daemon touches this marker file on its first successful
// adjustment (systemd 239 and later).
func (h Host) TimesyncdSynchronized() bool {
	return h.Exists("run/systemd/timesync/synchronized")
}

// ChronyRunning reports whether a chronyd pid file is present.
func (h Host) ChronyRunning() bool {
	return h.Exists("run/chrony/chronyd.pid") || h.Exists("run/chronyd.pid")
}

func readOptional(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readInt(p string) (int, error) {
	s := readOptional(p)
	return strconv.Atoi(s)
}

func readHex16(p string) (uint16, error) {
	s := readOptional(p)
	if s == "" {
		return 0, errors.New("linuxfs: missing")
	}
	v, err := strconv.ParseUint(s, 16, 16)
	if err != nil {
		return 0, err
	}
	return uint16(v), nil
}
