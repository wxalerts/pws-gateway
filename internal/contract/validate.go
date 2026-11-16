package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/wxalerts/pws-gateway/schemas"
)

// Validation errors. Use errors.Is to test for them; a *SchemaError wraps
// the detailed cause from the schema library.
var (
	ErrTooLarge    = errors.New("contract: message exceeds 64 KB")
	ErrInvalidJSON = errors.New("contract: payload is not valid JSON")
	ErrUnknownKind = errors.New("contract: unknown message kind")
)

// SchemaError reports that a payload parsed as JSON but did not satisfy the
// schema for its kind.
type SchemaError struct {
	Kind  Kind
	Cause *jsonschema.ValidationError
}

func (e *SchemaError) Error() string {
	return fmt.Sprintf("contract: %s payload does not match schema: %v", e.Kind, e.Cause)
}

func (e *SchemaError) Unwrap() error { return e.Cause }

var compileOnce = sync.OnceValues(compileAll)

// compileAll loads every embedded schema into one compiler so that $ref
// between files resolves without touching the network, then compiles the
// schema of each message kind.
func compileAll() (map[Kind]*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()

	entries, err := fs.ReadDir(schemas.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("contract: list embedded schemas: %w", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".schema.json") {
			continue
		}
		raw, err := schemas.FS.ReadFile(e.Name())
		if err != nil {
			return nil, fmt.Errorf("contract: read %s: %w", e.Name(), err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("contract: parse %s: %w", e.Name(), err)
		}
		if err := c.AddResource(schemas.BaseURL+e.Name(), doc); err != nil {
			return nil, fmt.Errorf("contract: add %s: %w", e.Name(), err)
		}
	}

	out := make(map[Kind]*jsonschema.Schema, len(Kinds))
	for _, k := range Kinds {
		sch, err := c.Compile(SchemaURL(k))
		if err != nil {
			return nil, fmt.Errorf("contract: compile %s schema: %w", k, err)
		}
		out[k] = sch
	}
	return out, nil
}

// SchemaURL returns the $id of the schema for kind.
func SchemaURL(kind Kind) string {
	return schemas.BaseURL + string(kind) + ".schema.json"
}

// Schema returns the compiled schema for kind. It compiles all schemas on
// first use.
func Schema(kind Kind) (*jsonschema.Schema, error) {
	all, err := compileOnce()
	if err != nil {
		return nil, err
	}
	sch, ok := all[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}
	return sch, nil
}

// Validate checks that data is a well formed payload of the given kind: at
// most MaxMessageBytes, valid JSON, and conforming to the kind's schema.
func Validate(kind Kind, data []byte) error {
	if len(data) > MaxMessageBytes {
		return fmt.Errorf("%w: %d bytes", ErrTooLarge, len(data))
	}
	sch, err := Schema(kind)
	if err != nil {
		return err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	if err := sch.Validate(doc); err != nil {
		var ve *jsonschema.ValidationError
		if errors.As(err, &ve) {
			return &SchemaError{Kind: kind, Cause: ve}
		}
		return fmt.Errorf("contract: validate %s: %w", kind, err)
	}
	return nil
}

// Marshal encodes m as JSON and validates the result against its schema, so a
// device never publishes a payload the server would reject.
func Marshal(m Message) ([]byte, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("contract: marshal %s: %w", m.Kind(), err)
	}
	if err := Validate(m.Kind(), data); err != nil {
		return nil, err
	}
	return data, nil
}

// Unmarshal validates data against the schema for m's kind and then decodes
// it into m. Unknown fields are ignored, as the contract requires.
func Unmarshal(data []byte, m Message) error {
	if err := Validate(m.Kind(), data); err != nil {
		return err
	}
	if err := json.Unmarshal(data, m); err != nil {
		return fmt.Errorf("contract: decode %s: %w", m.Kind(), err)
	}
	if c, ok := m.(*Command); ok {
		input, err := signingInputFromJSON(data)
		if err != nil {
			return err
		}
		c.signingInput = input
	}
	return nil
}

// Validate marshals the message and checks it against the obs schema.
func (o *Obs) Validate() error { _, err := Marshal(o); return err }

// Validate marshals the message and checks it against the tel schema.
func (t *Tel) Validate() error { _, err := Marshal(t); return err }

// Validate marshals the message and checks it against the update schema.
func (u *Update) Validate() error { _, err := Marshal(u); return err }

// Validate marshals the message and checks it against the event schema.
func (e *Event) Validate() error { _, err := Marshal(e); return err }

// Validate marshals the message and checks it against the status schema.
func (s Status) Validate() error { _, err := Marshal(&s); return err }

// Validate marshals the message and checks it against the cmd schema. It
// does not check the signature; use VerifyCommand for that.
func (c *Command) Validate() error { _, err := Marshal(c); return err }

// Validate marshals the message and checks it against the ack schema.
func (a *Ack) Validate() error { _, err := Marshal(a); return err }

// ParseCommand validates and decodes a command payload and records the
// signing input derived from the exact bytes received. It does not verify
// the signature; call VerifyCommand next.
func ParseCommand(data []byte) (*Command, error) {
	var c Command
	if err := Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
