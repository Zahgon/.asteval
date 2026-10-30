// Copyright (c) 2026 Matthew Newville, The University of Chicago
// Portions follow CPython 3.12, Copyright 2001 Python Software Foundation.
// SPDX-License-Identifier: MIT AND PSF-2.0
//
// Go port of asteval. The format-spec mini-language: builtin format(value,
// spec) for the values asteval code sees, ported from CPython 3.12's Python/formatter_unicode.c and
// the float layout of Python/pystrtod.c.

package asteval

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// fmtMaxWidth is the largest field width or output size formatted; CPython
// fails to allocate anything larger with MemoryError.
const fmtMaxWidth = math.MaxInt32

func fmtMemoryErr() error { return newPyErr("MemoryError", "") }

// fmtSpec is a parsed format specification (CPython's InternalFormatSpec).
type fmtSpec struct {
	fill   rune
	align  rune // '<', '>', '=' or '^'
	sign   rune // 0, '+', '-' or ' '
	noNeg0 bool // 'z'
	alt    bool // '#'
	width  int  // -1 when absent
	sep    rune // thousands separator: 0, ',' or '_'
	group  int  // digits per group when sep is set
	prec   int  // -1 when absent
	typ    rune // presentation type, 0 for none
}

// fmtDecimal returns the value of a Unicode decimal digit, or -1
// (Py_UNICODE_TODECIMAL).
func fmtDecimal(r rune) int {
	if r >= '0' && r <= '9' {
		return int(r - '0')
	}
	return pyDecimalValueOr(r)
}

// fmtSpecInteger reads a run of decimal digits at s[*pos:] (get_integer in
// formatter_unicode.c) and returns the value and the number of digits read.
func fmtSpecInteger(s []rune, pos *int) (int, int, error) {
	acc, n := 0, 0
	for ; *pos < len(s); *pos, n = *pos+1, n+1 {
		d := fmtDecimal(s[*pos])
		if d < 0 {
			break
		}
		if acc > (math.MaxInt64-d)/10 {
			return 0, 0, valueErr("Too many decimal digits in format string")
		}
		acc = acc*10 + d
	}
	return acc, n, nil
}

func fmtIsAlign(r rune) bool { return r == '<' || r == '>' || r == '=' || r == '^' }

// fmtCodeName renders a presentation type in an error message as CPython
// does: the character when it is printable ASCII, else \x and its hex code.
func fmtCodeName(r rune) string {
	if r > 32 && r < 128 {
		return string(r)
	}
	return `\x` + strconv.FormatInt(int64(r), 16)
}

// fmtParseSpec is parse_internal_render_format_spec. typeName names the
// formatted value's type in error messages.
func fmtParseSpec(spec, typeName string, defType, defAlign rune) (*fmtSpec, error) {
	s := []rune(spec)
	f := &fmtSpec{fill: ' ', align: defAlign, width: -1, prec: -1, typ: defType}
	pos, end := 0, len(s)
	fillSet, alignSet := false, false
	if end-pos >= 2 && fmtIsAlign(s[pos+1]) {
		f.align, f.fill = s[pos+1], s[pos]
		fillSet, alignSet = true, true
		pos += 2
	} else if end-pos >= 1 && fmtIsAlign(s[pos]) {
		f.align = s[pos]
		alignSet = true
		pos++
	}
	if end-pos >= 1 && (s[pos] == '+' || s[pos] == '-' || s[pos] == ' ') {
		f.sign = s[pos]
		pos++
	}
	if end-pos >= 1 && s[pos] == 'z' {
		f.noNeg0 = true
		pos++
	}
	if end-pos >= 1 && s[pos] == '#' {
		f.alt = true
		pos++
	}
	if !fillSet && end-pos >= 1 && s[pos] == '0' {
		f.fill = '0'
		if !alignSet && defAlign == '>' {
			f.align = '='
		}
		pos++
	}
	n, digits, err := fmtSpecInteger(s, &pos)
	if err != nil {
		return nil, err
	}
	if digits > 0 {
		f.width = n
	}
	if end-pos > 0 && s[pos] == ',' {
		f.sep = ','
		pos++
	}
	if end-pos > 0 && s[pos] == '_' {
		if f.sep != 0 {
			return nil, valueErr("Cannot specify both ',' and '_'.")
		}
		f.sep = '_'
		pos++
	}
	if end-pos > 0 && s[pos] == ',' && f.sep == '_' {
		return nil, valueErr("Cannot specify both ',' and '_'.")
	}
	if end-pos > 0 && s[pos] == '.' {
		pos++
		n, digits, err := fmtSpecInteger(s, &pos)
		if err != nil {
			return nil, err
		}
		if digits == 0 {
			return nil, valueErr("Format specifier missing precision")
		}
		f.prec = n
	}
	if end-pos > 1 {
		return nil, valueErr("Invalid format specifier '%s' for object of type '%s'", spec, typeName)
	}
	if end-pos == 1 {
		f.typ = s[pos]
	}
	if f.sep != 0 {
		switch f.typ {
		case 'd', 'e', 'f', 'g', 'E', 'G', '%', 'F', 0:
			f.group = 3
		case 'b', 'o', 'x', 'X':
			if f.sep == '_' {
				f.group = 4
			}
		}
		if f.group == 0 {
			return nil, valueErr("Cannot specify '%c' with '%s'.", f.sep, fmtCodeName(f.typ))
		}
	}
	return f, nil
}

func fmtUnknownCode(typ rune, typeName string) error {
	return valueErr("Unknown format code '%s' for object of type '%s'", fmtCodeName(typ), typeName)
}

// formatValue is the builtin format(v, spec): v.__format__(spec).
func formatValue(v Value, spec string) (string, error) {
	switch x := v.(type) {
	case strV:
		return fmtStr(string(x), spec, "str")
	case intV:
		if spec == "" {
			return intString(x.v)
		}
		return fmtInt(x.v, spec, "int")
	case boolV:
		if spec == "" {
			return pyStr(x)
		}
		i, _ := asInt(x)
		return fmtInt(i, spec, "bool")
	case floatV:
		if spec == "" {
			return floatRepr(float64(x)), nil
		}
		f, err := fmtParseSpec(spec, "float", 0, '>')
		if err != nil {
			return "", err
		}
		switch f.typ {
		case 0, 'e', 'E', 'f', 'F', 'g', 'G', 'n', '%':
			return fmtFloatSpec(float64(x), f)
		}
		return "", fmtUnknownCode(f.typ, "float")
	case complexV:
		return fmtComplex(complex128(x), spec)
	}
	// object.__format__
	if spec != "" {
		return "", typeErr("unsupported format string passed to %s.__format__", v.pyType())
	}
	return pyStr(v)
}

// fmtStr is str.__format__.
func fmtStr(s, spec, typeName string) (string, error) {
	if spec == "" {
		return s, nil
	}
	f, err := fmtParseSpec(spec, typeName, 's', '<')
	if err != nil {
		return "", err
	}
	if f.typ != 's' {
		return "", fmtUnknownCode(f.typ, typeName)
	}
	switch {
	case f.sign == ' ':
		return "", valueErr("Space not allowed in string format specifier")
	case f.sign != 0:
		return "", valueErr("Sign not allowed in string format specifier")
	case f.noNeg0:
		return "", valueErr("Negative zero coercion (z) not allowed in string format specifier")
	case f.alt:
		return "", valueErr("Alternate form (#) not allowed in string format specifier")
	case f.align == '=':
		return "", valueErr("'=' alignment not allowed in string format specifier")
	}
	n := utf8.RuneCountInString(s)
	if (f.width == -1 || f.width <= n) && (f.prec == -1 || f.prec >= n) {
		return s, nil
	}
	if f.prec >= 0 && n >= f.prec {
		s = string([]rune(s)[:f.prec])
		n = f.prec
	}
	if f.width > fmtMaxWidth {
		return "", fmtMemoryErr()
	}
	total := max(n, f.width)
	var left int
	switch f.align {
	case '>':
		left = total - n
	case '^':
		left = (total - n) / 2
	}
	fill := string(f.fill)
	return strings.Repeat(fill, left) + s + strings.Repeat(fill, total-n-left), nil
}

// fmtInt is int.__format__ for a non-empty spec; typeName is "int" or "bool".
func fmtInt(i *big.Int, spec, typeName string) (string, error) {
	f, err := fmtParseSpec(spec, typeName, 'd', '>')
	if err != nil {
		return "", err
	}
	switch f.typ {
	case 'b', 'c', 'd', 'o', 'x', 'X', 'n':
		return fmtLong(i, f)
	case 'e', 'E', 'f', 'F', 'g', 'G', '%':
		x, err := bigToFloat(i)
		if err != nil {
			return "", err
		}
		return fmtFloatSpec(x, f)
	}
	return "", fmtUnknownCode(f.typ, typeName)
}

// fmtLong is format_long_internal.
func fmtLong(i *big.Int, f *fmtSpec) (string, error) {
	if f.prec != -1 {
		return "", valueErr("Precision not allowed in integer format specifier")
	}
	if f.noNeg0 {
		return "", valueErr("Negative zero coercion (z) not allowed in integer format specifier")
	}
	if f.typ == 'c' {
		if f.sign != 0 {
			return "", valueErr("Sign not allowed with integer format specifier 'c'")
		}
		if f.alt {
			return "", valueErr("Alternate form (#) not allowed with integer format specifier 'c'")
		}
		if !i.IsInt64() {
			return "", newPyErr("OverflowError", "Python int too large to convert to C long")
		}
		if x := i.Int64(); x < 0 || x > 0x10ffff {
			return "", newPyErr("OverflowError", "%c arg not in range(0x110000)")
		}
		return fmtNumber(f, false, "", "", false, string(rune(i.Int64())))
	}
	var digits, prefix string
	switch f.typ {
	case 'b':
		digits, prefix = new(big.Int).Abs(i).Text(2), "0b"
	case 'o':
		digits, prefix = new(big.Int).Abs(i).Text(8), "0o"
	case 'x':
		digits, prefix = new(big.Int).Abs(i).Text(16), "0x"
	case 'X':
		digits, prefix = strings.ToUpper(new(big.Int).Abs(i).Text(16)), "0X"
	default:
		s, err := intString(i)
		if err != nil {
			return "", err
		}
		digits = strings.TrimPrefix(s, "-")
	}
	if !f.alt {
		prefix = ""
	}
	return fmtNumber(f, i.Sign() < 0, prefix, digits, false, "")
}

// fmtFloatSpec is format_float_internal.
func fmtFloatSpec(x float64, f *fmtSpec) (string, error) {
	if f.prec > math.MaxInt32 {
		return "", valueErr("precision too big")
	}
	typ, prec, defPrec := f.typ, f.prec, 6
	addDot0, pct := false, false
	if typ == 0 {
		// Like repr() without a precision, else like 'g' with at least one
		// digit after the decimal point.
		addDot0, typ, defPrec = true, 'r', 0
	}
	if typ == 'n' {
		typ = 'g'
	}
	if typ == '%' {
		typ, x, pct = 'f', x*100, true
	}
	if prec < 0 {
		prec = defPrec
	} else if typ == 'r' {
		typ = 'g'
	}
	s := fmtFloat(x, byte(typ), prec, f.alt, addDot0, f.noNeg0)
	if pct {
		s += "%"
	}
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	digits, rest := s[:i], s[i:]
	hasDecimal := strings.HasPrefix(rest, ".")
	if hasDecimal {
		rest = rest[1:]
	}
	return fmtNumber(f, neg, "", digits, hasDecimal, rest)
}

// fmtNumber lays out a formatted number (calc_number_widths and fill_number):
//
//	<lpadding> <sign> <prefix> <spadding> <grouped digits> <decimal> <rest> <rpadding>
//
// digits is the ASCII integer part; rest is what follows the decimal point,
// or the whole text when there are no digits (a character, inf or nan).
func fmtNumber(f *fmtSpec, neg bool, prefix, digits string, hasDecimal bool, rest string) (string, error) {
	if f.width > fmtMaxWidth {
		return "", fmtMemoryErr()
	}
	sign := ""
	switch {
	case neg:
		sign = "-"
	case f.sign == '+':
		sign = "+"
	case f.sign == ' ':
		sign = " "
	}
	nonDigits := len(sign) + len(prefix) + utf8.RuneCountInString(rest)
	if hasDecimal {
		nonDigits++
	}
	minWidth := 0
	if f.fill == '0' && f.align == '=' {
		minWidth = f.width - nonDigits
	}
	grouped := ""
	if digits != "" {
		grouped = fmtGroup(digits, minWidth, f.sep, f.group)
	}
	var left, inner, right int
	if pad := f.width - nonDigits - len(grouped); pad > 0 {
		switch f.align {
		case '<':
			right = pad
		case '^':
			left = pad / 2
			right = pad - left
		case '=':
			inner = pad
		default:
			left = pad
		}
	}
	fill := string(f.fill)
	var b strings.Builder
	b.WriteString(strings.Repeat(fill, left))
	b.WriteString(sign)
	b.WriteString(prefix)
	b.WriteString(strings.Repeat(fill, inner))
	b.WriteString(grouped)
	if hasDecimal {
		b.WriteByte('.')
	}
	b.WriteString(rest)
	b.WriteString(strings.Repeat(fill, right))
	return b.String(), nil
}

// fmtGroup is _PyUnicode_InsertThousandsGrouping: it inserts sep between
// groups of size digits counted from the right (no grouping when size is
// 0), padding with zeros (and separators) to at least minWidth characters.
func fmtGroup(digits string, minWidth int, sep rune, size int) string {
	minWidth = max(minWidth, 0)
	var parts []string // right to left
	remaining, pos := len(digits), len(digits)
	useSep := false
	piece := func(l int) {
		zeros := max(0, l-remaining)
		chars := max(0, min(remaining, l))
		p := strings.Repeat("0", zeros) + digits[pos-chars:pos]
		if useSep {
			p += string(sep)
		}
		parts = append(parts, p)
		pos -= chars
		remaining -= chars
		useSep = true
	}
	done := false
	if size > 0 {
		for {
			l := min(size, max(remaining, minWidth, 1))
			piece(l)
			minWidth -= l
			if remaining <= 0 && minWidth <= 0 {
				done = true
				break
			}
			minWidth--
		}
	}
	if !done {
		piece(max(remaining, minWidth, 1))
	}
	var b strings.Builder
	for i := len(parts) - 1; i >= 0; i-- {
		b.WriteString(parts[i])
	}
	return b.String()
}

// fmtFloat is PyOS_double_to_string without a forced sign: x formatted with
// code 'e', 'E', 'f', 'F', 'g', 'G' or 'r' (shortest repr; prec unused).
// alt keeps the decimal point and, for 'g', trailing zeros; addDot0 makes
// an integral result end in ".0"; noNeg0 drops the sign of a zero result.
func fmtFloat(x float64, code byte, prec int, alt, addDot0, noNeg0 bool) string {
	upper := code == 'E' || code == 'F' || code == 'G'
	if upper {
		code += 'a' - 'A'
	}
	neg := math.Signbit(x)
	if math.IsInf(x, 0) || math.IsNaN(x) {
		s := "inf"
		if math.IsNaN(x) {
			s, neg = "nan", false
		}
		if upper {
			s = strings.ToUpper(s)
		}
		if neg {
			s = "-" + s
		}
		return s
	}
	ax := math.Abs(x)
	var digits string
	var decpt int
	switch code {
	case 'e':
		prec++
		digits, decpt = fmtDigitsE(ax, prec)
	case 'g':
		if prec == 0 {
			prec = 1
		}
		digits, decpt = fmtDigitsE(ax, prec)
	case 'r':
		digits, decpt = fmtDigitsE(ax, -1)
	default:
		digits, decpt = fmtDigitsF(ax, prec)
	}
	if noNeg0 && neg && (digits == "" || digits == "0") {
		neg = false
	}

	// Output the slice vdigits[vStart:vEnd] of digits padded with zeros
	// on both sides, with the decimal point after decpt digits.
	dl := len(digits)
	vEnd := dl
	useExp := false
	switch code {
	case 'e':
		useExp, vEnd = true, prec
	case 'f':
		vEnd = decpt + prec
	case 'g':
		limit := prec
		if addDot0 {
			limit--
		}
		useExp = decpt <= -4 || decpt > limit
		if alt {
			vEnd = prec
		}
	case 'r':
		useExp = decpt <= -4 || decpt > 16
	}
	exp := 0
	if useExp {
		exp, decpt = decpt-1, 1
	}
	vStart := 0
	if decpt <= 0 {
		vStart = decpt - 1
	}
	if !useExp && addDot0 {
		vEnd = max(vEnd, decpt+1)
	} else {
		vEnd = max(vEnd, decpt)
	}
	zeros := func(n int) string { return strings.Repeat("0", max(n, 0)) }
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	if decpt <= 0 {
		b.WriteString(zeros(decpt - vStart))
		b.WriteByte('.')
		b.WriteString(zeros(-decpt))
	} else {
		b.WriteString(zeros(-vStart))
	}
	if 0 < decpt && decpt <= dl {
		b.WriteString(digits[:decpt])
		b.WriteByte('.')
		b.WriteString(digits[decpt:])
	} else {
		b.WriteString(digits)
	}
	if dl < decpt {
		b.WriteString(zeros(decpt - dl))
		b.WriteByte('.')
		b.WriteString(zeros(vEnd - decpt))
	} else {
		b.WriteString(zeros(vEnd - dl))
	}
	s := b.String()
	if !alt && strings.HasSuffix(s, ".") {
		s = s[:len(s)-1]
	}
	if useExp {
		e := "e"
		if upper {
			e = "E"
		}
		es := "+"
		if exp < 0 {
			es, exp = "-", -exp
		}
		if exp < 10 {
			es += "0"
		}
		s += e + es + strconv.Itoa(exp)
	}
	return s
}

// fmtDigitsE returns the n significant digits of x >= 0 correctly rounded
// (the shortest digits that round trip when n < 0), without trailing zeros,
// and the position of the decimal point: _Py_dg_dtoa modes 2 and 0.
func fmtDigitsE(x float64, n int) (string, int) {
	if x == 0 {
		return "0", 1
	}
	s := strconv.FormatFloat(x, 'e', max(n-1, -1), 64)
	e := strings.IndexByte(s, 'e')
	exp, _ := strconv.Atoi(s[e+1:])
	return strings.TrimRight(strings.Replace(s[:e], ".", "", 1), "0"), exp + 1
}

// fmtDigitsF returns the digits of x >= 0 rounded to prec places after the
// point, without leading or trailing zeros, and the position of the decimal
// point: _Py_dg_dtoa mode 3.
func fmtDigitsF(x float64, prec int) (string, int) {
	if x == 0 {
		return "0", 1
	}
	s := strconv.FormatFloat(x, 'f', prec, 64)
	ip, fp, _ := strings.Cut(s, ".")
	if ip != "0" {
		return strings.TrimRight(ip+fp, "0"), len(ip)
	}
	t := strings.TrimLeft(fp, "0")
	if t == "" {
		return "", -prec
	}
	return strings.TrimRight(t, "0"), len(t) - len(fp)
}

// fmtComplex is complex.__format__ (format_complex_internal).
func fmtComplex(c complex128, spec string) (string, error) {
	if spec == "" {
		return complexRepr(c), nil
	}
	f, err := fmtParseSpec(spec, "complex", 0, '>')
	if err != nil {
		return "", err
	}
	if f.fill == '0' {
		return "", valueErr("Zero padding is not allowed in complex format specifier")
	}
	if f.align == '=' {
		return "", valueErr("'=' alignment flag is not allowed in complex format specifier")
	}
	switch f.typ {
	case 0, 'e', 'E', 'f', 'F', 'g', 'G', 'n':
	default:
		return "", fmtUnknownCode(f.typ, "complex")
	}
	re, im := real(c), imag(c)
	typ, prec := f.typ, f.prec
	skipRe, parens := false, false
	defPrec := 6
	if typ == 0 {
		typ, defPrec = 'r', 0
		if re == 0 && !math.Signbit(re) {
			skipRe = true
		} else {
			parens = true
		}
	}
	if typ == 'n' {
		typ = 'g'
	}
	if prec < 0 {
		prec = defPrec
	} else if typ == 'r' {
		typ = 'g'
	}
	part := func(x float64, sign rune) (string, error) {
		s := fmtFloat(x, byte(typ), prec, f.alt, false, f.noNeg0)
		neg := strings.HasPrefix(s, "-")
		if neg {
			s = s[1:]
		}
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		digits, rest := s[:i], s[i:]
		hasDecimal := strings.HasPrefix(rest, ".")
		if hasDecimal {
			rest = rest[1:]
		}
		tmp := *f
		tmp.fill, tmp.align, tmp.width, tmp.sign = ' ', '<', -1, sign
		return fmtNumber(&tmp, neg, "", digits, hasDecimal, rest)
	}
	var b strings.Builder
	if !skipRe {
		s, err := part(re, f.sign)
		if err != nil {
			return "", err
		}
		b.WriteString(s)
	}
	imSign := f.sign
	if !skipRe {
		imSign = '+'
	}
	s, err := part(im, imSign)
	if err != nil {
		return "", err
	}
	b.WriteString(s)
	b.WriteByte('j')
	body := b.String()
	if parens {
		body = "(" + body + ")"
	}
	n := utf8.RuneCountInString(body)
	if f.width <= n {
		return body, nil
	}
	pad := f.width - n
	fill := string(f.fill)
	switch f.align {
	case '<':
		return body + strings.Repeat(fill, pad), nil
	case '^':
		return strings.Repeat(fill, pad/2) + body + strings.Repeat(fill, pad-pad/2), nil
	}
	return strings.Repeat(fill, pad) + body, nil
}
