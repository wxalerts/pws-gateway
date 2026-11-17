package uplink

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/platform"
	"github.com/wxalerts/pws-gateway/internal/version"
)

// telCycle builds and enqueues one tel message from platform health, time
// sync, the outbox state and the radio list.
func (u *Uplink) telCycle(ctx context.Context) error {
	return u.publishMessage(ctx, contract.Topic(u.opts.DeviceID, contract.KindTel), u.buildTel(ctx))
}

func (u *Uplink) buildTel(ctx context.Context) *contract.Tel {
	now := u.now().UTC().Truncate(time.Second)
	tel := &contract.Tel{
		V: contract.Version, DeviceID: u.opts.DeviceID, TS: now,
		FW:   version.Version,
		Time: contract.TimeSync{Src: "none"},
		Net:  contract.Net{Link: u.linkType(ctx)},
	}

	if h, err := u.opts.Platform.Health(ctx); err == nil {
		tel.UptimeS = max(h.UptimeS, 0)
		if h.CPUTempKnown {
			t := h.CPUTempC
			tel.Sys.CPUTempC = &t
		}
		if h.Throttled.Known {
			th := int(h.Throttled.Raw & 0x7fffffff)
			tel.Sys.Throttled = &th
		}
		mem := float64(h.MemFreeMB)
		tel.Sys.MemFreeMB = &mem
		// data_used_pct is the share of a metered data allowance, not disk
		// usage, so it stays unset until the modem module (M7) knows it.
	} else if !isUnsupported(err) {
		u.log.Warn("health read failed", "err", err)
	}

	var rtc *platform.RTCInfo
	if r, err := u.opts.Platform.RTC(ctx); err == nil {
		rtc = r
	}
	if ts, err := u.opts.Platform.TimeSync(ctx); err == nil {
		tel.Time.Synced = ts.Synced
		if ts.OffsetKnown {
			tel.Time.OffsetMS = float64(ts.Offset) / float64(time.Millisecond)
		}
		tel.Time.Src = platform.TimeSource(ts, rtc)
	}

	tel.Modem = u.modemTel(ctx)
	tel.GPS = u.gpsTel(ctx)

	if st, err := u.opts.Store.OutboxStatus(ctx); err == nil {
		tel.Outbox.Depth = int(st.Count)
		if !st.Oldest.IsZero() && now.After(st.Oldest) {
			tel.Outbox.OldestS = int64(now.Sub(st.Oldest) / time.Second)
		}
	}

	if u.opts.SDRs != nil {
		tel.SDR = u.opts.SDRs()
	}
	u.fillPackets(tel)
	return tel
}

// modemTel reports the modem, or nil when none is attached or ModemManager
// is not running, so the tel message then has no modem key.
func (u *Uplink) modemTel(ctx context.Context) *contract.Modem {
	m, err := u.opts.Platform.Modem(ctx)
	if err != nil {
		if !isUnsupported(err) {
			u.log.Warn("modem read failed", "err", err)
		}
		return nil
	}
	if m == nil {
		return nil
	}
	out := &contract.Modem{Carrier: m.Carrier, Reg: m.Reg, Tech: m.Tech, RSSIdBm: m.RSSIdBm}
	// The contract pattern is five or six digits; anything else is left out
	// rather than failing validation for the whole message.
	if n := len(m.MCCMNC); n == 5 || n == 6 {
		out.MCCMNC = m.MCCMNC
	}
	return out
}

// gpsTel reports the GNSS receiver, or nil when none is attached.
func (u *Uplink) gpsTel(ctx context.Context) *contract.GPS {
	g, err := u.opts.Platform.GPS(ctx)
	if err != nil {
		if !isUnsupported(err) {
			u.log.Warn("gps read failed", "err", err)
		}
		return nil
	}
	if g == nil {
		return nil
	}
	fix, sats := g.Fix, g.Sats
	return &contract.GPS{Fix: &fix, Sats: &sats, Lat: g.Lat, Lon: g.Lon, AltM: g.AltM}
}

// fillPackets sets packets_15m on the radio entries that lack it. The
// collector counts readings, not readings per radio, so the figure is exact
// for the usual single radio and omitted when several radios share the
// count. It is the growth of the reading counter since the previous tel.
func (u *Uplink) fillPackets(tel *contract.Tel) {
	if u.opts.Collector == nil {
		return
	}
	cur := u.opts.Collector.Status().Readings
	u.gateMu.Lock()
	prev, have := u.prevReadings, u.havePrev
	u.prevReadings, u.havePrev = cur, true
	u.gateMu.Unlock()
	if !have || len(tel.SDR) != 1 || tel.SDR[0].Packets15m != nil {
		return
	}
	n := int(max(cur-prev, 0))
	tel.SDR[0].Packets15m = &n
}

// linkType classifies the default route interface name. Names the table does
// not know, and hosts that cannot tell, report "unknown".
func (u *Uplink) linkType(ctx context.Context) string {
	name, err := u.opts.Platform.DefaultRouteInterface(ctx)
	if err != nil {
		return "unknown"
	}
	return LinkFromInterface(name)
}

// LinkFromInterface maps a Linux interface name to a contract net.link value.
func LinkFromInterface(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasPrefix(n, "wwan"), strings.HasPrefix(n, "ppp"),
		strings.HasPrefix(n, "rmnet"), strings.HasPrefix(n, "usb"):
		return "lte"
	case strings.HasPrefix(n, "wlan"), strings.HasPrefix(n, "wlp"), strings.HasPrefix(n, "wlx"):
		return "wifi"
	case strings.HasPrefix(n, "eth"), strings.HasPrefix(n, "en"):
		return "ethernet"
	default:
		return "unknown"
	}
}

func isUnsupported(err error) bool { return errors.Is(err, platform.ErrUnsupported) }
