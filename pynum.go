// Copyright (c) 2026 Matthew Newville, The University of Chicago
// Portions follow CPython 3.12, Copyright 2001 Python Software Foundation.
// SPDX-License-Identifier: MIT AND PSF-2.0

package asteval

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// pynumToASCII is CPython's _PyUnicode_TransformDecimalAndSpaceToASCII:
// characters below U+007F are kept, other whitespace becomes a space, other
// decimal digits become ASCII digits, and the first other character becomes
// '?', where the result ends.
func pynumToASCII(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r < 0x7f:
			out = append(out, byte(r))
		case pyRuneIsSpace(r):
			out = append(out, ' ')
		default:
			d := pyDecimalValueOr(r)
			if d < 0 {
				return append(out, '?')
			}
			out = append(out, byte('0'+d))
		}
	}
	return out
}

// pynumCSpace is C's isspace in the "C" locale (Py_ISSPACE).
func pynumCSpace(c byte) bool {
	return c == ' ' || c >= '\t' && c <= '\r'
}

// pynumDigit is CPython's _PyLong_DigitValue: 0-35 for 0-9, a-z and A-Z,
// else 37.
func pynumDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return 37
}

// pynumReprPrec is "%.200R": repr() cut to its first 200 characters.
func pynumReprPrec(v Value) string {
	r := reprOf(v)
	if utf8.RuneCountInString(r) <= 200 {
		return r
	}
	n := 0
	for i := range r {
		if n == 200 {
			return r[:i]
		}
		n++
	}
	return r
}

// pynumDigitLimitErr is the ValueError for a decimal string over
// sys.get_int_max_str_digits() digits.
func pynumDigitLimitErr(digits int) error {
	return valueErr("Exceeds the limit (%d digits) for integer string conversion: value has %d digits; "+
		"use sys.set_int_max_str_digits() to increase the limit", maxStrDigits, digits)
}

// pyParseInt is int(s, base) for a str s: surrounding whitespace, a sign,
// underscores between digits, an optional base prefix (required to pick the
// base when base is 0), and decimal digits of any script. Errors are
// Python's ValueErrors, word for word.
func pyParseInt(s string, base int) (Value, error) { return pyParseIntOf(s, base, strV(s)) }

// pyParseIntOf is pyParseInt for the str object obj, whose repr() errors show.
func pyParseIntOf(s string, base int, obj Value) (Value, error) {
	if base != 0 && base < 2 || base > 36 {
		return nil, valueErr("int() base must be >= 2 and <= 36, or 0")
	}
	buf := pynumToASCII(s)
	// The C code reads a NUL-terminated buffer.
	full := true
	if i := strings.IndexByte(string(buf), 0); i >= 0 {
		buf, full = buf[:i], false
	}
	v, err := pynumFromString(buf, base)
	if err != nil {
		return nil, err
	}
	if v == nil || !full {
		return nil, valueErr("invalid literal for int() with base %d: %s", base, pynumReprPrec(obj))
	}
	return mkBig(v), nil
}

// pynumFromString is CPython's PyLong_FromString on an ASCII buffer. It
// returns nil, nil for a malformed string and an error only for the digit
// limit.
func pynumFromString(b []byte, base int) (*big.Int, error) {
	at := func(i int) byte {
		if i < len(b) {
			return b[i]
		}
		return 0
	}
	p := 0
	for p < len(b) && pynumCSpace(b[p]) {
		p++
	}
	neg := false
	switch at(p) {
	case '+':
		p++
	case '-':
		p++
		neg = true
	}
	errorIfNonzero := false
	if base == 0 {
		switch {
		case at(p) != '0':
			base = 10
		case at(p+1) == 'x' || at(p+1) == 'X':
			base = 16
		case at(p+1) == 'o' || at(p+1) == 'O':
			base = 8
		case at(p+1) == 'b' || at(p+1) == 'B':
			base = 2
		default:
			// An old-style octal literal is invalid unless it is zero.
			errorIfNonzero = true
			base = 10
		}
	}
	if at(p) == '0' {
		c := at(p + 1)
		if base == 16 && (c == 'x' || c == 'X') || base == 8 && (c == 'o' || c == 'O') ||
			base == 2 && (c == 'b' || c == 'B') {
			p += 2
			if at(p) == '_' {
				p++
			}
		}
	}
	if at(p) == '_' {
		return nil, nil
	}
	start := p
	var prev byte
	digits := 0
	for pynumDigit(at(p)) < base || at(p) == '_' {
		if at(p) == '_' {
			if prev == '_' {
				return nil, nil
			}
		} else {
			digits++
		}
		prev = at(p)
		p++
	}
	if prev == '_' || p == start {
		return nil, nil
	}
	end := p
	for p < len(b) && pynumCSpace(b[p]) {
		p++
	}
	if p != len(b) {
		return nil, nil
	}
	if base&(base-1) != 0 && digits > maxStrDigits {
		return nil, pynumDigitLimitErr(digits)
	}
	text := strings.ReplaceAll(string(b[start:end]), "_", "")
	v, ok := new(big.Int).SetString(text, base)
	if !ok {
		return nil, nil
	}
	if errorIfNonzero && v.Sign() != 0 {
		return nil, nil
	}
	if neg {
		v.Neg(v)
	}
	return v, nil
}

// pyParseFloat is float(s) for a str s: surrounding whitespace, a sign,
// inf, infinity and nan in any case, underscores between digits, and decimal
// digits of any script, correctly rounded.
func pyParseFloat(s string) (float64, error) { return pyParseFloatOf(s, strV(s)) }

// pyParseFloatOf is pyParseFloat for the str object obj, whose repr() errors
// show.
func pyParseFloatOf(s string, obj Value) (float64, error) {
	f, ok := pynumParseFloat(pynumToASCII(s))
	if !ok {
		return 0, valueErr("could not convert string to float: %s", reprOf(obj))
	}
	return f, nil
}

// pynumParseFloat is _Py_string_to_number_with_underscores with
// float_from_string_inner on an ASCII buffer.
func pynumParseFloat(b []byte) (float64, bool) {
	if strings.IndexByte(string(b), 0) >= 0 {
		return 0, false
	}
	if strings.IndexByte(string(b), '_') >= 0 {
		isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
		var prev byte
		clean := make([]byte, 0, len(b))
		for _, c := range b {
			if c == '_' {
				// Underscores are only allowed after digits...
				if !isDigit(prev) {
					return 0, false
				}
			} else {
				clean = append(clean, c)
				// ... and before digits.
				if prev == '_' && !isDigit(c) {
					return 0, false
				}
			}
			prev = c
		}
		if prev == '_' {
			return 0, false
		}
		b = clean
	}
	i, j := 0, len(b)
	for i < j && pynumCSpace(b[i]) {
		i++
	}
	for j > i && pynumCSpace(b[j-1]) {
		j--
	}
	return pynumStrtod(string(b[i:j]))
}

// pynumStrtod is PyOS_string_to_double on the whole of s: an optionally
// signed decimal number, or inf, infinity or nan in any case.
func pynumStrtod(s string) (float64, bool) {
	body := s
	neg := false
	if body != "" && (body[0] == '+' || body[0] == '-') {
		neg = body[0] == '-'
		body = body[1:]
	}
	switch strings.ToLower(body) {
	case "inf", "infinity":
		if neg {
			return math.Inf(-1), true
		}
		return math.Inf(1), true
	case "nan":
		if neg {
			return math.Copysign(math.NaN(), -1), true
		}
		return math.NaN(), true
	}
	if !pynumIsDecimalFloat(body) {
		return 0, false
	}
	// The syntax is checked, so the only possible error is ErrRange, and
	// the value it comes with (±Inf or ±0) is the one Python gives.
	f, _ := strconv.ParseFloat(s, 64)
	return f, true
}

// pynumIsDecimalFloat reports whether s is digits with an optional point
// (at least one digit in all) and an optional exponent.
func pynumIsDecimalFloat(s string) bool {
	i := 0
	digits := func() int {
		n := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			n++
		}
		return n
	}
	n := digits()
	if i < len(s) && s[i] == '.' {
		i++
		n += digits()
	}
	if n == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if digits() == 0 {
			return false
		}
	}
	return i == len(s)
}
