// Package rtl433 runs the rtl_433 process and understands its JSON output.
//
// rtl_433 is GPL and runs as a separate process, never linked in. This package
// only speaks to it through argv, stdout and stderr. The Supervisor keeps it
// alive, the Decoder parses one output line, and Replay feeds recorded lines
// through the same path so tests and demos need no radio.
//
// The output format is documented in rtl_433's docs/DATA_FORMAT.md. Field
// names were checked against that document and against the decoder sources
// in src/devices on 2026-11-16; see the mapping package for the list.
package rtl433

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

var (
	// ErrEmptyLine is returned for blank input, which rtl_433 never writes
	// but editors and recordings often do.
	ErrEmptyLine = errors.New("rtl433: empty line")
	// ErrNotObject is returned when a line parses but is not a JSON object.
	ErrNotObject = errors.New("rtl433: line is not a JSON object")
	// ErrNoModel is returned when the object has no "model" string. Every
	// decoder output has one; its absence means the line is not a decode.
	ErrNoModel = errors.New("rtl433: line has no model")
)

// Message is one decoded rtl_433 line. Data keeps every key except "time",
// with numbers as float64, so unknown fields survive to the raw passthrough.
type Message struct {
	// Time is the embedded timestamp converted to UTC. HasTime is false when
	// the line had none or it did not parse; Time is then the zero value.
	Time    time.Time
	HasTime bool

	Model string
	// ID and Channel are formatted as strings because rtl_433 emits them as
	// integers for most devices and as strings ("A", "0x1F") for some.
	ID      string
	Channel string

	Data map[string]any
}

// Float returns a numeric field.
func (m *Message) Float(key string) (float64, bool) {
	v, ok := m.Data[key].(float64)
	return v, ok
}

// String returns a string field.
func (m *Message) String(key string) (string, bool) {
	v, ok := m.Data[key].(string)
	return v, ok
}

// Decoder parses rtl_433 JSON lines.
type Decoder struct {
	// Location is used for timestamps that carry no zone, which is what the
	// default rtl_433 time format produces. nil means time.Local. Lines
	// produced with "-M time:iso:usec:utc:tz" carry a Z and ignore this.
	Location *time.Location
}

// Decode parses one line. Unknown keys are kept, nested values are kept as
// decoded by encoding/json, and only "model" is required.
func (d *Decoder) Decode(line []byte) (*Message, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, ErrEmptyLine
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var raw map[string]json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		if line[0] != '{' {
			return nil, ErrNotObject
		}
		return nil, fmt.Errorf("rtl433: parse: %w", err)
	}

	msg := &Message{Data: make(map[string]any, len(raw))}
	for k, v := range raw {
		val, err := decodeValue(v)
		if err != nil {
			return nil, fmt.Errorf("rtl433: field %q: %w", k, err)
		}
		switch k {
		case "time":
			if s, ok := val.(string); ok {
				if t, err := ParseTime(s, d.Location); err == nil {
					msg.Time = t
					msg.HasTime = true
				}
			}
		case "model":
			s, ok := val.(string)
			if !ok {
				return nil, ErrNoModel
			}
			msg.Model = s
			msg.Data[k] = val
		case "id":
			msg.ID = scalarString(val)
			msg.Data[k] = val
		case "channel":
			msg.Channel = scalarString(val)
			msg.Data[k] = val
		default:
			msg.Data[k] = val
		}
	}
	if msg.Model == "" {
		return nil, ErrNoModel
	}
	return msg, nil
}

// decodeValue turns a raw JSON value into float64, string, bool, nil, or the
// generic encoding/json containers for arrays and objects. Numbers become
// float64 so that every numeric field has one type; integer ids are
// formatted from the original text to avoid float rounding.
func decodeValue(v json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(v))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return convertNumbers(out), nil
}

func convertNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return x.String()
		}
		return f
	case []any:
		for i := range x {
			x[i] = convertNumbers(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = convertNumbers(x[k])
		}
		return x
	default:
		return v
	}
}

// scalarString formats an id or channel. Whole numbers print without a
// fraction, so id 2342 becomes "2342" and not "2342.000000".
func scalarString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "1"
		}
		return "0"
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}
