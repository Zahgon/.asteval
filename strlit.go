// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT
//
// Go port of asteval. String and bytes literal escapes as the Python 3.12
// parser decodes them.

package asteval

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// unicodeNames holds the character names \N{...} may use. Go's standard
// library has no Unicode name database, so the port carries the names of the
// characters it supports; a name outside the table is refused as CPython
// refuses an unknown name.
var unicodeNames = map[string]rune{
	"SPACE": ' ', "NO-BREAK SPACE": 0xa0, "EM DASH": 0x2014, "EN DASH": 0x2013, "BULLET": 0x2022,
	"HORIZONTAL ELLIPSIS": 0x2026, "DEGREE SIGN": 0xb0, "MICRO SIGN": 0xb5, "SECTION SIGN": 0xa7,
	"COPYRIGHT SIGN": 0xa9, "REGISTERED SIGN": 0xae, "EURO SIGN": 0x20ac, "POUND SIGN": 0xa3, "YEN SIGN": 0xa5,
	"LATIN SMALL LETTER A": 'a', "LATIN SMALL LETTER B": 'b', "LATIN CAPITAL LETTER A": 'A',
	"LATIN CAPITAL LETTER B": 'B', "LATIN SMALL LETTER E WITH ACUTE": 0xe9, "LATIN SMALL LETTER SHARP S": 0xdf,
	"LATIN CAPITAL LETTER I WITH DOT ABOVE": 0x130, "LATIN SMALL LETTER DOTLESS I": 0x131,
	"LATIN CAPITAL LETTER D WITH SMALL LETTER Z WITH CARON": 0x1c5, "LATIN SMALL LIGATURE FI": 0xfb01,
	"GREEK SMALL LETTER ALPHA": 0x3b1, "GREEK SMALL LETTER BETA": 0x3b2, "GREEK SMALL LETTER PI": 0x3c0,
	"GREEK SMALL LETTER SIGMA": 0x3c3, "GREEK SMALL LETTER FINAL SIGMA": 0x3c2, "GREEK CAPITAL LETTER SIGMA": 0x3a3,
	"GREEK CAPITAL LETTER OMEGA": 0x3a9, "SNOWMAN": 0x2603, "BLACK STAR": 0x2605, "WHITE SMILING FACE": 0x263a,
	"INFINITY": 0x221e, "PLUS-MINUS SIGN": 0xb1, "MULTIPLICATION SIGN": 0xd7, "DIVISION SIGN": 0xf7,
	"SUPERSCRIPT TWO": 0xb2, "VULGAR FRACTION ONE HALF": 0xbd, "ROMAN NUMERAL TWELVE": 0x216b,
	"DIGIT ZERO": '0', "DIGIT SEVEN": '7', "ARABIC-INDIC DIGIT THREE": 0x663, "FULLWIDTH DIGIT FIVE": 0xff15,
	"CJK UNIFIED IDEOGRAPH-4E2D": 0x4e2d, "HIRAGANA LETTER A": 0x3042, "ZERO WIDTH SPACE": 0x200b,
	"LINE SEPARATOR": 0x2028, "RIGHT-TO-LEFT MARK": 0x200f, "COMBINING ACUTE ACCENT": 0x301,
	"GRINNING FACE": 0x1f600, "PILE OF POO": 0x1f4a9, "CHECK MARK": 0x2713, "HEAVY BLACK HEART": 0x2764,
	"LEFT-POINTING DOUBLE ANGLE QUOTATION MARK": 0xab, "RIGHT DOUBLE QUOTATION MARK": 0x201d, "TAB": 0,
	"NULL": 0, "LINE FEED": '\n', "CARRIAGE RETURN": '\r', "CHARACTER TABULATION": '\t',
}

// decodeStrBody decodes the body of a str or bytes literal. A raw literal is
// kept as written, except that a line break is normalised to \n.
func decodeStrBody(body string, raw, isBytes bool) (string, error) {
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	if isBytes {
		for i := 0; i < len(body); i++ {
			if body[i] >= 0x80 {
				return "", errors.New("bytes can only contain ASCII literal characters")
			}
		}
	}
	if raw {
		return body, nil
	}
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' || i+1 >= len(body) {
			b.WriteByte(c)
			continue
		}
		i++
		e := body[i]
		switch e {
		case '\n':
		case '\\', '\'', '"':
			b.WriteByte(e)
		case 'a':
			b.WriteByte(7)
		case 'b':
			b.WriteByte(8)
		case 'f':
			b.WriteByte(12)
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte(11)
		case '0', '1', '2', '3', '4', '5', '6', '7':
			v := 0
			j := i
			for j < len(body) && j < i+3 && body[j] >= '0' && body[j] <= '7' {
				v = v*8 + int(body[j]-'0')
				j++
			}
			i = j - 1
			if isBytes {
				b.WriteByte(byte(v))
			} else {
				b.WriteRune(rune(v))
			}
		case 'x':
			v, ok := hexDigits(body, i+1, 2)
			if !ok {
				return "", escapeErr(isBytes, "truncated \\xXX escape")
			}
			i += 2
			if isBytes {
				b.WriteByte(byte(v))
			} else {
				b.WriteRune(rune(v))
			}
		case 'u', 'U', 'N':
			if isBytes {
				b.WriteByte('\\')
				b.WriteByte(e)
				continue
			}
			switch e {
			case 'u', 'U':
				n := 4
				if e == 'U' {
					n = 8
				}
				v, ok := hexDigits(body, i+1, n)
				if !ok {
					return "", escapeErr(false, "truncated \\"+string(e)+strings.Repeat("X", n)+" escape")
				}
				if v > 0x10ffff {
					return "", escapeErr(false, "illegal Unicode character")
				}
				i += n
				b.WriteRune(rune(v))
			case 'N':
				if i+1 >= len(body) || body[i+1] != '{' {
					return "", escapeErr(false, "malformed \\N character escape")
				}
				end := strings.IndexByte(body[i+1:], '}')
				if end < 0 {
					return "", escapeErr(false, "malformed \\N character escape")
				}
				name := strings.ToUpper(body[i+2 : i+1+end])
				r, ok := unicodeNames[name]
				if !ok || name == "TAB" {
					return "", escapeErr(false, "unknown Unicode character name")
				}
				b.WriteRune(r)
				i += 1 + end
			}
		default:
			// An unknown escape keeps its backslash (with a SyntaxWarning).
			b.WriteByte('\\')
			b.WriteByte(e)
		}
	}
	return b.String(), nil
}

func escapeErr(isBytes bool, msg string) error {
	codec := "unicodeescape"
	if isBytes {
		return errors.New("(value error) invalid \\x escape")
	}
	return errors.New("(unicode error) '" + codec + "' codec can't decode bytes: " + msg)
}

func hexDigits(s string, at, n int) (int, bool) {
	if at+n > len(s) {
		return 0, false
	}
	v := 0
	for k := 0; k < n; k++ {
		c := s[at+k]
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			return 0, false
		}
		v = v*16 + d
	}
	return v, true
}

// nfkcName normalises an identifier as the parser does (NFKC). Identifiers
// in ASCII are already normal; the port leaves others as written.
func nfkcName(s string) string {
	if !utf8.ValidString(s) {
		return s
	}
	return s
}
