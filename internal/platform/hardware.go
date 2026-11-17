package platform

import "time"

// ModemInfo is the state of the cellular modem as ModemManager reports it.
type ModemInfo struct {
	// Model is the manufacturer and model string, for display.
	Model string `json:"model"`
	// State is the ModemManager state name: registered, connected, searching,
	// disabled and so on.
	State string `json:"state"`
	// Carrier is the operator name, MCCMNC the digits only network code.
	Carrier string `json:"carrier"`
	MCCMNC  string `json:"mcc_mnc"`
	// Reg is the 3GPP registration state: home, roaming, searching, denied,
	// idle, unknown.
	Reg string `json:"reg"`
	// Tech is the radio access technology: lte, umts, gsm, nr, unknown.
	Tech string `json:"tech"`
	// RSSIdBm is nil when the modem does not report a signal strength in dBm.
	RSSIdBm *float64 `json:"rssi_dbm"`
	// SignalPct is the coarse 0 to 100 quality, nil when unknown.
	SignalPct *int `json:"signal_pct"`
	// NetworkTime is the time the carrier network reported, nil when the
	// modem has none.
	NetworkTime *time.Time `json:"network_time"`
}

// GPSInfo is the GNSS receiver state as gpsd reports it.
type GPSInfo struct {
	// Fix is 0 for no fix, 2 for a two dimensional fix and 3 for three
	// dimensional.
	Fix  int `json:"fix"`
	Sats int `json:"sats"`
	// Lat, Lon and AltM are nil until there is a fix that carries them.
	Lat  *float64 `json:"lat"`
	Lon  *float64 `json:"lon"`
	AltM *float64 `json:"alt_m"`
}

// I2C device kinds reported by the scan.
const (
	I2CKindDS3231  = "ds3231"
	I2CKindBMP280  = "bmp280"
	I2CKindBME280  = "bme280"
	I2CKindBME680  = "bme680"
	I2CKindUnknown = "unknown"
)

// I2CDevice is one address that answered the bus scan.
type I2CDevice struct {
	Addr    int    `json:"addr"`
	AddrHex string `json:"addr_hex"`
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	// ChipID is the value of register 0xD0 for the pressure sensor family,
	// nil for other devices.
	ChipID *int `json:"chip_id"`
	// KernelDriver is true when a kernel driver already owns the address, as
	// with a DS3231 bound through the i2c-rtc overlay.
	KernelDriver bool `json:"kernel_driver"`
}

// I2CStatus is the state of the I2C bus: whether it is enabled in the boot
// configuration, whether the bus device exists, and what answered a scan.
type I2CStatus struct {
	// Enabled is the dtparam=i2c_arm setting in the boot configuration.
	Enabled bool `json:"enabled"`
	// BusPresent is true when the kernel exposes the bus device. It lags
	// Enabled until the next reboot.
	BusPresent bool        `json:"bus_present"`
	Devices    []I2CDevice `json:"devices"`
}

// RTCInfo is the state of an attached DS3231 real-time clock.
type RTCInfo struct {
	// Time is the RTC time in UTC.
	Time time.Time `json:"time"`
	// OscillatorStopped is the OSF flag: the oscillator halted since the
	// flag was last cleared, so the time cannot be trusted.
	OscillatorStopped bool `json:"oscillator_stopped"`
	// TempC is the die temperature, nil when it could not be read.
	TempC *float64 `json:"temp_c"`
}

// PressureReading is one BMP280 measurement.
type PressureReading struct {
	// StationHPa is the uncorrected station pressure in hectopascals.
	StationHPa float64
}

// TimeSource names the clock source for the UI and telemetry: chrony's
// selected source class when synced (ntp, gps or lte), "rtc" when unsynced but
// a DS3231 whose oscillator never stopped holds provisional time, else
// "none". An RTC never marks the clock synced.
func TimeSource(ts TimeSync, rtc *RTCInfo) string {
	if ts.Synced {
		if ts.Ref != "" {
			return ts.Ref
		}
		return "ntp"
	}
	if rtc != nil && !rtc.OscillatorStopped {
		return "rtc"
	}
	return "none"
}
