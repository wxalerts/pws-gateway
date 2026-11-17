package uplink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/platform"
)

// commandTypes is the closed set of command types a v1 device dispatches. A
// topic outside it is dropped before the payload is even parsed. There is no
// shell, install or file-write type, and no handler below passes any part of
// a body to a process (docs/adr/0006-signed-commands-no-shell.md).
var commandTypes = []string{
	contract.CmdConfig, contract.CmdReboot, contract.CmdBundle,
	contract.CmdUpdateCheck, contract.CmdResync, contract.CmdDiag, contract.CmdCapture,
}

// diagActions is the closed diag allow list. It must equal the actions in the
// contract; a test asserts that.
var diagActions = []string{
	contract.DiagRestartService, contract.DiagRediscover,
	contract.DiagConnectivityTest, contract.DiagClearOutbox,
}

// Command failure texts used in acks.
const (
	errNotImplemented = "not_implemented"
	errCaptureV2      = "capture is not accepted in v1"
)

// onMessage is called by the MQTT client for every inbound publish. It only
// copies the message to the worker: verification and dispatch run off the
// client goroutine.
func (u *Uplink) onMessage(topic string, payload []byte) {
	if len(payload) > maxCommandSize {
		u.log.Warn("ignored oversized message", "topic", topic, "bytes", len(payload))
		return
	}
	select {
	case u.cmds <- queuedCommand{topic: topic, payload: append([]byte(nil), payload...)}:
	default:
		u.log.Warn("command queue full, message dropped", "topic", topic)
	}
}

func (u *Uplink) commandWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-u.cmds:
			u.handleCommand(ctx, c.topic, c.payload)
		}
	}
}

// handleCommand runs the full pipeline for one inbound message: topic check,
// closed type check, schema, device_id, expiry, signature, replay, dispatch.
// Anything that fails a check is logged at warn and ignored without an ack.
func (u *Uplink) handleCommand(ctx context.Context, topic string, payload []byte) {
	pt, err := contract.ParseTopic(topic)
	if err != nil || pt.Kind != contract.KindCmd || pt.DeviceID != u.opts.DeviceID {
		u.log.Warn("ignored message on unexpected topic", "topic", topic)
		return
	}
	if !slices.Contains(commandTypes, pt.Suffix) {
		u.log.Warn("ignored command of unknown type", "type", pt.Suffix)
		return
	}
	if u.opts.CommandKey == nil {
		if u.warnedNoKey.CompareAndSwap(false, true) {
			u.log.Warn("ignored command: no command-signing key is pinned", "type", pt.Suffix)
		}
		return
	}
	cmd, err := contract.ParseCommand(payload)
	if err != nil {
		u.log.Warn("ignored malformed command", "type", pt.Suffix, "err", err)
		return
	}
	if cmd.Type != pt.Suffix {
		u.log.Warn("ignored command whose type differs from its topic", "topic_type", pt.Suffix, "id", cmd.ID)
		return
	}
	err = contract.VerifyCommand(u.opts.CommandKey, cmd, contract.VerifyOptions{
		DeviceID: u.opts.DeviceID, Now: u.now, Replay: u.replay,
	})
	if err != nil {
		u.log.Warn("ignored command that failed verification", "type", cmd.Type, "id", cmd.ID, "err", err)
		return
	}
	u.log.Info("command accepted", "type", cmd.Type, "id", cmd.ID)
	result, derr := u.dispatch(ctx, cmd)
	u.ack(ctx, cmd, result, derr)
}

// dispatch runs the handler for a verified command. It returns the ack result
// and the failure, if any.
func (u *Uplink) dispatch(ctx context.Context, cmd *contract.Command) (map[string]any, error) {
	switch cmd.Type {
	case contract.CmdConfig:
		return u.cmdConfig(cmd.Body)
	case contract.CmdReboot:
		return u.cmdReboot(ctx)
	case contract.CmdResync:
		return u.cmdResync(ctx, cmd.Body)
	case contract.CmdDiag:
		return u.cmdDiag(ctx, cmd.Body)
	case contract.CmdUpdateCheck:
		if u.opts.UpdateCheck == nil {
			return nil, errors.New(errNotImplemented)
		}
		return u.opts.UpdateCheck(ctx)
	case contract.CmdBundle:
		return nil, errors.New(errNotImplemented)
	case contract.CmdCapture:
		return nil, errors.New(errCaptureV2)
	default:
		// Unreachable: commandTypes was checked on the topic and the schema
		// pins the type enum.
		return nil, fmt.Errorf("unsupported command type %q", cmd.Type)
	}
}

// ack queues the command result on ack/{id}.
func (u *Uplink) ack(ctx context.Context, cmd *contract.Command, result map[string]any, derr error) {
	a := &contract.Ack{
		V: contract.Version, ID: cmd.ID, DeviceID: u.opts.DeviceID, Type: cmd.Type,
		OK: derr == nil, TS: u.now().UTC().Truncate(time.Second),
	}
	if derr != nil {
		msg := derr.Error()
		if len(msg) > 300 {
			msg = msg[:300]
		}
		a.Error = &msg
	}
	if len(result) > 0 {
		if b, err := json.Marshal(result); err == nil {
			a.Result = b
		}
	}
	if err := u.publishMessage(ctx, contract.AckTopic(u.opts.DeviceID, cmd.ID), a); err != nil {
		u.log.Error("queue ack failed", "id", cmd.ID, "err", err)
	}
}

// cmdConfig applies a config patch. The body maps section names to section
// values exactly like PUT /api/config/{section}, through the same decoding and
// validation, and either every section applies or none does.
func (u *Uplink) cmdConfig(body json.RawMessage) (map[string]any, error) {
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(body, &sections); err != nil {
		return nil, fmt.Errorf("config body is not an object of sections: %w", err)
	}
	if len(sections) == 0 {
		return nil, errors.New("config body has no sections")
	}
	if _, err := u.opts.Config.ApplySections(sections); err != nil {
		return nil, err
	}
	applied := make([]string, 0, len(sections))
	for k := range sections {
		applied = append(applied, k)
	}
	slices.Sort(applied)
	return map[string]any{"applied": applied}, nil
}

func (u *Uplink) cmdReboot(ctx context.Context) (map[string]any, error) {
	if err := u.opts.Platform.Reboot(ctx); err != nil {
		if errors.Is(err, platform.ErrUnsupported) {
			return nil, errors.New("reboot is not supported on this host")
		}
		return nil, fmt.Errorf("reboot failed: %w", err)
	}
	return map[string]any{"rebooting": true}, nil
}

// cmdDiag validates the action against the closed allow list and hands it to
// the configured handler. With no handler every action answers not_implemented.
func (u *Uplink) cmdDiag(ctx context.Context, body json.RawMessage) (map[string]any, error) {
	var b contract.DiagBody
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, fmt.Errorf("diag body: %w", err)
	}
	if !slices.Contains(diagActions, b.Action) {
		return nil, fmt.Errorf("diag action %q is not on the allow list", b.Action)
	}
	if u.opts.DiagHandler == nil {
		return nil, errors.New(errNotImplemented)
	}
	return u.opts.DiagHandler(ctx, b)
}

// cmdResync re-enqueues the stored buckets of the selected stations whose
// start lies in [from, to] (to defaults to now), packed like a backlog.
func (u *Uplink) cmdResync(ctx context.Context, body json.RawMessage) (map[string]any, error) {
	var b contract.ResyncBody
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, fmt.Errorf("resync body: %w", err)
	}
	if !u.timeSynced(ctx) {
		return nil, errors.New("clock is not synced")
	}
	now := u.now().UTC()
	to := now
	if b.To != nil {
		to = b.To.UTC()
	}
	from := b.From.UTC()
	if to.Before(from) {
		return nil, errors.New("resync range is empty: to is before from")
	}
	if to.Sub(from) > maxResync {
		return nil, fmt.Errorf("resync range is longer than %d days", int(maxResync/(24*time.Hour)))
	}
	cfg := u.opts.Config.Get()
	keys := selectedKeys(cfg)
	if len(keys) == 0 {
		return nil, errors.New("no station is selected")
	}

	u.buildMu.Lock()
	defer u.buildMu.Unlock()
	buckets, err := u.loadBuckets(ctx, cfg, keys, from, to.Add(time.Second)) // store times have whole second precision
	if err != nil {
		return nil, err
	}
	st, err := u.loadState(ctx)
	if err != nil {
		return nil, err
	}
	msgs, err := u.emitObs(ctx, st, buckets, now, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{"buckets": len(buckets), "messages": msgs}, nil
}
