// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT
//
// Go port of asteval. The value model: the Python values asteval code sees,
// with CPython 3.12's repr, str, truth, equality, hashing and ordering rules.

package asteval

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Value is a Python value. It is implemented only by the values this package
// creates; build them with None, Bool, Int, BigInt, Float, Complex, String,
// Bytes, List, Tuple and NewDict.
type Value interface {
	pyType() string
}

type noneV struct{}

type boolV bool

type intV struct{ v *big.Int }

type floatV float64

type complexV complex128

type strV string

// bytesV holds the bytes of a bytes object in a Go string.
type bytesV string

type bytearrayV struct{ b []byte }

type listV struct{ items []Value }

type tupleV struct{ items []Value }

type rangeV struct{ start, stop, step int64 }

type sliceV struct{ start, stop, step Value }

type ellipsisV struct{}

// emptyV is astutils.Empty; ReturnedNone is its one instance.
type emptyV struct{}

var (
	none         Value = noneV{}
	trueV        Value = boolV(true)
	falseV       Value = boolV(false)
	returnedNone Value = emptyV{}
)

func (noneV) pyType() string       { return "NoneType" }
func (boolV) pyType() string       { return "bool" }
func (intV) pyType() string        { return "int" }
func (floatV) pyType() string      { return "float" }
func (complexV) pyType() string    { return "complex" }
func (strV) pyType() string        { return "str" }
func (bytesV) pyType() string      { return "bytes" }
func (*bytearrayV) pyType() string { return "bytearray" }
func (*listV) pyType() string      { return "list" }
func (*tupleV) pyType() string     { return "tuple" }
func (rangeV) pyType() string      { return "range" }
func (*sliceV) pyType() string     { return "slice" }
func (ellipsisV) pyType() string   { return "ellipsis" }
func (emptyV) pyType() string      { return "Empty" }
func (e *excV) pyType() string     { return e.Class }

func newList(items []Value) *listV   { return &listV{items: items} }
func newTuple(items []Value) *tupleV { return &tupleV{items: items} }

func mkInt(i int64) Value    { return intV{big.NewInt(i)} }
func mkBig(i *big.Int) Value { return intV{i} }
func pyBool(b bool) Value {
	if b {
		return trueV
	}
	return falseV
}

// asInt returns v as an integer if it is an int or a bool.
func asInt(v Value) (*big.Int, bool) {
	switch n := v.(type) {
	case intV:
		return n.v, true
	case boolV:
		if n {
			return big.NewInt(1), true
		}
		return big.NewInt(0), true
	}
	return nil, false
}

// isNumber reports whether v is a real number (int, bool or float).
func isNumber(v Value) bool {
	switch v.(type) {
	case intV, boolV, floatV:
		return true
	}
	return false
}

// isStr reports whether v is a str and returns its text.
func isStr(v Value) (string, bool) {
	s, ok := v.(strV)
	return string(s), ok
}

// ---------------------------------------------------------------- repr / str

// reprer is implemented by the objects that define __repr__.
type reprer interface {
	repr(st *reprState) (string, error)
}

// stringer is implemented by the objects whose __str__ differs from __repr__.
type stringer interface {
	str() (string, error)
}

// reprState tracks the containers being printed, as Py_ReprEnter does, so a
// recursive container prints as [...] or {...}.
type reprState struct{ active []Value }

func (st *reprState) enter(v Value) bool {
	for _, a := range st.active {
		if a == v {
			return false
		}
	}
	st.active = append(st.active, v)
	return true
}

func (st *reprState) leave() { st.active = st.active[:len(st.active)-1] }

func pyRepr(v Value) (string, error) { return reprWith(v, &reprState{}) }

func reprWith(v Value, st *reprState) (string, error) {
	switch x := v.(type) {
	case noneV:
		return "None", nil
	case boolV:
		if x {
			return "True", nil
		}
		return "False", nil
	case intV:
		return intString(x.v)
	case floatV:
		return floatRepr(float64(x)), nil
	case complexV:
		return complexRepr(complex128(x)), nil
	case strV:
		return strRepr(string(x)), nil
	case bytesV:
		return bytesRepr(string(x)), nil
	case *bytearrayV:
		return "bytearray(" + bytesRepr(string(x.b)) + ")", nil
	case ellipsisV:
		return "Ellipsis", nil
	case emptyV:
		return "Empty", nil
	case *listV:
		if !st.enter(x) {
			return "[...]", nil
		}
		defer st.leave()
		return seqRepr("[", "]", x.items, false, st)
	case *tupleV:
		if len(x.items) == 0 {
			return "()", nil
		}
		if !st.enter(x) {
			return "(...)", nil
		}
		defer st.leave()
		return seqRepr("(", ")", x.items, true, st)
	case *dictV:
		if len(x.keys) == 0 {
			return "{}", nil
		}
		if !st.enter(x) {
			return "{...}", nil
		}
		defer st.leave()
		return dictRepr(x, st)
	case rangeV:
		if x.step == 1 {
			return "range(" + strconv.FormatInt(x.start, 10) + ", " + strconv.FormatInt(x.stop, 10) + ")", nil
		}
		return "range(" + strconv.FormatInt(x.start, 10) + ", " + strconv.FormatInt(x.stop, 10) + ", " +
			strconv.FormatInt(x.step, 10) + ")", nil
	case *sliceV:
		parts := make([]string, 3)
		for i, p := range []Value{x.start, x.stop, x.step} {
			s, err := reprWith(p, st)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return "slice(" + strings.Join(parts, ", ") + ")", nil
	case *excV:
		if len(x.args) == 1 {
			s, err := reprWith(x.args[0], st)
			if err != nil {
				return "", err
			}
			return x.Class + "(" + s + ")", nil
		}
		s, err := seqRepr("(", ")", x.args, true, st)
		if err != nil {
			return "", err
		}
		if len(x.args) == 0 {
			s = "()"
		}
		return x.Class + s, nil
	case reprer:
		return x.repr(st)
	}
	return "<" + v.pyType() + " object at " + objAddr(v) + ">", nil
}

func seqRepr(open, close string, items []Value, tuple bool, st *reprState) (string, error) {
	var b strings.Builder
	b.WriteString(open)
	for i, it := range items {
		if i > 0 {
			b.WriteString(", ")
		}
		s, err := reprWith(it, st)
		if err != nil {
			return "", err
		}
		b.WriteString(s)
	}
	if tuple && len(items) == 1 {
		b.WriteByte(',')
	}
	b.WriteString(close)
	return b.String(), nil
}

func dictRepr(d *dictV, st *reprState) (string, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i := range d.keys {
		if i > 0 {
			b.WriteString(", ")
		}
		k, err := reprWith(d.keys[i], st)
		if err != nil {
			return "", err
		}
		val, err := reprWith(d.vals[i], st)
		if err != nil {
			return "", err
		}
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(val)
	}
	b.WriteByte('}')
	return b.String(), nil
}

// pyStr returns Python's str() of the value.
func pyStr(v Value) (string, error) {
	switch x := v.(type) {
	case strV:
		return string(x), nil
	case *excV:
		return x.Message, nil
	case stringer:
		return x.str()
	}
	return pyRepr(v)
}

// maxStrDigits is CPython's default sys.int_info.default_max_str_digits.
const maxStrDigits = 4300

var errIntToStr = "Exceeds the limit (4300 digits) for integer string conversion; use sys.set_int_max_str_digits() to increase the limit"

// intString is str() of an int, refused above 4300 digits as CPython does.
func intString(i *big.Int) (string, error) {
	if i.BitLen() > 14000 {
		s := i.String()
		n := len(s)
		if i.Sign() < 0 {
			n--
		}
		if n > maxStrDigits {
			return "", newPyErr("ValueError", errIntToStr)
		}
		return s, nil
	}
	return i.String(), nil
}

// floatRepr is Python's repr() of a float: the shortest digits that round
// trip, in fixed notation for exponents from -4 to 15, else in exponent form.
func floatRepr(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	epos := strings.IndexByte(s, 'e')
	mant, exp := s[:epos], s[epos+1:]
	e, _ := strconv.Atoi(exp)
	digits := strings.Replace(mant, ".", "", 1)
	decpt := e + 1
	var out string
	if decpt <= -4 || decpt > 16 {
		out = digits[:1]
		if len(digits) > 1 {
			out += "." + digits[1:]
		}
		ee := decpt - 1
		sign := "+"
		if ee < 0 {
			sign = "-"
			ee = -ee
		}
		es := strconv.Itoa(ee)
		if len(es) < 2 {
			es = "0" + es
		}
		out += "e" + sign + es
	} else if decpt <= 0 {
		out = "0." + strings.Repeat("0", -decpt) + digits
	} else if decpt >= len(digits) {
		out = digits + strings.Repeat("0", decpt-len(digits)) + ".0"
	} else {
		out = digits[:decpt] + "." + digits[decpt:]
	}
	if neg {
		return "-" + out
	}
	return out
}

// complexRepr is Python's repr() of a complex: parts in repr's shortest
// form with integral values printed without ".0", the real part omitted when
// it is +0.0.
func complexRepr(c complex128) string {
	re, im := real(c), imag(c)
	part := func(f float64) string {
		s := floatRepr(f)
		return strings.TrimSuffix(s, ".0")
	}
	if re == 0 && !math.Signbit(re) {
		return part(im) + "j"
	}
	ims := part(im)
	if !strings.HasPrefix(ims, "-") {
		ims = "+" + ims
	}
	return "(" + part(re) + ims + "j)"
}

// strRepr is Python's repr() of a str.
func strRepr(s string) string {
	quote := byte('\'')
	if strings.IndexByte(s, '\'') >= 0 && strings.IndexByte(s, '"') < 0 {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote) || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			b.WriteString(`\x`)
			b.WriteString(hex2(int(r)))
		case r < 0x7f:
			b.WriteRune(r)
		case pyRuneIsPrintable(r):
			b.WriteRune(r)
		case r <= 0xff:
			b.WriteString(`\x`)
			b.WriteString(hex2(int(r)))
		case r <= 0xffff:
			b.WriteString(`\u`)
			b.WriteString(hexN(int(r), 4))
		default:
			b.WriteString(`\U`)
			b.WriteString(hexN(int(r), 8))
		}
	}
	b.WriteByte(quote)
	return b.String()
}

// bytesRepr is Python's repr() of a bytes object.
func bytesRepr(s string) string {
	quote := byte('\'')
	if strings.IndexByte(s, '\'') >= 0 && strings.IndexByte(s, '"') < 0 {
		quote = '"'
	}
	var b strings.Builder
	b.WriteString("b")
	b.WriteByte(quote)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == quote || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c < 0x20 || c >= 0x7f:
			b.WriteString(`\x`)
			b.WriteString(hex2(int(c)))
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte(quote)
	return b.String()
}

func hex2(n int) string { return hexN(n, 2) }

func hexN(n, w int) string {
	s := strconv.FormatInt(int64(n), 16)
	for len(s) < w {
		s = "0" + s
	}
	return s
}

// ---------------------------------------------------------------- truth

// lener is implemented by the objects that define __len__.
type lener interface {
	length() (int, error)
}

// truthy is Python's bool().
func truthy(v Value) (bool, error) {
	switch x := v.(type) {
	case noneV:
		return false, nil
	case boolV:
		return bool(x), nil
	case intV:
		return x.v.Sign() != 0, nil
	case floatV:
		return x != 0, nil
	case complexV:
		return x != 0, nil
	case strV:
		return x != "", nil
	case bytesV:
		return x != "", nil
	case *bytearrayV:
		return len(x.b) > 0, nil
	case *listV:
		return len(x.items) > 0, nil
	case *tupleV:
		return len(x.items) > 0, nil
	case *dictV:
		return len(x.keys) > 0, nil
	case rangeV:
		return x.length() > 0, nil
	case lener:
		n, err := x.length()
		return n > 0, err
	}
	return true, nil
}

// ---------------------------------------------------------------- equality

type eqer interface {
	eq(other Value) (bool, error)
}

// pyEq is Python's ==.
func pyEq(a, b Value) (bool, error) {
	if isNumber(a) && isNumber(b) {
		return numCmp(a, b) == 0 && !isNaNVal(a) && !isNaNVal(b), nil
	}
	if ca, ok := asComplexNum(a); ok {
		if cb, ok := asComplexNum(b); ok {
			if _, isC := a.(complexV); isC {
				return complexEq(ca, b), nil
			}
			if _, isC := b.(complexV); isC {
				return complexEq(cb, a), nil
			}
		}
	}
	switch x := a.(type) {
	case noneV:
		_, ok := b.(noneV)
		return ok, nil
	case strV:
		y, ok := b.(strV)
		return ok && x == y, nil
	case bytesV, *bytearrayV:
		ba, _ := bytesOf(a)
		bb, ok := bytesOf(b)
		return ok && ba == bb, nil
	case *listV:
		y, ok := b.(*listV)
		if !ok {
			return false, nil
		}
		if x == y {
			return true, nil
		}
		return seqEq(x.items, y.items)
	case *tupleV:
		y, ok := b.(*tupleV)
		if !ok {
			return false, nil
		}
		return seqEq(x.items, y.items)
	case *dictV:
		y, ok := b.(*dictV)
		if !ok {
			return false, nil
		}
		return dictEq(x, y)
	case rangeV:
		y, ok := b.(rangeV)
		if !ok {
			return false, nil
		}
		lx, ly := x.length(), y.length()
		if lx != ly {
			return false, nil
		}
		if lx == 0 {
			return true, nil
		}
		if x.start != y.start {
			return false, nil
		}
		return lx == 1 || x.step == y.step, nil
	case *sliceV:
		y, ok := b.(*sliceV)
		if !ok {
			return false, nil
		}
		return seqEq([]Value{x.start, x.stop, x.step}, []Value{y.start, y.stop, y.step})
	case eqer:
		return x.eq(b)
	}
	if e, ok := b.(eqer); ok {
		return e.eq(a)
	}
	return sameObject(a, b), nil
}

func dictEq(x, y *dictV) (bool, error) {
	if x == y {
		return true, nil
	}
	if len(x.keys) != len(y.keys) {
		return false, nil
	}
	for i, k := range x.keys {
		v2, found, err := y.get(k)
		if err != nil {
			return false, err
		}
		if !found {
			return false, nil
		}
		if sameObject(x.vals[i], v2) {
			continue
		}
		eq, err := pyEq(x.vals[i], v2)
		if err != nil || !eq {
			return false, err
		}
	}
	return true, nil
}

func complexEq(c complex128, other Value) bool {
	switch o := other.(type) {
	case complexV:
		return c == complex128(o)
	case floatV:
		return imag(c) == 0 && real(c) == float64(o)
	}
	i, ok := asInt(other)
	if !ok || imag(c) != 0 {
		return false
	}
	return floatIntCmp(real(c), i) == 0
}

// asComplexNum returns a number as a complex, for mixed comparisons.
func asComplexNum(v Value) (complex128, bool) {
	switch x := v.(type) {
	case complexV:
		return complex128(x), true
	case floatV:
		return complex(float64(x), 0), true
	case intV, boolV:
		return 0, true
	}
	return 0, false
}

func seqEq(a, b []Value) (bool, error) {
	if len(a) != len(b) {
		return false, nil
	}
	for i := range a {
		// Python compares identical objects as equal before calling __eq__.
		if sameObject(a[i], b[i]) {
			continue
		}
		eq, err := pyEq(a[i], b[i])
		if err != nil || !eq {
			return false, err
		}
	}
	return true, nil
}

// sameObject is Python's `is`. Mutable objects and the singletons compare by
// identity; ints compare equal-valued objects as one when CPython caches them
// (-5 to 256) or when they share their storage.
func sameObject(a, b Value) bool {
	switch x := a.(type) {
	case noneV, boolV, ellipsisV, emptyV:
		return a == b
	case intV:
		y, ok := b.(intV)
		if !ok {
			return false
		}
		if x.v == y.v {
			return true
		}
		return x.v.Cmp(y.v) == 0 && x.v.IsInt64() && x.v.Int64() >= -5 && x.v.Int64() <= 256
	case strV:
		y, ok := b.(strV)
		return ok && x == y && utf8.RuneCountInString(string(x)) <= 1
	case floatV, complexV, bytesV, rangeV:
		return false
	}
	return a == b
}

func isNaNVal(v Value) bool {
	f, ok := v.(floatV)
	return ok && math.IsNaN(float64(f))
}

// numCmp compares two real numbers exactly (ints of any size against
// floats). NaN compares as unordered; callers check for it.
func numCmp(a, b Value) int {
	ia, aInt := asInt(a)
	ib, bInt := asInt(b)
	switch {
	case aInt && bInt:
		return ia.Cmp(ib)
	case aInt:
		return -floatIntCmp(float64(b.(floatV)), ia)
	case bInt:
		return floatIntCmp(float64(a.(floatV)), ib)
	}
	fa, fb := float64(a.(floatV)), float64(b.(floatV))
	switch {
	case fa < fb:
		return -1
	case fa > fb:
		return 1
	}
	return 0
}

// floatIntCmp compares a float with an int exactly.
func floatIntCmp(f float64, i *big.Int) int {
	if math.IsNaN(f) {
		return 2
	}
	if math.IsInf(f, 1) {
		return 1
	}
	if math.IsInf(f, -1) {
		return -1
	}
	bf := new(big.Float).SetPrec(0).SetFloat64(f)
	bi := new(big.Float).SetPrec(uint(i.BitLen() + 64)).SetInt(i)
	return bf.Cmp(bi)
}

// ---------------------------------------------------------------- ordering

// pyLess is Python's <, with its TypeError for unorderable operands.
func pyLess(a, b Value) (bool, error) { return pyCompare("<", a, b) }

// pyCompare evaluates a rich comparison operator.
func pyCompare(op string, a, b Value) (bool, error) {
	switch op {
	case "==":
		return pyEq(a, b)
	case "!=":
		eq, err := pyEq(a, b)
		return !eq, err
	}
	if la, lb, ok := seqPair(a, b); ok {
		return seqRichCompare(op, la, lb)
	}
	if sa, ok := a.(*setV); ok {
		if sb, ok := b.(*setV); ok {
			return setCompare(op, sa, sb)
		}
	}
	c, ok, err := orderCmp(a, b)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, newPyErr("TypeError", "'"+op+"' not supported between instances of '"+a.pyType()+"' and '"+b.pyType()+"'")
	}
	if c == 2 {
		return false, nil
	}
	switch op {
	case "<":
		return c < 0, nil
	case "<=":
		return c <= 0, nil
	case ">":
		return c > 0, nil
	case ">=":
		return c >= 0, nil
	}
	return false, nil
}

// seqPair returns the items of two lists or two tuples.
func seqPair(a, b Value) ([]Value, []Value, bool) {
	switch x := a.(type) {
	case *listV:
		if y, ok := b.(*listV); ok {
			return x.items, y.items, true
		}
	case *tupleV:
		if y, ok := b.(*tupleV); ok {
			return x.items, y.items, true
		}
	}
	return nil, nil, false
}

// seqRichCompare is list_richcompare / tuple_richcompare for an ordering
// operator: the first pair of items that differ is compared with op itself.
func seqRichCompare(op string, a, b []Value) (bool, error) {
	i := 0
	for ; i < len(a) && i < len(b); i++ {
		if sameObject(a[i], b[i]) {
			continue
		}
		eq, err := pyEq(a[i], b[i])
		if err != nil {
			return false, err
		}
		if !eq {
			break
		}
	}
	if i >= len(a) || i >= len(b) {
		la, lb := len(a), len(b)
		switch op {
		case "<":
			return la < lb, nil
		case "<=":
			return la <= lb, nil
		case ">":
			return la > lb, nil
		}
		return la >= lb, nil
	}
	return pyCompare(op, a[i], b[i])
}

// orderCmp returns -1, 0 or 1 (2 for unordered NaN), and false when the two
// types cannot be ordered.
func orderCmp(a, b Value) (int, bool, error) {
	if isNumber(a) && isNumber(b) {
		if isNaNVal(a) || isNaNVal(b) {
			return 2, true, nil
		}
		return numCmp(a, b), true, nil
	}
	if sa, ok := a.(strV); ok {
		if sb, ok := b.(strV); ok {
			// UTF-8 byte order equals code point order.
			return strings.Compare(string(sa), string(sb)), true, nil
		}
		return 0, false, nil
	}
	if ba, ok := bytesOf(a); ok {
		if bb, ok := bytesOf(b); ok {
			return strings.Compare(ba, bb), true, nil
		}
	}
	return 0, false, nil
}

// bytesOf returns the contents of a bytes or bytearray.
func bytesOf(v Value) (string, bool) {
	switch x := v.(type) {
	case bytesV:
		return string(x), true
	case *bytearrayV:
		return string(x.b), true
	}
	return "", false
}

// ---------------------------------------------------------------- range

func (r rangeV) length() int64 {
	if r.step > 0 {
		if r.start >= r.stop {
			return 0
		}
		return (r.stop - r.start + r.step - 1) / r.step
	}
	if r.start <= r.stop {
		return 0
	}
	return (r.start - r.stop - r.step - 1) / (-r.step)
}

func (r rangeV) at(i int64) int64 { return r.start + i*r.step }

// ---------------------------------------------------------------- iteration

// iterator walks the items of an iterable.
type iterator interface {
	next() (Value, bool, error)
}

type sliceIter struct {
	items []Value
	i     int
}

func (s *sliceIter) next() (Value, bool, error) {
	if s.i >= len(s.items) {
		return nil, false, nil
	}
	v := s.items[s.i]
	s.i++
	return v, true, nil
}

// listIter reads a list by index, so it sees changes made while iterating.
type listIter struct {
	l *listV
	i int
}

func (s *listIter) next() (Value, bool, error) {
	if s.i >= len(s.l.items) {
		return nil, false, nil
	}
	v := s.l.items[s.i]
	s.i++
	return v, true, nil
}

type rangeIter struct {
	r rangeV
	i int64
}

func (s *rangeIter) next() (Value, bool, error) {
	if s.i >= s.r.length() {
		return nil, false, nil
	}
	v := mkInt(s.r.at(s.i))
	s.i++
	return v, true, nil
}

type strIter struct {
	s string
	i int
}

func (s *strIter) next() (Value, bool, error) {
	if s.i >= len(s.s) {
		return nil, false, nil
	}
	_, n := utf8.DecodeRuneInString(s.s[s.i:])
	v := strV(s.s[s.i : s.i+n])
	s.i += n
	return v, true, nil
}

// dictIter walks a dict's keys and fails as CPython does when the dict
// changes size while it is iterated.
type dictIter struct {
	d    *dictV
	i    int
	size int
}

func (s *dictIter) next() (Value, bool, error) {
	if len(s.d.keys) != s.size {
		s.size = -1
		return nil, false, newPyErr("RuntimeError", "dictionary changed size during iteration")
	}
	if s.i >= len(s.d.keys) {
		return nil, false, nil
	}
	v := s.d.keys[s.i]
	s.i++
	return v, true, nil
}

type funcIter func() (Value, bool, error)

func (f funcIter) next() (Value, bool, error) { return f() }

// iterable is implemented by objects that support iter().
type iterable interface {
	iter() (iterator, error)
}

// pyIter is Python's iter().
func pyIter(v Value) (iterator, error) {
	switch x := v.(type) {
	case *listV:
		return &listIter{l: x}, nil
	case *tupleV:
		return &sliceIter{items: x.items}, nil
	case *dictV:
		return &dictIter{d: x, size: len(x.keys)}, nil
	case strV:
		return &strIter{s: string(x)}, nil
	case bytesV, *bytearrayV:
		b, _ := bytesOf(x)
		items := make([]Value, len(b))
		for i := 0; i < len(b); i++ {
			items[i] = mkInt(int64(b[i]))
		}
		return &sliceIter{items: items}, nil
	case rangeV:
		return &rangeIter{r: x}, nil
	case iterable:
		return x.iter()
	}
	return nil, newPyErr("TypeError", "'"+v.pyType()+"' object is not iterable")
}

// toSlice is list(v).
func toSlice(v Value) ([]Value, error) {
	switch x := v.(type) {
	case *listV:
		return append([]Value(nil), x.items...), nil
	case *tupleV:
		return append([]Value(nil), x.items...), nil
	}
	it, err := pyIter(v)
	if err != nil {
		return nil, err
	}
	var out []Value
	for {
		item, ok, err := it.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return out, nil
		}
		out = append(out, item)
	}
}

// pyLen is Python's len().
func pyLen(v Value) (int, error) {
	switch x := v.(type) {
	case strV:
		return utf8.RuneCountInString(string(x)), nil
	case bytesV:
		return len(x), nil
	case *bytearrayV:
		return len(x.b), nil
	case *listV:
		return len(x.items), nil
	case *tupleV:
		return len(x.items), nil
	case *dictV:
		return len(x.keys), nil
	case rangeV:
		return int(x.length()), nil
	case lener:
		return x.length()
	}
	return 0, newPyErr("TypeError", "object of type '"+v.pyType()+"' has no len()")
}

// containser is implemented by objects that define __contains__.
type containser interface {
	contains(item Value) (bool, error)
}

// pyContains is Python's `item in container`.
func pyContains(container, item Value) (bool, error) {
	switch c := container.(type) {
	case strV:
		s, ok := item.(strV)
		if !ok {
			return false, newPyErr("TypeError", "'in <string>' requires string as left operand, not "+item.pyType())
		}
		return strings.Contains(string(c), string(s)), nil
	case bytesV, *bytearrayV:
		hay, _ := bytesOf(c)
		if i, ok := asInt(item); ok {
			if !i.IsInt64() || i.Int64() < 0 || i.Int64() > 255 {
				return false, newPyErr("ValueError", "byte must be in range(0, 256)")
			}
			return strings.IndexByte(hay, byte(i.Int64())) >= 0, nil
		}
		needle, ok := bytesOf(item)
		if !ok {
			return false, newPyErr("TypeError", "a bytes-like object is required, not '"+item.pyType()+"'")
		}
		return strings.Contains(hay, needle), nil
	case *dictV:
		_, found, err := c.get(item)
		return found, err
	case *listV:
		return seqContains(c.items, item)
	case *tupleV:
		return seqContains(c.items, item)
	case rangeV:
		if i, ok := asInt(item); ok && i.IsInt64() {
			n := i.Int64()
			if c.step > 0 && (n < c.start || n >= c.stop) || c.step < 0 && (n > c.start || n <= c.stop) {
				return false, nil
			}
			return (n-c.start)%c.step == 0, nil
		}
		if _, ok := item.(intV); ok {
			return false, nil
		}
	case containser:
		return c.contains(item)
	}
	it, err := pyIter(container)
	if err != nil {
		return false, newPyErr("TypeError", "argument of type '"+container.pyType()+"' is not iterable")
	}
	for {
		v, ok, err := it.next()
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		if sameObject(v, item) {
			return true, nil
		}
		eq, err := pyEq(v, item)
		if err != nil {
			return false, err
		}
		if eq {
			return true, nil
		}
	}
}

func seqContains(items []Value, item Value) (bool, error) {
	for _, v := range items {
		if sameObject(v, item) {
			return true, nil
		}
		eq, err := pyEq(v, item)
		if err != nil {
			return false, err
		}
		if eq {
			return true, nil
		}
	}
	return false, nil
}

// Len returns the number of items.
func (d *dictV) Len() int { return len(d.keys) }

// reprOf is repr() for messages, where CPython's own repr cannot fail.
func reprOf(v Value) string {
	s, err := pyRepr(v)
	if err != nil {
		return "<" + v.pyType() + " object>"
	}
	return s
}
