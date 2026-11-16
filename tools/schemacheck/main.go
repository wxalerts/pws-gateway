// Command schemacheck validates every example payload under schemas/examples
// against the JSON Schemas in schemas/, checks that each invalid example has
// a reason file, that each valid example survives a round trip through the
// Go types, that the valid cmd examples carry signatures from the published
// test key, and that the signing and canonicalization vectors recompute. It
// exits non-zero on any mismatch. The Makefile runs it as `make schemas`.
package main

import (
	"crypto/ed25519"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/contract/conformance"
)

func main() {
	root := flag.String("examples", "schemas/examples", "directory holding valid/, invalid/ and signing/")
	quiet := flag.Bool("q", false, "print only failures")
	flag.Parse()

	failures := 0
	fail := func(format string, args ...any) {
		failures++
		fmt.Fprintf(os.Stderr, "FAIL "+format+"\n", args...)
	}
	ok := func(format string, args ...any) {
		if !*quiet {
			fmt.Printf("ok   "+format+"\n", args...)
		}
	}

	examples, err := conformance.LoadExamples(*root)
	if err != nil {
		fail("%v", err)
		os.Exit(1)
	}

	vectors, err := conformance.LoadSigningVectors(filepath.Join(*root, "signing", "ed25519.json"))
	if err != nil {
		fail("%v", err)
		os.Exit(1)
	}
	pub, _, err := vectors.Keys()
	if err != nil {
		fail("signing vectors: %v", err)
		os.Exit(1)
	}

	valid := map[contract.Kind]int{}
	invalid := map[contract.Kind]int{}
	for _, ex := range examples {
		rel, relErr := filepath.Rel(*root, ex.Path)
		if relErr != nil {
			rel = ex.Path
		}
		if ex.Valid {
			valid[ex.Kind]++
			if msg := checkValid(ex, rel, pub, vectors.VerifyAt); msg != "" {
				fail("%s", msg)
				continue
			}
			ok("%s", rel)
			continue
		}
		invalid[ex.Kind]++
		if err := contract.Validate(ex.Kind, ex.Data); err == nil {
			fail("%s: validated but must fail: %s", rel, ex.Reason)
			continue
		}
		ok("%s (rejected: %s)", rel, ex.Reason)
	}

	for _, k := range contract.Kinds {
		if valid[k] < 3 {
			fail("%s: %d valid examples, want at least 3", k, valid[k])
		}
		if invalid[k] < 3 {
			fail("%s: %d invalid examples, want at least 3", k, invalid[k])
		}
	}

	sigErrs := vectors.Check()
	for _, err := range sigErrs {
		fail("signing/ed25519.json: %v", err)
	}
	if len(sigErrs) == 0 {
		ok("signing/ed25519.json (%d vectors)", len(vectors.Vectors))
	}

	jcs, err := conformance.LoadJCSVectors(filepath.Join(*root, "signing", "jcs.json"))
	if err != nil {
		fail("%v", err)
	} else {
		errs := jcs.Check()
		for _, err := range errs {
			fail("signing/jcs.json: %v", err)
		}
		if len(errs) == 0 {
			ok("signing/jcs.json (%d vectors)", len(jcs.Vectors))
		}
	}

	if failures > 0 {
		fmt.Fprintf(os.Stderr, "%d failure(s)\n", failures)
		os.Exit(1)
	}
	fmt.Printf("all %d examples conform\n", len(examples))
}

// checkValid runs every check a valid example must pass and returns a failure
// message, or "" when the example conforms.
func checkValid(ex conformance.Example, rel string, pub ed25519.PublicKey, verifyAt time.Time) string {
	if err := contract.Validate(ex.Kind, ex.Data); err != nil {
		return fmt.Sprintf("%s: %v", rel, err)
	}
	out, err := conformance.RoundTrip(ex)
	if err != nil {
		return fmt.Sprintf("%s: round trip through Go type: %v", rel, err)
	}
	diffs, err := conformance.Diff(ex.Data, out)
	if err != nil {
		return fmt.Sprintf("%s: %v", rel, err)
	}
	if len(diffs) > 0 {
		return fmt.Sprintf("%s: round trip changed the document:\n  %s", rel, strings.Join(diffs, "\n  "))
	}
	if ex.Kind == contract.KindCmd {
		if err := conformance.VerifyCommandExample(ex, pub, verifyAt); err != nil {
			return fmt.Sprintf("%s: signature does not verify with the test key: %v", rel, err)
		}
	}
	return ""
}
