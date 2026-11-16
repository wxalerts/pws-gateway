// Command simulator writes rtl_433 style JSON lines for synthetic weather
// stations, so the gateway can be exercised without a radio.
//
//	simulator -stations 4 -duration 30m > sim.jsonl
//	simulator -stations 2 -speed 60 | pwsgw ...        # 60x real time
//	simulator -stations 1 -speed 0 -out sim.jsonl       # as fast as possible
//
// Stations alternate between the Acurite-5n1 shape (two message types every
// 18 s, three identical repeats per transmission, imperial units as the
// decoder emits them) and the Fineoffset-WH65B shape (one message every 16 s,
// native SI). Temperature follows a daily sine with noise, wind direction
// wanders around a prevailing direction, and rain accumulates in tips.
// Field names and units follow rtl_433's decoders; the output goes through
// the same mapping as a real capture.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"sort"
	"time"
)

type config struct {
	Stations int
	Start    time.Time
	Duration time.Duration
	Seed     int64
}

type event struct {
	at   time.Time
	line string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "simulator:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		n        = flag.Int("stations", 2, "number of synthetic stations")
		duration = flag.Duration("duration", time.Hour, "simulated time to cover")
		speed    = flag.Float64("speed", 0, "playback speed factor: 1 is real time, 60 is a minute per second, 0 is as fast as possible")
		out      = flag.String("out", "", "write to this file instead of stdout")
		seed     = flag.Int64("seed", 1, "random seed, for reproducible output")
		start    = flag.String("start", "", "RFC 3339 start time, default now (UTC)")
	)
	flag.Parse()

	cfg := config{Stations: *n, Duration: *duration, Seed: *seed, Start: time.Now().UTC().Truncate(time.Second)}
	if *start != "" {
		t, err := time.Parse(time.RFC3339, *start)
		if err != nil {
			return fmt.Errorf("bad -start: %w", err)
		}
		cfg.Start = t.UTC()
	}
	if cfg.Stations < 1 || cfg.Duration <= 0 || *speed < 0 {
		return errors.New("need -stations >= 1, -duration > 0 and -speed >= 0")
	}

	var w io.Writer = os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		w = f
	}
	bw := bufio.NewWriter(w)
	return emit(bw, generate(cfg), cfg.Start, *speed, time.Sleep)
}

// emit writes events in time order, sleeping between them when speed > 0.
// Output is flushed per line when pacing so a pipe reader sees it live.
func emit(w *bufio.Writer, evs []event, start time.Time, speed float64, sleep func(time.Duration)) error {
	prev := start
	for _, e := range evs {
		if speed > 0 {
			if gap := e.at.Sub(prev); gap > 0 {
				sleep(time.Duration(float64(gap) / speed))
			}
		}
		prev = e.at
		if _, err := w.WriteString(e.line + "\n"); err != nil {
			return err
		}
		if speed > 0 {
			if err := w.Flush(); err != nil {
				return err
			}
		}
	}
	return w.Flush()
}

// generate builds every line for every station, sorted by time.
func generate(cfg config) []event {
	var evs []event
	for i := 0; i < cfg.Stations; i++ {
		r := rand.New(rand.NewSource(cfg.Seed + int64(i)*7919)) //nolint:gosec // simulation noise, not security
		if i%2 == 0 {
			evs = append(evs, acurite5n1(cfg, i, r)...)
		} else {
			evs = append(evs, wh65b(cfg, i, r)...)
		}
	}
	sort.SliceStable(evs, func(a, b int) bool { return evs[a].at.Before(evs[b].at) })
	return evs
}

// weather is the slowly varying truth a station samples.
type weather struct {
	meanC, ampC float64
	humBase     float64
	windBase    float64
	dir         float64
	rainTotalMM float64
	r           *rand.Rand
}

func newWeather(r *rand.Rand) *weather {
	return &weather{
		meanC: 8 + r.Float64()*14, ampC: 3 + r.Float64()*5,
		humBase: 55 + r.Float64()*20, windBase: 1.5 + r.Float64()*3,
		dir: r.Float64() * 360, rainTotalMM: math.Round(r.Float64()*300*10) / 10, r: r,
	}
}

// at samples the weather. The daily cycle peaks at 15:00 UTC.
func (w *weather) at(t time.Time) (tempC, hum, windMS, dir float64) {
	h := float64(t.Hour()) + float64(t.Minute())/60
	phase := math.Sin(2 * math.Pi * (h - 9) / 24)
	tempC = w.meanC + w.ampC*phase + w.r.NormFloat64()*0.15
	hum = clamp(w.humBase-8*phase+w.r.NormFloat64()*1.2, 5, 100)
	windMS = math.Max(0, w.windBase*(1+0.3*phase)+w.r.NormFloat64()*0.8)
	w.dir = math.Mod(w.dir+w.r.NormFloat64()*12+360, 360)
	return tempC, math.Round(hum), windMS, w.dir
}

// tip adds rain occasionally: about one 0.2 mm tip per twenty minutes.
func (w *weather) tip(step time.Duration) {
	if w.r.Float64() < step.Minutes()/20 {
		w.rainTotalMM += 0.2
	}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func stamp(t time.Time) string {
	return fmt.Sprintf("%s.%06d", t.UTC().Format("2006-01-02T15:04:05"), t.Nanosecond()/1000)
}

func radio(r *rand.Rand, freq float64) string {
	rssi := -6 - r.Float64()*8
	noise := -14.2 - r.Float64()*0.6
	return fmt.Sprintf(`"freq" : %.3f, "rssi" : %.3f, "snr" : %.3f, "noise" : %.3f`,
		freq+(r.Float64()-0.5)*0.04, rssi, rssi-noise, noise)
}

func acurite5n1(cfg config, idx int, r *rand.Rand) []event {
	w := newWeather(r)
	id := 1000 + 137*idx + r.Intn(100)
	channel := string(rune('A' + idx%3))
	var evs []event
	t := cfg.Start.Add(time.Duration(r.Intn(18000)) * time.Millisecond)
	end := cfg.Start.Add(cfg.Duration)
	typ := 49
	for ; t.Before(end); t = t.Add(18*time.Second + time.Duration(r.Intn(200)-100)*time.Millisecond) {
		tempC, hum, windMS, dir := w.at(t)
		w.tip(18 * time.Second)
		windKmh := math.Round(windMS*3.6*1000) / 1000
		var body string
		if typ == 49 {
			body = fmt.Sprintf(`"wind_avg_km_h" : %.3f, "wind_dir_deg" : %.3f, "rain_in" : %.3f`,
				windKmh, math.Round(dir/22.5)*22.5, w.rainTotalMM/25.4)
		} else {
			body = fmt.Sprintf(`"wind_avg_km_h" : %.3f, "temperature_F" : %.1f, "humidity" : %d`,
				windKmh, tempC*9/5+32, int(hum))
		}
		for seq := 0; seq < 3; seq++ {
			evs = append(evs, event{t, fmt.Sprintf(
				`{"time" : %q, "protocol" : 40, "model" : "Acurite-5n1", "message_type" : %d, "id" : %d, "channel" : %q, "sequence_num" : %d, "battery_ok" : 1, %s, "mic" : "CHECKSUM", "mod" : "ASK", %s}`,
				stamp(t), typ, id, channel, seq, body, radio(r, 433.92))})
		}
		if typ == 49 {
			typ = 56
		} else {
			typ = 49
		}
	}
	return evs
}

func wh65b(cfg config, idx int, r *rand.Rand) []event {
	w := newWeather(r)
	id := 40 + idx*11 + r.Intn(10)
	var evs []event
	t := cfg.Start.Add(time.Duration(r.Intn(16000)) * time.Millisecond)
	end := cfg.Start.Add(cfg.Duration)
	for ; t.Before(end); t = t.Add(16*time.Second + time.Duration(r.Intn(80)-40)*time.Millisecond) {
		tempC, hum, windMS, dir := w.at(t)
		w.tip(16 * time.Second)
		gust := windMS + math.Abs(r.NormFloat64())*0.8
		evs = append(evs, event{t, fmt.Sprintf(
			`{"time" : %q, "protocol" : 78, "model" : "Fineoffset-WH65B", "id" : %d, "battery_ok" : 1, "temperature_C" : %.1f, "humidity" : %d, "wind_dir_deg" : %d, "wind_avg_m_s" : %.1f, "wind_max_m_s" : %.1f, "rain_mm" : %.1f, "mic" : "CRC", "mod" : "FSK", %s}`,
			stamp(t), id, tempC, int(hum), int(math.Round(dir)), windMS, gust, w.rainTotalMM, radio(r, 433.92))})
	}
	return evs
}
