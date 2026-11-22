package uplink

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/contract"
)

// maxLiveAge is how long after its end a bucket still counts as live. The
// normal cycle runs every 15 minutes and carries up to three buckets, so a
// bucket built in the normal cycle is at most about 20 minutes old; the margin
// covers the jitter and one missed cycle.
const maxLiveAge = 30 * time.Minute

// groupHas reports whether f carries any field of the group.
func groupHas(f *contract.Fields, group string) bool {
	probe := contract.Fields{}
	copyGroup(&probe, f, group)
	return probe != (contract.Fields{})
}

// groupFieldNames lists the canonical fields of the group that f carries.
func groupFieldNames(f *contract.Fields, group string) []string {
	var out []string
	for _, name := range config.FieldGroups[group] {
		probe := contract.Fields{}
		copyField(&probe, f, name)
		if probe != (contract.Fields{}) {
			out = append(out, name)
		}
	}
	return out
}

// copyGroup copies every field of the group from src to dst.
func copyGroup(dst, src *contract.Fields, group string) {
	for _, name := range config.FieldGroups[group] {
		copyField(dst, src, name)
	}
}

func copyField(dst, src *contract.Fields, name string) {
	switch name {
	case "temp_c":
		dst.TempC = src.TempC
	case "humidity_pct":
		dst.HumidityPct = src.HumidityPct
	case "pressure_station_hpa":
		dst.PressureStationHPa = src.PressureStationHPa
	case "wind_speed_ms":
		dst.WindSpeedMS = src.WindSpeedMS
	case "wind_gust_ms":
		dst.WindGustMS = src.WindGustMS
	case "wind_dir_deg":
		dst.WindDirDeg = src.WindDirDeg
	case "rain_mm":
		dst.RainMM = src.RainMM
	case "uv_index":
		dst.UVIndex = src.UVIndex
	case "light_lux":
		dst.LightLux = src.LightLux
	case "solar_wm2":
		dst.SolarWM2 = src.SolarWM2
	case "lightning_count":
		dst.LightningCount = src.LightningCount
	case "lightning_km":
		dst.LightningKM = src.LightningKM
	case "battery_ok":
		dst.BatteryOK = src.BatteryOK
	}
}

// groupSources decides which station supplies each group for the buckets being
// built. An owner mapping (config.Composite) wins. Without one the group is
// supplied by the only selected station that reports it anywhere in the
// buckets; with several candidates and no mapping the group is left out, which
// is exactly what the server composite did before contract v2 (null, not a
// guess). Reporting is judged over the whole range so a sensor that misses one
// bucket does not hand the group to another sensor for that bucket.
func groupSources(cfg config.Config, buckets []contract.Bucket) map[string]string {
	cands := map[string]map[string]bool{}
	for i := range buckets {
		for j := range buckets[i].Stations {
			st := &buckets[i].Stations[j]
			for _, g := range config.GroupNames {
				if groupHas(&st.F, g) {
					if cands[g] == nil {
						cands[g] = map[string]bool{}
					}
					cands[g][st.Key] = true
				}
			}
		}
	}
	out := map[string]string{}
	for _, g := range config.GroupNames {
		if key := cfg.Composite[g]; key != "" {
			out[g] = key
			continue
		}
		if len(cands[g]) == 1 {
			for k := range cands[g] {
				out[g] = k
			}
		}
	}
	return out
}

// combineBuckets turns the per station buckets of one range into one record
// per bucket start: the gateway is the weather station. normal is true for the
// regular cycle and false for a resync; only a normal bucket that closed
// recently is live.
func combineBuckets(cfg config.Config, buckets []contract.Bucket, now time.Time, normal, synced bool) []contract.Record {
	src := groupSources(cfg, buckets)
	out := make([]contract.Record, 0, len(buckets))
	for i := range buckets {
		b := &buckets[i]
		if len(b.Stations) == 0 {
			continue
		}
		rec := contract.Record{
			Start:  b.Start,
			Live:   normal && now.Sub(b.Start.Add(BucketWidth)) <= maxLiveAge,
			Synced: synced,
		}
		byKey := make(map[string]*contract.Station, len(b.Stations))
		for j := range b.Stations {
			byKey[b.Stations[j].Key] = &b.Stations[j]
		}
		used := fillGroups(&rec, byKey, src)
		fillBattery(&rec, byKey, used)
		fillHeard(&rec, byKey)
		if len(used) > 1 {
			rec.Src = sourceMap(byKey, src)
		}
		out = append(out, rec)
	}
	return out
}

// fillGroups copies each group from its source station and takes the site
// metadata from the pressure and wind sources. It returns the keys that
// supplied at least one field.
func fillGroups(rec *contract.Record, byKey map[string]*contract.Station, src map[string]string) map[string]bool {
	used := map[string]bool{}
	for _, g := range config.GroupNames {
		st := byKey[src[g]]
		if st == nil || !groupHas(&st.F, g) {
			continue
		}
		copyGroup(&rec.F, &st.F, g)
		used[st.Key] = true
		if st.Meta == nil {
			continue
		}
		switch g {
		case "pressure":
			if st.Meta.AltM != nil {
				a := *st.Meta.AltM
				metaOf(rec).AltM = &a
			}
		case "wind":
			if st.Meta.WindHeightM != nil {
				h := *st.Meta.WindHeightM
				metaOf(rec).WindHeightM = &h
			}
		}
	}
	return used
}

// sourceMap names the sensor that supplied each field of the record.
func sourceMap(byKey map[string]*contract.Station, src map[string]string) map[string]string {
	out := map[string]string{}
	for _, g := range config.GroupNames {
		st := byKey[src[g]]
		if st == nil {
			continue
		}
		for _, name := range groupFieldNames(&st.F, g) {
			out[name] = st.Key
		}
	}
	return out
}

// fillBattery reports the worst battery flag over the sensors that supplied a
// field, so one low battery is not hidden by a good one.
func fillBattery(rec *contract.Record, byKey map[string]*contract.Station, used map[string]bool) {
	battery := -1
	for k := range used {
		if f := byKey[k].F.BatteryOK; f != nil && (battery < 0 || f.Last < battery) {
			battery = f.Last
		}
	}
	if battery >= 0 {
		rec.F.BatteryOK = &contract.Flag{Last: battery}
	}
}

// fillHeard sets what is about every sensor in the bucket rather than the
// chosen ones: the packet count, the raw samples and the mean radio signal.
func fillHeard(rec *contract.Record, byKey map[string]*contract.Station) {
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var rssi, snr float64
	var nr, ns int
	for _, k := range keys {
		st := byKey[k]
		rec.N += st.N
		if len(st.Raw) > 0 {
			if rec.Raw == nil {
				rec.Raw = map[string]json.RawMessage{}
			}
			rec.Raw[k] = st.Raw
		}
		if st.Sig == nil {
			continue
		}
		if st.Sig.RSSIdB != nil {
			rssi += *st.Sig.RSSIdB
			nr++
		}
		if st.Sig.SNRdB != nil {
			snr += *st.Sig.SNRdB
			ns++
		}
	}
	if nr == 0 && ns == 0 {
		return
	}
	rec.Sig = &contract.Signal{}
	if nr > 0 {
		v := rssi / float64(nr)
		rec.Sig.RSSIdB = &v
	}
	if ns > 0 {
		v := snr / float64(ns)
		rec.Sig.SNRdB = &v
	}
}

func metaOf(r *contract.Record) *contract.StationMeta {
	if r.Meta == nil {
		r.Meta = &contract.StationMeta{}
	}
	return r.Meta
}
