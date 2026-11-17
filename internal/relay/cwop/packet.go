package cwop

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/wxalerts/pws-gateway/internal/relay"
)

// SoftwareTag ends every packet: "e" (other) and the unit name.
const SoftwareTag = "eWxPG"

// round is half to even, as the specification requires (it matches the Python
// round the API uses and the C lrint of the existing stations).
func round(x float64) int { return int(math.RoundToEven(x)) }

// formatPosition renders a coordinate in the APRS ddmm.hh form.
func formatPosition(v float64, lat bool) string {
	n := round(math.Abs(v) * 6000)
	deg, mins := n/6000, n%6000
	var hemi string
	switch {
	case lat && v >= 0:
		hemi = "N"
	case lat:
		hemi = "S"
	case v >= 0:
		hemi = "E"
	default:
		hemi = "W"
	}
	if lat {
		return fmt.Sprintf("%02d%02d.%02d%s", deg, mins/100, mins%100, hemi)
	}
	return fmt.Sprintf("%03d%02d.%02d%s", deg, mins/100, mins%100, hemi)
}

// windMPH renders a 10 m wind in whole mph, or "..." when it cannot be had.
func windMPH(v *float64, heightM *float64) string {
	if v == nil || heightM == nil {
		return "..."
	}
	ms, ok := WindAt10m(*v, *heightM)
	if !ok {
		return "..."
	}
	return fmt.Sprintf("%03d", min(round(ms*mphPerMS), 999))
}

// FormatPacket builds the APRS-IS weather packet line for one station in one
// bucket, without the trailing CR LF. id is the CWOP identifier and lat, lon
// the public position in decimal degrees.
func FormatPacket(id string, lat, lon float64, obs relay.Observation) (string, error) {
	st := obs.Station
	if st.N <= 0 {
		return "", relay.Errorf(relay.CodeNoData, "the bucket has no samples")
	}
	end := obs.Start.UTC().Add(time.Duration(obs.IntervalS) * time.Second)
	var b strings.Builder
	fmt.Fprintf(&b, "%s>APRS,TCPIP*:@%02d%02d%02dz%s/%s_", id, end.Day(), end.Hour(), end.Minute(),
		formatPosition(lat, true), formatPosition(lon, false))

	f := st.F
	if f.WindDirDeg != nil {
		d := round(f.WindDirDeg.Vec) % 360
		if d < 0 {
			d += 360
		}
		if d == 0 {
			d = 360
		}
		fmt.Fprintf(&b, "%03d", d)
	} else {
		b.WriteString("...")
	}
	var height *float64
	if st.Meta != nil {
		height = st.Meta.WindHeightM
	}
	var avg, gust *float64
	if f.WindSpeedMS != nil {
		avg = f.WindSpeedMS.Avg
	}
	if f.WindGustMS != nil {
		gust = f.WindGustMS.Max
	}
	fmt.Fprintf(&b, "/%sg%s", windMPH(avg, height), windMPH(gust, height))

	var tc *float64
	if f.TempC != nil {
		tc = f.TempC.Avg
	}
	if tc != nil {
		fmt.Fprintf(&b, "t%03d", round(*tc*9/5+32))
	} else {
		b.WriteString("t...")
	}

	if r := obs.Rain; r != nil {
		fmt.Fprintf(&b, "r%03dp%03dP%03d", hundredths(r.Last1hMM), hundredths(r.Last24hMM), hundredths(r.SinceMidnightMM))
	}
	if f.HumidityPct != nil && f.HumidityPct.Avg != nil {
		h := round(*f.HumidityPct.Avg)
		if h >= 100 {
			h = 0
		}
		fmt.Fprintf(&b, "h%02d", max(h, 0))
	}
	if f.PressureStationHPa != nil && f.PressureStationHPa.Avg != nil && tc != nil && st.Meta != nil && st.Meta.AltM != nil {
		if slp, ok := SeaLevelPressure(*f.PressureStationHPa.Avg, *tc, *st.Meta.AltM); ok {
			fmt.Fprintf(&b, "b%05d", round(slp*10))
		}
	}
	b.WriteString(SoftwareTag)
	return b.String(), nil
}

func hundredths(mm float64) int {
	return min(max(round(mm/0.254), 0), 999)
}
