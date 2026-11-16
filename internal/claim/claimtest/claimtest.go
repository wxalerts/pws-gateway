// Package claimtest is an httptest fake of the WxAlerts claim endpoints, for
// the claim, app and web tests. It answers in the shapes documented in the
// API repository (docs/pws.md).
package claimtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Fake is the fake API. Script the poll answers with Queue, then read what
// the device sent from the exported counters.
type Fake struct {
	*httptest.Server

	mu sync.Mutex
	// StartStatus, when not 0 or 200, is the status of start (with
	// StartError as the error code).
	StartStatus int
	StartError  string
	// ExpiresIn and Interval are the start reply values in server seconds.
	ExpiresIn int
	Interval  int
	// BrokerURL is the mqtt.url of the success reply.
	BrokerURL string

	hwids       []string
	starts      int
	pollTimes   []time.Time
	pollCodes   []string
	queue       []reply
	deviceCodes int
}

type reply struct {
	status int
	body   map[string]any
}

// New starts the fake and registers its cleanup.
func New(t *testing.T) *Fake {
	t.Helper()
	f := &Fake{ExpiresIn: 600, Interval: 5, BrokerURL: "wss://pws-mqtt.wxalerts.org/mqtt"}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /pws/claim/start", f.start)
	mux.HandleFunc("POST /pws/claim/poll", f.poll)
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func write(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (f *Fake) start(w http.ResponseWriter, r *http.Request) {
	var in struct {
		HWID string `json:"hw_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.HWID) < 8 {
		write(w, http.StatusUnprocessableEntity, map[string]any{"detail": "hw_id"})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	f.hwids = append(f.hwids, in.HWID)
	if f.StartStatus != 0 && f.StartStatus != http.StatusOK {
		if f.StartStatus == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "30")
		}
		write(w, f.StartStatus, map[string]any{"error": f.StartError})
		return
	}
	f.deviceCodes++
	write(w, http.StatusOK, map[string]any{
		"device_code":               "device-code-secret-" + string(rune('a'+f.deviceCodes)) + "xxxxxxxxxxxxxxxx",
		"user_code":                 "K7MP-2XQD",
		"verification_uri":          "https://wxalerts.org/pws/claim",
		"verification_uri_complete": "https://wxalerts.org/pws/claim?code=K7MP2XQD",
		"expires_in":                f.ExpiresIn,
		"interval":                  f.Interval,
	})
}

func (f *Fake) poll(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DeviceCode string `json:"device_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		write(w, http.StatusBadRequest, map[string]any{"error": "bad_request"})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pollTimes = append(f.pollTimes, time.Now())
	f.pollCodes = append(f.pollCodes, in.DeviceCode)
	if len(f.queue) == 0 {
		write(w, http.StatusBadRequest, map[string]any{"error": "authorization_pending"})
		return
	}
	next := f.queue[0]
	f.queue = f.queue[1:]
	write(w, next.status, next.body)
}

func (f *Fake) push(status int, body map[string]any) {
	f.mu.Lock()
	f.queue = append(f.queue, reply{status, body})
	f.mu.Unlock()
}

// Pending queues one authorization_pending answer.
func (f *Fake) Pending() {
	f.push(http.StatusBadRequest, map[string]any{"error": "authorization_pending"})
}

// SlowDown queues a slow_down answer carrying the new interval.
func (f *Fake) SlowDown(interval int) {
	f.push(http.StatusBadRequest, map[string]any{"error": "slow_down", "interval": interval})
}

// Expired queues an expired_token answer.
func (f *Fake) Expired() { f.push(http.StatusBadRequest, map[string]any{"error": "expired_token"}) }

// Denied queues an access_denied answer.
func (f *Fake) Denied() { f.push(http.StatusBadRequest, map[string]any{"error": "access_denied"}) }

// Outage queues a 503.
func (f *Fake) Outage() {
	f.push(http.StatusServiceUnavailable, map[string]any{"error": "unavailable"})
}

// Approve queues the success answer: the owner approved the code.
func (f *Fake) Approve(deviceID, password string) {
	f.push(http.StatusOK, map[string]any{
		"device_id": deviceID,
		"mqtt": map[string]any{
			"url": f.BrokerURL, "host": "pws-mqtt.wxalerts.org", "port": 443, "transport": "wss",
			"path": "/mqtt", "username": deviceID, "client_id": deviceID, "password": password,
		},
		"credential_version": 1,
	})
}

// HWIDs returns every hw_id start received.
func (f *Fake) HWIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.hwids...)
}

// PollTimes returns when each poll arrived.
func (f *Fake) PollTimes() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Time(nil), f.pollTimes...)
}

// PollCodes returns the device_code of each poll.
func (f *Fake) PollCodes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.pollCodes...)
}
