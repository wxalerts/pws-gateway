package platform

import "fmt"

// usbID is a vendor:product pair.
type usbID struct{ vendor, product uint16 }

// rtlSDRIDs lists USB ids of RTL2832U based receivers, mirroring the
// known_devices table in librtlsdr. The two Realtek reference ids cover nearly
// every dongle sold as "RTL-SDR" (including the RTL-SDR Blog V3 and V4 and the
// Nooelec NESDR line, which all report 0x0bda:0x2838). The rest are DVB-T
// sticks that happen to use the same chip and that rtl_433 can also drive.
var rtlSDRIDs = map[usbID]string{
	{0x0bda, 0x2832}: "Realtek RTL2832U",
	{0x0bda, 0x2838}: "Realtek RTL2838 (generic RTL-SDR)",
	{0x0413, 0x6680}: "Leadtek DTV Dongle",
	{0x0413, 0x6f0f}: "Leadtek WinFast DTV Dongle mini D",
	{0x0458, 0x707f}: "Genius TVGo DVB-T03",
	{0x0ccd, 0x00a9}: "Terratec Cinergy T Stick Black",
	{0x0ccd, 0x00b3}: "Terratec NOXON DAB/DAB+ Stick",
	{0x0ccd, 0x00d3}: "Terratec Cinergy T Stick RC",
	{0x0ccd, 0x00e0}: "Terratec NOXON DAB Stick rev 2",
	{0x1554, 0x5020}: "PixelView PV-DT235U(RN)",
	{0x15f4, 0x0131}: "Astrometa DVB-T/DVB-T2",
	{0x185b, 0x0620}: "Compro Videomate U620F",
	{0x185b, 0x0650}: "Compro Videomate U650F",
	{0x1b80, 0xd393}: "GIGABYTE GT-U7300",
	{0x1b80, 0xd394}: "DIKOM USB-DVBT HD",
	{0x1b80, 0xd395}: "Peak 102569AGPK",
	{0x1b80, 0xd397}: "KWorld KW-UB450-T",
	{0x1b80, 0xd398}: "Zaapa ZT-MINDVBZP",
	{0x1b80, 0xd39d}: "SVEON STV20",
	{0x1d19, 0x1101}: "Dexatek DK DVB-T Dongle",
	{0x1d19, 0x1102}: "Dexatek DK DVB-T Dongle rev 2",
	{0x1d19, 0x1103}: "Dexatek Technology DK 5217",
	{0x1d19, 0x1104}: "MSI DigiVox Micro HD",
	{0x1f4d, 0xa803}: "Sweex DVB-T USB",
	{0x1f4d, 0xb803}: "GTek T803",
	{0x1f4d, 0xc803}: "Lifeview LV5TDeluxe",
	{0x1f4d, 0xd286}: "MyGica TD312",
	{0x1f4d, 0xd803}: "PROlectrix DV107669",
}

// IsRTLSDR reports whether the device is an RTL2832U receiver rtl_433 can use.
func (d USBDevice) IsRTLSDR() bool {
	_, ok := rtlSDRIDs[usbID{d.VendorID, d.ProductID}]
	return ok
}

// RTLSDRName returns the librtlsdr name for a known receiver, or "" for any
// other device.
func (d USBDevice) RTLSDRName() string {
	return rtlSDRIDs[usbID{d.VendorID, d.ProductID}]
}

// ID formats the vendor and product ids the way lsusb prints them.
func (d USBDevice) ID() string {
	return fmt.Sprintf("%04x:%04x", d.VendorID, d.ProductID)
}
