package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// ErrNotCanonicalizable is returned when a JSON document cannot be put in
// RFC 8785 form, for example because a number is out of IEEE 754 double range.
var ErrNotCanonicalizable = errors.New("contract: JSON cannot be canonicalized")

// Canonicalize returns the RFC 8785 (JSON Canonicalization Scheme) form of
// data: no insignificant whitespace, object keys sorted by UTF-16 code
// units, numbers printed the way ECMAScript's Number.prototype.toString
// prints them, and strings escaped only where JSON requires it.
//
// This is a small self-contained implementation rather than a dependency
// because the signed objects are tiny and the rules fit in one file. It is
// checked against the examples in RFC 8785 and against the vectors in
// schemas/examples/signing/jcs.json.
func Canonicalize(data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing data after JSON value", ErrInvalidJSON)
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		f, err := strconv.ParseFloat(string(x), 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return fmt.Errorf("%w: number %s is not a finite IEEE 754 double", ErrNotCanonicalizable, x)
		}
		buf.WriteString(formatES6Number(f))
	case string:
		writeJCSString(buf, x)
	case []any:
		buf.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return lessUTF16(keys[i], keys[j]) })
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeJCSString(buf, k)
			buf.WriteByte(':')
			if err := writeCanonical(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("%w: unexpected value of type %T", ErrNotCanonicalizable, v)
	}
	return nil
}

// lessUTF16 orders strings by their UTF-16 code unit sequences, as RFC 8785
// section 3.2.3 requires. This differs from byte order for characters outside
// the Basic Multilingual Plane, which sort before U+E000 to U+FFFF.
func lessUTF16(a, b string) bool {
	ua := utf16.Encode([]rune(a))
	ub := utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

// writeJCSString writes s as a JSON string using only the escapes RFC 8785
// section 3.2.2.2 allows: \" \\ \b \f \n \r \t and \u00xx (lowercase hex)
// for other control characters. Everything else, including non-ASCII, '/'
// and DEL, is written literally.
func writeJCSString(buf *bytes.Buffer, s string) {
	const hex = "0123456789abcdef"
	buf.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if c < 0x20 {
				buf.WriteString(`\u00`)
				buf.WriteByte(hex[c>>4])
				buf.WriteByte(hex[c&0xf])
			} else {
				buf.WriteByte(c)
			}
		}
	}
	buf.WriteByte('"')
}

// formatES6Number prints a finite double the way ECMAScript
// Number.prototype.toString does (ECMA-262 section 6.1.6.1.20), which is what
// RFC 8785 section 3.2.2.3 requires. Go's shortest round trip digits are the
// same as ECMAScript's; only the layout of the digits differs, so this
// function takes the digits from strconv and lays them out by the ECMAScript
// rules.
func formatES6Number(f float64) string {
	if f == 0 {
		return "0" // covers negative zero, which ECMAScript prints as "0"
	}
	neg := f < 0
	if neg {
		f = -f
	}
	// Shortest representation in d.ddddde[+-]xx form.
	s := strconv.FormatFloat(f, 'e', -1, 64)
	mant, expStr, _ := strings.Cut(s, "e")
	digits := strings.Replace(mant, ".", "", 1)
	exp, err := strconv.Atoi(expStr)
	if err != nil {
		// strconv always produces a parsable exponent; this guards the
		// contract of the function rather than a reachable path.
		panic("contract: unexpected float format " + s)
	}
	k := len(digits) // number of significant digits
	n := exp + 1     // value = 0.digits * 10^n

	var out string
	switch {
	case k <= n && n <= 21:
		out = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		out = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		out = "0." + strings.Repeat("0", -n) + digits
	default:
		e := n - 1
		sign := "+"
		if e < 0 {
			sign = "-"
			e = -e
		}
		if k == 1 {
			out = digits + "e" + sign + strconv.Itoa(e)
		} else {
			out = digits[:1] + "." + digits[1:] + "e" + sign + strconv.Itoa(e)
		}
	}
	if neg {
		return "-" + out
	}
	return out
}
