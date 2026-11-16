package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wxalerts/pws-gateway/internal/collector/mapping"
	"github.com/wxalerts/pws-gateway/internal/config"
	"github.com/wxalerts/pws-gateway/internal/platform"
)

// ---- health ---------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := s.opts.Platform
	h, err := p.Health(ctx)
	if err != nil {
		s.log.Warn("web: platform health", "err", err)
	}
	ts, err := p.TimeSync(ctx)
	if err != nil {
		s.log.Debug("web: platform time sync", "err", err)
	}
	resets, err := p.USBResetCount(ctx)
	if err != nil {
		resets = 0
	}
	b := p.Board()
	var temp *float64
	if h.CPUTempKnown {
		temp = &h.CPUTempC
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"fw": s.opts.Version,
		"board": map[string]any{
			"model": b.Model, "soc": b.SoC, "arch": b.Arch, "tier": string(b.Tier),
		},
		"cpu_temp_c": temp,
		"throttled": map[string]bool{
			"under_voltage_now":      h.Throttled.UnderVoltageNow,
			"under_voltage_occurred": h.Throttled.UnderVoltageOccurred,
			"freq_capped":            h.Throttled.FreqCappedNow,
			"throttled":              h.Throttled.ThrottledNow,
			"soft_temp_limit":        h.Throttled.SoftTempLimitNow,
		},
		"mem_free_mb":     h.MemFreeMB,
		"uptime_s":        h.UptimeS,
		"rootfs_used_pct": h.RootFSUsedPct,
		"tmpfs_used_pct":  h.TmpfsUsedPct,
		"time": map[string]any{
			"synced": ts.Synced, "src": ts.Source, "offset_ms": ts.Offset.Milliseconds(),
		},
		"outbox":     s.opts.Status.Outbox(ctx),
		"usb_resets": resets,
	})
}

// ---- stations -------------------------------------------------------------

type stationView struct {
	Key       string  `json:"key"`
	Model     string  `json:"model"`
	ID        int     `json:"id"`
	Channel   *int    `json:"channel"`
	LastSeen  string  `json:"last_seen"`
	RSSIdB    float64 `json:"rssi_db"`
	SNRdB     float64 `json:"snr_db"`
	BatteryOK bool    `json:"battery_ok"`
	// Fields is the effective selected set: the model default with the
	// owner's overrides applied (config.Station.EffectiveFields). For a
	// station that is not selected it shows what would be used if it were.
	Fields []string `json:"fields"`
	// FieldsAvailable is every canonical field the station has reported and
	// RawFields the unmapped decoder names seen.
	FieldsAvailable []string `json:"fields_available"`
	RawFields       []string `json:"raw_fields"`
	// FieldsDefault is the model default for the fields seen.
	FieldsDefault []string `json:"fields_default"`
	IncludeRaw    bool     `json:"include_raw"`
	NSightings    int      `json:"n_sightings"`
	Selected      bool     `json:"selected"`
	Name          string   `json:"name"`
	AltM          *float64 `json:"alt_m"`
	WindHeightM   *float64 `json:"wind_height_m"`
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return append([]string(nil), s...)
}

func mergeStation(info StationInfo, cfg map[string]config.Station) stationView {
	v := stationView{
		Key: info.Key, Model: info.Model, ID: info.ID, Channel: info.Channel,
		LastSeen: info.LastSeen.UTC().Format(time.RFC3339), RSSIdB: info.RSSIdB, SNRdB: info.SNRdB,
		BatteryOK: info.BatteryOK, NSightings: info.NSightings,
	}
	v.FieldsAvailable = nonNil(info.Fields)
	v.RawFields = nonNil(info.RawFields)
	v.FieldsDefault = mapping.DefaultFields(info.Model, info.Fields)
	c, ok := cfg[info.Key]
	if ok {
		v.Selected, v.Name = c.Selected, c.Name
		v.AltM, v.WindHeightM = &c.AltM, &c.WindHeightM
	}
	v.Fields = c.EffectiveFields(v.FieldsDefault)
	v.IncludeRaw = c.RawIncluded()
	return v
}

func (s *Server) handleStations(w http.ResponseWriter, r *http.Request) {
	infos, err := s.opts.Stations.Stations(r.Context())
	if err != nil {
		s.log.Error("web: list stations", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "could not list stations")
		return
	}
	cfg := s.opts.Config.Get().Stations
	out := make([]stationView, 0, len(infos))
	for i := range infos {
		out = append(out, mergeStation(infos[i], cfg))
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Key < out[b].Key })
	writeJSON(w, http.StatusOK, out)
}

// nullableFloat distinguishes an absent JSON field from an explicit null.
type nullableFloat struct {
	set bool
	val float64
}

func (n *nullableFloat) UnmarshalJSON(b []byte) error {
	n.set = true
	if string(b) == "null" {
		n.val = 0
		return nil
	}
	return json.Unmarshal(b, &n.val)
}

// selectionPatch is the field selection part of PUT /api/stations/{key}.
type selectionPatch struct {
	fields      map[string]bool
	clearFields bool
	rawSet      bool
	raw         *bool
}

// parseSelectionPatch decodes the fields and include_raw members. It writes
// the 400 response itself and returns false on a malformed value.
func parseSelectionPatch(w http.ResponseWriter, fields, includeRaw json.RawMessage) (selectionPatch, bool) {
	var p selectionPatch
	if fields != nil {
		if string(fields) == "null" {
			p.clearFields = true
		} else if err := json.Unmarshal(fields, &p.fields); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "fields must be an object of field name to true or false")
			return p, false
		}
	}
	if includeRaw != nil {
		if err := json.Unmarshal(includeRaw, &p.raw); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "include_raw must be true, false or null")
			return p, false
		}
		p.rawSet = true
	}
	return p, true
}

// apply merges the patch into st: field overrides are merged key by key, null
// fields clears them, and include_raw null returns to the default rule.
func (p selectionPatch) apply(st *config.Station) {
	if p.clearFields {
		st.Fields = nil
	}
	if len(p.fields) > 0 {
		merged := make(map[string]bool, len(st.Fields)+len(p.fields))
		for k, v := range st.Fields {
			merged[k] = v
		}
		for k, v := range p.fields {
			merged[k] = v
		}
		st.Fields = merged
	}
	if p.rawSet {
		st.IncludeRaw = p.raw
	}
}

func (s *Server) handleStationPut(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var in struct {
		Selected    *bool         `json:"selected"`
		Name        *string       `json:"name"`
		AltM        nullableFloat `json:"alt_m"`
		WindHeightM nullableFloat `json:"wind_height_m"`
		// Fields is a partial object of canonical name to bool merged into
		// the station's overrides; null clears every override (back to the
		// model default). IncludeRaw null returns to the default rule.
		Fields     json.RawMessage `json:"fields"`
		IncludeRaw json.RawMessage `json:"include_raw"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	infos, err := s.opts.Stations.Stations(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not list stations")
		return
	}
	var info *StationInfo
	for i := range infos {
		if infos[i].Key == key {
			info = &infos[i]
		}
	}
	patch, ok := parseSelectionPatch(w, in.Fields, in.IncludeRaw)
	if !ok {
		return
	}
	_, configured := s.opts.Config.Get().Stations[key]
	if info == nil && !configured {
		writeErr(w, http.StatusNotFound, "not_found", "no station with key "+key)
		return
	}
	cfg, err := s.opts.Config.Update(func(c *config.Config) error {
		st := c.Stations[key]
		if in.Selected != nil {
			st.Selected = *in.Selected
		}
		patch.apply(&st)
		if in.Name != nil {
			st.Name = *in.Name
		}
		if in.AltM.set {
			st.AltM = in.AltM.val
		}
		if in.WindHeightM.set {
			st.WindHeightM = in.WindHeightM.val
		}
		c.Stations[key] = st
		return nil
	})
	if err != nil {
		s.configError(w, err)
		return
	}
	if info == nil {
		info = &StationInfo{Key: key}
	}
	writeJSON(w, http.StatusOK, mergeStation(*info, cfg.Stations))
}

// ---- radios ---------------------------------------------------------------

type radioView struct {
	Serial  string  `json:"serial"`
	Bus     string  `json:"bus"`
	Product string  `json:"product"`
	Role    string  `json:"role"`
	BandMHz float64 `json:"band_mhz"`
	Status  string  `json:"status"`
}

const defaultBandMHz = 433.92

func (s *Server) radioViews(ctx context.Context) ([]radioView, error) {
	devs, err := s.opts.Platform.USBDevices(ctx)
	if err != nil && !errors.Is(err, platform.ErrUnsupported) {
		return nil, err
	}
	cfg := map[string]config.Radio{}
	for _, r := range s.opts.Config.Get().Radios {
		cfg[r.Serial] = r
	}
	out := []radioView{}
	seen := map[string]bool{}
	for _, d := range devs {
		if !d.IsRTLSDR() {
			continue
		}
		serial := d.Serial
		if serial == "" {
			serial = d.Port
		}
		if seen[serial] {
			continue
		}
		seen[serial] = true
		product := d.Product
		if product == "" {
			product = d.RTLSDRName()
		}
		v := radioView{
			Serial: serial, Bus: fmt.Sprintf("%03d:%03d", d.Bus, d.Dev), Product: product,
			Role: config.RoleRTL433, BandMHz: defaultBandMHz,
		}
		if c, ok := cfg[serial]; ok {
			v.Role, v.BandMHz = c.Role, c.BandMHz
		}
		v.Status = s.radioStatus(ctx, v)
		out = append(out, v)
	}
	for serial, c := range cfg {
		if !seen[serial] {
			out = append(out, radioView{Serial: serial, Role: c.Role, BandMHz: c.BandMHz, Status: "missing"})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Serial < out[b].Serial })
	return out, nil
}

func (s *Server) radioStatus(ctx context.Context, v radioView) string {
	if v.Role == config.RoleDisabled {
		return "idle"
	}
	if st := s.opts.Status.RadioStatus(ctx, v.Serial); st != "" {
		return st
	}
	return "unknown"
}

func (s *Server) handleRadios(w http.ResponseWriter, r *http.Request) {
	out, err := s.radioViews(r.Context())
	if err != nil {
		s.log.Error("web: list radios", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "could not enumerate USB devices")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRadioPut(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	var in struct {
		Role    string  `json:"role"`
		BandMHz float64 `json:"band_mhz"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Role != config.RoleRTL433 && in.Role != config.RoleDisabled {
		writeErr(w, http.StatusBadRequest, "bad_request", "role must be rtl_433 or disabled")
		return
	}
	if in.BandMHz != 433.92 && in.BandMHz != 915 {
		writeErr(w, http.StatusBadRequest, "bad_request", "band_mhz must be 433.92 or 915")
		return
	}
	views, err := s.radioViews(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not enumerate USB devices")
		return
	}
	found := false
	for _, v := range views {
		found = found || v.Serial == serial
	}
	if !found {
		writeErr(w, http.StatusNotFound, "not_found", "no radio with serial "+serial)
		return
	}
	_, err = s.opts.Config.Update(func(c *config.Config) error {
		for i := range c.Radios {
			if c.Radios[i].Serial == serial {
				c.Radios[i].Role, c.Radios[i].BandMHz = in.Role, in.BandMHz
				return nil
			}
		}
		c.Radios = append(c.Radios, config.Radio{Serial: serial, Role: in.Role, BandMHz: in.BandMHz})
		return nil
	})
	if err != nil {
		s.configError(w, err)
		return
	}
	views, err = s.radioViews(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not enumerate USB devices")
		return
	}
	for _, v := range views {
		if v.Serial == serial {
			writeJSON(w, http.StatusOK, v)
			return
		}
	}
	writeErr(w, http.StatusNotFound, "not_found", "no radio with serial "+serial)
}

// ---- config ---------------------------------------------------------------

func (s *Server) configError(w http.ResponseWriter, err error) {
	var ve *config.ValidationError
	var de *config.DecodeError
	switch {
	case errors.As(err, &ve):
		writeErr(w, http.StatusUnprocessableEntity, "invalid_config", strings.Join(ve.Problems, "; "))
	case errors.As(err, &de):
		writeErr(w, http.StatusBadRequest, "bad_request", de.Error())
	case errors.Is(err, config.ErrUnknownSection):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, config.ErrCommitInProgress):
		writeErr(w, http.StatusConflict, "commit_in_progress", err.Error())
	default:
		s.log.Error("web: config update", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "could not save the configuration")
	}
}

// expireNetwork rolls back an applied network change whose confirmation
// deadline has passed. Nothing else is running a timer for it.
func (s *Server) expireNetwork() {
	n := s.opts.Config.Get().Network
	if n.ConfirmDeadline == nil || !s.now().After(*n.ConfirmDeadline) {
		return
	}
	_, err := s.opts.Config.Update(func(c *config.Config) error {
		if c.Network.ConfirmDeadline != nil && s.now().After(*c.Network.ConfirmDeadline) {
			c.Network.Pending, c.Network.ConfirmDeadline = nil, nil
		}
		return nil
	})
	if err != nil {
		s.log.Warn("web: expire network change", "err", err)
	}
}

func (s *Server) pendingDeadline() *string {
	s.expireNetwork()
	d := s.opts.Config.Get().Network.ConfirmDeadline
	if d == nil {
		return nil
	}
	v := d.UTC().Format(time.RFC3339)
	return &v
}

func (s *Server) handleConfigGet(w http.ResponseWriter, _ *http.Request) {
	pending := s.pendingDeadline()
	raw, err := json.Marshal(s.opts.Config.Get())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "")
		return
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "")
		return
	}
	m["pending_confirm"] = pending
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	section := r.PathValue("section")
	var raw json.RawMessage
	if !decodeBody(w, r, &raw) {
		return
	}
	cfg, err := s.opts.Config.Apply(section, raw)
	if err != nil {
		s.configError(w, err)
		return
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "")
		return
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "")
		return
	}
	writeJSON(w, http.StatusOK, m[section])
}

func (s *Server) handleConfigPending(w http.ResponseWriter, _ *http.Request) {
	d := s.pendingDeadline()
	writeJSON(w, http.StatusOK, map[string]any{"pending_confirm": d != nil, "confirm_deadline": d})
}

// handleConfigApply starts the confirmation window for the staged network
// candidate. Pushing the settings to the host is the network adapter's job and
// does not exist in v1; until it does the window only gates the config swap.
func (s *Server) handleConfigApply(w http.ResponseWriter, _ *http.Request) {
	s.expireNetwork()
	var deadline time.Time
	_, err := s.opts.Config.Update(func(c *config.Config) error {
		if c.Network.ConfirmDeadline != nil {
			return errAlreadyApplied
		}
		if c.Network.Pending == nil {
			return errNothingStaged
		}
		deadline = s.now().Add(s.opts.ConfirmWindow).UTC()
		c.Network.ConfirmDeadline = &deadline
		return nil
	})
	switch {
	case errors.Is(err, errNothingStaged):
		writeErr(w, http.StatusConflict, "nothing_pending", "there is no staged change to apply")
	case errors.Is(err, errAlreadyApplied):
		writeErr(w, http.StatusConflict, "already_applied", "a change is already awaiting confirmation")
	case err != nil:
		s.configError(w, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"applied": true, "pending_confirm": true, "confirm_deadline": deadline.Format(time.RFC3339),
		})
	}
}

var (
	errNothingStaged  = errors.New("nothing staged")
	errAlreadyApplied = errors.New("already applied")
	errNothingPending = errors.New("nothing pending")
)

func (s *Server) handleConfigConfirm(w http.ResponseWriter, _ *http.Request) {
	s.expireNetwork()
	_, err := s.opts.Config.Update(func(c *config.Config) error {
		if c.Network.ConfirmDeadline == nil || c.Network.Pending == nil {
			return errNothingPending
		}
		c.Network.Current = *c.Network.Pending
		c.Network.Pending, c.Network.ConfirmDeadline = nil, nil
		return nil
	})
	switch {
	case errors.Is(err, errNothingPending):
		writeErr(w, http.StatusConflict, "nothing_pending", "there is no change waiting for confirmation")
	case err != nil:
		s.configError(w, err)
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"confirmed": true})
	}
}

// ---- uplink, update, logs, readings, version ------------------------------

func (s *Server) handleUplink(w http.ResponseWriter, r *http.Request) {
	st := s.opts.Status.Uplink(r.Context())
	cfg := s.opts.Config.Get().Uplink
	if st.Broker == "" {
		st.Broker = cfg.BrokerURL
	}
	if st.DeviceID == "" {
		st.DeviceID = cfg.DeviceID
	}
	var last *string
	if st.LastPublish != nil {
		v := st.LastPublish.UTC().Format(time.RFC3339)
		last = &v
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"connected": st.Connected, "broker": st.Broker, "device_id": st.DeviceID,
		"last_publish": last, "outbox": s.opts.Status.Outbox(r.Context()),
	})
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.opts.Status.Update(r.Context()))
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	n := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 {
			writeErr(w, http.StatusBadRequest, "bad_request", "lines must be a positive integer")
			return
		}
		n = min(p, 2000)
	}
	var lines []string
	if s.opts.Logs != nil {
		lines = s.opts.Logs.Tail(n)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	text := strings.Join(lines, "\n")
	if text != "" {
		text += "\n"
	}
	if _, err := w.Write([]byte(text)); err != nil {
		s.log.Debug("web: write logs", "err", err)
	}
}

func (s *Server) handleReadings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	key := q.Get("station")
	if key == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "station is required")
		return
	}
	since := s.now().Add(-time.Hour)
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "since must be RFC 3339")
			return
		}
		since = t
	}
	rs, err := s.opts.Readings.Readings(r.Context(), key, since)
	if err != nil {
		s.log.Error("web: readings", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "could not read readings")
		return
	}
	if rs == nil {
		rs = []Reading{}
	}
	writeJSON(w, http.StatusOK, rs)
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version": s.opts.Version, "commit": s.opts.Commit, "build_date": s.opts.BuildDate,
	})
}
