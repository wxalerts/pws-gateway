// Package cwop is the Pi-side CWOP relay plugin: sea-level pressure and 10 m
// wind normalisation, the APRS packet format, the APRS-IS login and delivery,
// and the loop that drives them. It follows docs/relay/README.md and runs the
// golden vectors in testdata/relay/.
package cwop

import "math"

// Constants of the specification, identical to the WxAlerts API readings code.
const (
	lapseKPerM  = 0.0065
	slpExponent = -5.257
	roughnessM  = 0.03
	referenceM  = 10.0
	mphPerMS    = 2.2369362920544
)

// SeaLevelPressure reduces a station pressure (hPa) at altitude altM (m) and
// air temperature tC (C) to sea level, rounded to 0.1 hPa. ok is false when the
// inputs do not give a valid result.
func SeaLevelPressure(pHPa, tC, altM float64) (hpa float64, ok bool) {
	denom := tC + lapseKPerM*altM + 273.15
	if denom <= 0 {
		return 0, false
	}
	base := 1 - lapseKPerM*altM/denom
	if base <= 0 {
		return 0, false
	}
	return math.RoundToEven(pHPa*math.Pow(base, slpExponent)*10) / 10, true
}

// WindAt10m normalises a wind speed (m/s) measured at heightM to 10 m with the
// neutral logarithmic profile, rounded to 0.01 m/s. ok is false without a
// usable height.
func WindAt10m(vMS, heightM float64) (ms float64, ok bool) {
	if heightM <= roughnessM {
		return 0, false
	}
	v := vMS * math.Log(referenceM/roughnessM) / math.Log(heightM/roughnessM)
	return math.RoundToEven(v*100) / 100, true
}
