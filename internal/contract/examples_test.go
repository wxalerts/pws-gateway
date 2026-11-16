package contract_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/contract/conformance"
)

const examplesRoot = "../../schemas/examples"

func loadExamples(t *testing.T) []conformance.Example {
	t.Helper()
	examples, err := conformance.LoadExamples(examplesRoot)
	if err != nil {
		t.Fatalf("load examples: %v", err)
	}
	if len(examples) == 0 {
		t.Fatal("no examples found")
	}
	return examples
}

// TestValidExamples checks that every file under valid/ passes schema
// validation, decodes into its Go type, re-encodes to a document that also
// passes, and loses nothing but x_ probes and empty optional members.
func TestValidExamples(t *testing.T) {
	for _, ex := range loadExamples(t) {
		if !ex.Valid {
			continue
		}
		t.Run(ex.Name, func(t *testing.T) {
			if err := contract.Validate(ex.Kind, ex.Data); err != nil {
				t.Fatalf("%s should validate: %v", ex.Path, err)
			}
			out, err := conformance.RoundTrip(ex)
			if err != nil {
				t.Fatalf("%s round trip through Go type: %v", ex.Path, err)
			}
			diffs, err := conformance.Diff(ex.Data, out)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range diffs {
				t.Errorf("%s: round trip changed the document: %s", ex.Path, d)
			}
		})
	}
}

// TestInvalidExamples checks that every file under invalid/ is rejected and
// names the documented reason in the failure message so a regression is
// easy to read.
func TestInvalidExamples(t *testing.T) {
	for _, ex := range loadExamples(t) {
		if ex.Valid {
			continue
		}
		t.Run(ex.Name, func(t *testing.T) {
			err := contract.Validate(ex.Kind, ex.Data)
			if err == nil {
				t.Fatalf("%s validated but must fail. Reason it is invalid: %s", ex.Path, ex.Reason)
			}
			var se *contract.SchemaError
			if !errors.As(err, &se) && !errors.Is(err, contract.ErrInvalidJSON) && !errors.Is(err, contract.ErrTooLarge) {
				t.Fatalf("%s failed for an unexpected reason: %v (documented reason: %s)", ex.Path, err, ex.Reason)
			}
			t.Logf("rejected as documented (%s): %v", ex.Reason, err)
		})
	}
}

// TestExampleCoverage enforces the minimum of three valid and three invalid
// examples per message kind.
func TestExampleCoverage(t *testing.T) {
	valid := map[contract.Kind]int{}
	invalid := map[contract.Kind]int{}
	for _, ex := range loadExamples(t) {
		if ex.Valid {
			valid[ex.Kind]++
		} else {
			invalid[ex.Kind]++
		}
	}
	for _, k := range contract.Kinds {
		if valid[k] < 3 {
			t.Errorf("%s: %d valid examples, want at least 3", k, valid[k])
		}
		if invalid[k] < 3 {
			t.Errorf("%s: %d invalid examples, want at least 3", k, invalid[k])
		}
	}
}

// TestCommandExamplesAreSigned verifies every valid cmd example with the
// published test key, so the committed examples are real signed commands and
// not just schema shaped.
func TestCommandExamplesAreSigned(t *testing.T) {
	vectors, err := conformance.LoadSigningVectors(filepath.Join(examplesRoot, "signing", "ed25519.json"))
	if err != nil {
		t.Fatal(err)
	}
	pub, _, err := vectors.Keys()
	if err != nil {
		t.Fatal(err)
	}
	for _, ex := range loadExamples(t) {
		if !ex.Valid || ex.Kind != contract.KindCmd {
			continue
		}
		t.Run(ex.Name, func(t *testing.T) {
			if err := conformance.VerifyCommandExample(ex, pub, vectors.VerifyAt); err != nil {
				t.Fatalf("%s: %v", ex.Path, err)
			}
		})
	}
}
