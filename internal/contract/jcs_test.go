package contract_test

import (
	"path/filepath"
	"testing"

	"github.com/wxalerts/pws-gateway/internal/contract"
	"github.com/wxalerts/pws-gateway/internal/contract/conformance"
)

// TestCanonicalizeRFC8785Examples uses the worked examples from RFC 8785
// section 3.2.3 (property sorting) and section 3.2.2 (number and string
// handling). They are inline so the vectors file in schemas/examples/signing
// is checked against an independent source. Non-ASCII characters are written
// as escapes so the source stays ASCII.
func TestCanonicalizeRFC8785Examples(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "numbers strings literals",
			in: `{
  "numbers": [333333333.33333329, 1E30, 4.50, 2e-3, 0.000000000000000000000000001],
  "string": "\u20ac$\u000F\u000aA'\u0042\u0022\u005c\\\"\/",
  "literals": [null, true, false]
}`,
			want: `{"literals":[null,true,false],"numbers":[333333333.3333333,1e+30,4.5,0.002,1e-27],"string":"` +
				"\u20ac" + `$\u000f\nA'B\"\\\\\"/"}`,
		},
		{
			// Keys sort by UTF-16 code units, so the supplementary plane
			// character U+1F602 (a surrogate pair starting D83D) sorts
			// before U+FB33.
			name: "utf16 key order",
			in: `{
  "\u20ac": "Euro Sign",
  "\r": "Carriage Return",
  "\ufb33": "Hebrew Letter Dalet With Dagesh",
  "1": "One",
  "\ud83d\ude02": "Emoji: Smiling Face",
  "\u0080": "Control",
  "\u00f6": "Latin Small Letter O With Diaeresis"
}`,
			want: `{"\r":"Carriage Return","1":"One","` + "\u0080" + `":"Control","` +
				"\u00f6" + `":"Latin Small Letter O With Diaeresis","` +
				"\u20ac" + `":"Euro Sign","` +
				"\U0001F602" + `":"Emoji: Smiling Face","` +
				"\ufb33" + `":"Hebrew Letter Dalet With Dagesh"}`,
		},
		{
			name: "whitespace and empties",
			in:   " { \"b\" : [ ] , \"a\" : { } , \"c\" : [ { } , [ ] , \"\" ] } \n",
			want: `{"a":{},"b":[],"c":[{},[],""]}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := contract.Canonicalize([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("canonical mismatch\n want %s\n got  %s", c.want, got)
			}
		})
	}
}

// TestCanonicalizeNumbers pins the ECMAScript number layout rules.
func TestCanonicalizeNumbers(t *testing.T) {
	cases := map[string]string{
		"0":                       "0",
		"-0":                      "0",
		"-0.0":                    "0",
		"1":                       "1",
		"-1":                      "-1",
		"1.0":                     "1",
		"100":                     "100",
		"1e21":                    "1e+21",
		"1e20":                    "100000000000000000000",
		"123456789012345680000":   "123456789012345680000",
		"0.1":                     "0.1",
		"0.000001":                "0.000001",
		"0.0000001":               "1e-7",
		"1.5e-7":                  "1.5e-7",
		"123.456":                 "123.456",
		"-1.5E+3":                 "-1500",
		"9007199254740993":        "9007199254740992",
		"5e-324":                  "5e-324",
		"1.7976931348623157e308":  "1.7976931348623157e+308",
		"333333333.33333329":      "333333333.3333333",
		"4.50":                    "4.5",
		"2e-3":                    "0.002",
		"1E30":                    "1e+30",
		"0.000000000000000000001": "1e-21",
		"12345678901234567890":    "12345678901234567000",
	}
	for in, want := range cases {
		got, err := contract.Canonicalize([]byte(in))
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s: want %s, got %s", in, want, got)
		}
	}
}

func TestCanonicalizeRejects(t *testing.T) {
	for _, in := range []string{
		"",
		"{",
		`{"a":1} trailing`,
		"1e400", // overflows a double, not representable
		"NaN",
		`{"a":1,}`,
	} {
		if _, err := contract.Canonicalize([]byte(in)); err == nil {
			t.Errorf("%q: expected an error", in)
		}
	}
}

// TestCanonicalizeVectorsFile checks the committed JCS vectors that other
// implementations use.
func TestCanonicalizeVectorsFile(t *testing.T) {
	path := filepath.Join(examplesRoot, "signing", "jcs.json")
	f, err := conformance.LoadJCSVectors(path)
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := f.Fill(); err != nil {
			t.Fatal(err)
		}
		if err := conformance.WriteJSON(path, f); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s", path)
	}
	if len(f.Vectors) == 0 {
		t.Fatal("no JCS vectors")
	}
	for _, err := range f.Check() {
		t.Error(err)
	}
}
