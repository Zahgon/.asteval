// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT
//
// Go port of asteval. Python's operators on the builtin types, with CPython
// 3.12's results and exceptions, and asteval's safe wrappers around them
// (astutils.safe_pow, safe_mult, safe_add and safe_lshift).

package asteval

import (
	"math"
	"math/big"
	"strings"
)

// The limits of astutils.
const (
	maxExponent = 10000
	maxStrLen   = 2 << 17
	maxShift    = 1000
)

func unsupported(op string, a, b Value) error {
	return typeErr("unsupported operand type(s) for %s: '%s' and '%s'", op, a.pyType(), b.pyType())
}

// toFloat converts an int, bool or float to a float64, with Python's
// OverflowError for ints out of range.
func toFloat(v Value) (float64, error) {
	switch n := v.(type) {
	case floatV:
		return float64(n), nil
	case boolV:
		if n {
			return 1, nil
		}
		return 0, nil
	case intV:
		return bigToFloat(n.v)
	}
	return 0, typeErr("must be real number, not %s", v.pyType())
}

func bigToFloat(i *big.Int) (float64, error) {
	if i.IsInt64() {
		n := i.Int64()
		if n > -(1<<53) && n < 1<<53 {
			return float64(n), nil
		}
	}
	f, _ := new(big.Float).SetInt(i).Float64()
	if math.IsInf(f, 0) {
		return 0, newPyErr("OverflowError", "int too large to convert to float")
	}
	return f, nil
}

// toComplex converts a number to a complex128.
func toComplex(v Value) (complex128, error) {
	if c, ok := v.(complexV); ok {
		return complex128(c), nil
	}
	f, err := toFloat(v)
	return complex(f, 0), err
}

func isComplexOperand(a, b Value) bool {
	_, ca := a.(complexV)
	_, cb := b.(complexV)
	return (ca || cb) && (ca || isNumber(a)) && (cb || isNumber(b))
}

// binop applies a binary operator by its ast class name.
func binop(op string, a, b Value) (Value, error) {
	switch op {
	case "Add":
		return pyAdd(a, b)
	case "Sub":
		return pySub(a, b)
	case "Mult":
		return pyMul(a, b)
	case "Div":
		return pyTrueDiv(a, b)
	case "FloorDiv":
		return pyFloorDiv(a, b)
	case "Mod":
		return pyMod(a, b)
	case "Pow":
		return pyPow(a, b)
	case "LShift":
		return pyShift("<<", a, b)
	case "RShift":
		return pyShift(">>", a, b)
	case "BitAnd":
		return pyBitop("&", a, b)
	case "BitOr":
		return pyBitop("|", a, b)
	case "BitXor":
		return pyBitop("^", a, b)
	case "MatMult":
		return nil, unsupported("@", a, b)
	}
	return nil, newPyErr("SystemError", "unknown operator "+op)
}

// ---------------------------------------------------------------- safe wrappers

func isNumbersNumber(v Value) bool {
	switch v.(type) {
	case intV, boolV, floatV, complexV:
		return true
	}
	return false
}

// greaterThan is `v > limit` for a number v, with the TypeError a complex
// raises.
func greaterThan(v Value, limit int64) (bool, error) {
	return pyCompare(">", v, mkInt(limit))
}

// safePow is astutils.safe_pow.
func safePow(base, exp Value) (Value, error) {
	if isNumbersNumber(exp) {
		gt, err := greaterThan(exp, maxExponent)
		if err != nil {
			return nil, err
		}
		if gt {
			return nil, newPyErr("RuntimeError", "Invalid exponent, max exponent is 10000")
		}
	}
	return pyPow(base, exp)
}

// safeMult is astutils.safe_mult.
func safeMult(a, b Value) (Value, error) {
	if s, ok := a.(strV); ok {
		if n, ok := asInt(b); ok {
			l := big.NewInt(int64(len([]rune(string(s)))))
			if l.Mul(l, n).Cmp(big.NewInt(maxStrLen)) > 0 {
				return nil, newPyErr("RuntimeError", "String length exceeded, max string length is 262144")
			}
		}
	}
	return pyMul(a, b)
}

// safeAdd is astutils.safe_add.
func safeAdd(a, b Value) (Value, error) {
	if sa, ok := a.(strV); ok {
		if sb, ok := b.(strV); ok && len([]rune(string(sa)))+len([]rune(string(sb))) > maxStrLen {
			return nil, newPyErr("RuntimeError", "String length exceeded, max string length is 262144")
		}
	}
	return pyAdd(a, b)
}

// safeLshift is astutils.safe_lshift.
func safeLshift(a, b Value) (Value, error) {
	if isNumbersNumber(b) {
		gt, err := greaterThan(b, maxShift)
		if err != nil {
			return nil, err
		}
		if gt {
			return nil, newPyErr("RuntimeError", "Invalid left shift, max left shift is 1000")
		}
	}
	return pyShift("<<", a, b)
}

// safeBinop is astutils.op2func for a binary operator: the safe wrappers for
// +, *, ** and <<, the plain operator for the rest.
func safeBinop(op string, a, b Value) (Value, error) {
	switch op {
	case "Add":
		return safeAdd(a, b)
	case "Mult":
		return safeMult(a, b)
	case "Pow":
		return safePow(a, b)
	case "LShift":
		return safeLshift(a, b)
	}
	return binop(op, a, b)
}

// ---------------------------------------------------------------- + and -

func concatErr(a, b Value) error {
	return typeErr("can only concatenate %s (not \"%s\") to %s", a.pyType(), b.pyType(), a.pyType())
}

func pyAdd(a, b Value) (Value, error) {
	if ia, ok := asInt(a); ok {
		if ib, ok := asInt(b); ok {
			return mkBig(new(big.Int).Add(ia, ib)), nil
		}
	}
	if isComplexOperand(a, b) {
		ca, err := toComplex(a)
		if err != nil {
			return nil, err
		}
		cb, err := toComplex(b)
		if err != nil {
			return nil, err
		}
		return complexV(ca + cb), nil
	}
	if isNumber(a) && isNumber(b) {
		fa, err := toFloat(a)
		if err != nil {
			return nil, err
		}
		fb, err := toFloat(b)
		if err != nil {
			return nil, err
		}
		return floatV(fa + fb), nil
	}
	switch x := a.(type) {
	case strV:
		if y, ok := b.(strV); ok {
			return x + y, nil
		}
		return nil, concatErr(a, b)
	case *listV:
		if y, ok := b.(*listV); ok {
			return newList(append(append([]Value(nil), x.items...), y.items...)), nil
		}
		return nil, concatErr(a, b)
	case *tupleV:
		if y, ok := b.(*tupleV); ok {
			return newTuple(append(append([]Value(nil), x.items...), y.items...)), nil
		}
		return nil, concatErr(a, b)
	case bytesV, *bytearrayV:
		sa, _ := bytesOf(a)
		sb, ok := bytesOf(b)
		if !ok {
			return nil, typeErr("can't concat %s to %s", b.pyType(), a.pyType())
		}
		if _, ok := a.(*bytearrayV); ok {
			return &bytearrayV{b: []byte(sa + sb)}, nil
		}
		return bytesV(sa + sb), nil
	}
	return nil, unsupported("+", a, b)
}

func pySub(a, b Value) (Value, error) {
	if ia, ok := asInt(a); ok {
		if ib, ok := asInt(b); ok {
			return mkBig(new(big.Int).Sub(ia, ib)), nil
		}
	}
	if isComplexOperand(a, b) {
		ca, err := toComplex(a)
		if err != nil {
			return nil, err
		}
		cb, err := toComplex(b)
		if err != nil {
			return nil, err
		}
		return complexV(ca - cb), nil
	}
	if isNumber(a) && isNumber(b) {
		fa, err := toFloat(a)
		if err != nil {
			return nil, err
		}
		fb, err := toFloat(b)
		if err != nil {
			return nil, err
		}
		return floatV(fa - fb), nil
	}
	if sa, ok := a.(*setV); ok {
		if sb, ok := b.(*setV); ok {
			return setDifference(sa, sb, sa.frozen)
		}
	}
	return nil, unsupported("-", a, b)
}

// ---------------------------------------------------------------- *

// repeatCount is the repeat count of sequence * n: operator.index of n, with
// Python's OverflowError for a count that does not fit.
func repeatCount(v Value) (int, bool, error) {
	i, ok := asInt(v)
	if !ok {
		return 0, false, nil
	}
	if !i.IsInt64() {
		if i.Sign() < 0 {
			return 0, true, nil
		}
		return 0, true, newPyErr("OverflowError", "cannot fit 'int' into an index-sized integer")
	}
	n := i.Int64()
	if n < 0 {
		n = 0
	}
	if n > math.MaxInt32*64 {
		return 0, true, newPyErr("MemoryError", "")
	}
	return int(n), true, nil
}

// maxRepeat bounds the size of a repeated sequence; CPython would run out of
// memory well before it.
const maxRepeat = 1 << 31

func repeatTooBig(unit, n int) bool { return unit > 0 && n > maxRepeat/unit }

func repeatItems(items []Value, n int) []Value {
	out := make([]Value, 0, len(items)*n)
	for i := 0; i < n; i++ {
		out = append(out, items...)
	}
	return out
}

func repeatSeq(seq, count Value) (Value, bool, error) {
	switch seq.(type) {
	case strV, *listV, *tupleV, bytesV, *bytearrayV:
	default:
		return nil, false, nil
	}
	n, ok, err := repeatCount(count)
	if !ok {
		if isNumbersNumber(count) || true {
			return nil, true, typeErr("can't multiply sequence by non-int of type '%s'", count.pyType())
		}
	}
	if err != nil {
		return nil, true, err
	}
	switch x := seq.(type) {
	case strV:
		if repeatTooBig(len(x), n) {
			return nil, true, newPyErr("MemoryError", "")
		}
		return strV(strings.Repeat(string(x), n)), true, nil
	case bytesV:
		if repeatTooBig(len(x), n) {
			return nil, true, newPyErr("MemoryError", "")
		}
		return bytesV(strings.Repeat(string(x), n)), true, nil
	case *bytearrayV:
		return &bytearrayV{b: []byte(strings.Repeat(string(x.b), n))}, true, nil
	case *listV:
		if repeatTooBig(len(x.items), n) {
			return nil, true, newPyErr("MemoryError", "")
		}
		return newList(repeatItems(x.items, n)), true, nil
	case *tupleV:
		if repeatTooBig(len(x.items), n) {
			return nil, true, newPyErr("MemoryError", "")
		}
		return newTuple(repeatItems(x.items, n)), true, nil
	}
	return nil, false, nil
}

func pyMul(a, b Value) (Value, error) {
	if ia, ok := asInt(a); ok {
		if ib, ok := asInt(b); ok {
			return mkBig(new(big.Int).Mul(ia, ib)), nil
		}
	}
	if isComplexOperand(a, b) {
		ca, err := toComplex(a)
		if err != nil {
			return nil, err
		}
		cb, err := toComplex(b)
		if err != nil {
			return nil, err
		}
		return complexV(complexMul(ca, cb)), nil
	}
	if isNumber(a) && isNumber(b) {
		fa, err := toFloat(a)
		if err != nil {
			return nil, err
		}
		fb, err := toFloat(b)
		if err != nil {
			return nil, err
		}
		return floatV(fa * fb), nil
	}
	if r, ok, err := repeatSeq(a, b); ok {
		return r, err
	}
	if _, ok := asInt(a); ok {
		if r, ok, err := repeatSeq(b, a); ok {
			return r, err
		}
	}
	switch b.(type) {
	case strV, *listV, *tupleV, bytesV, *bytearrayV:
		if !isNumbersNumber(a) || true {
			if _, isInt := asInt(a); !isInt {
				return nil, typeErr("can't multiply sequence by non-int of type '%s'", a.pyType())
			}
		}
	}
	return nil, unsupported("*", a, b)
}

// complexMul is _Py_c_prod. Each product is rounded on its own (the
// float64 conversions stop the compiler from fusing a multiply and an add,
// which CPython does not do).
func complexMul(a, b complex128) complex128 {
	return complex(float64(real(a)*real(b))-float64(imag(a)*imag(b)),
		float64(real(a)*imag(b))+float64(imag(a)*real(b)))
}

// complexQuot is CPython's _Py_c_quot; ok is false for a zero divisor.
func complexQuot(a, b complex128) (complex128, bool) {
	ar, ai, br, bi := real(a), imag(a), real(b), imag(b)
	absBr, absBi := math.Abs(br), math.Abs(bi)
	switch {
	case absBr >= absBi:
		if absBr == 0 {
			return 0, false
		}
		ratio := bi / br
		denom := br + float64(bi*ratio)
		return complex((ar+float64(ai*ratio))/denom, (ai-float64(ar*ratio))/denom), true
	case absBi >= absBr:
		ratio := br / bi
		denom := float64(br*ratio) + bi
		return complex((float64(ar*ratio)+ai)/denom, (float64(ai*ratio)-ar)/denom), true
	}
	return complex(math.NaN(), math.NaN()), true
}

// ---------------------------------------------------------------- / // %

func pyTrueDiv(a, b Value) (Value, error) {
	if isComplexOperand(a, b) {
		ca, err := toComplex(a)
		if err != nil {
			return nil, err
		}
		cb, err := toComplex(b)
		if err != nil {
			return nil, err
		}
		q, ok := complexQuot(ca, cb)
		if !ok {
			return nil, newPyErr("ZeroDivisionError", "complex division by zero")
		}
		return complexV(q), nil
	}
	if !isNumber(a) || !isNumber(b) {
		return nil, unsupported("/", a, b)
	}
	ia, aInt := asInt(a)
	ib, bInt := asInt(b)
	if aInt && bInt {
		if ib.Sign() == 0 {
			return nil, newPyErr("ZeroDivisionError", "division by zero")
		}
		if ia.IsInt64() && ib.IsInt64() {
			x, y := ia.Int64(), ib.Int64()
			if x > -(1<<53) && x < 1<<53 && y > -(1<<53) && y < 1<<53 {
				return floatV(float64(x) / float64(y)), nil
			}
		}
		f, _ := new(big.Rat).SetFrac(ia, ib).Float64()
		if math.IsInf(f, 0) {
			return nil, newPyErr("OverflowError", "integer division result too large for a float")
		}
		return floatV(f), nil
	}
	fa, err := toFloat(a)
	if err != nil {
		return nil, err
	}
	fb, err := toFloat(b)
	if err != nil {
		return nil, err
	}
	if fb == 0 {
		return nil, newPyErr("ZeroDivisionError", "float division by zero")
	}
	return floatV(fa / fb), nil
}

// intFloorDivMod is Python's divmod for ints.
func intFloorDivMod(a, b *big.Int) (*big.Int, *big.Int) {
	q, r := new(big.Int).QuoRem(a, b, new(big.Int))
	if r.Sign() != 0 && (r.Sign() < 0) != (b.Sign() < 0) {
		q.Sub(q, big.NewInt(1))
		r.Add(r, b)
	}
	return q, r
}

// floatDivMod is CPython's float divmod.
func floatDivMod(vx, wx float64) (float64, float64) {
	mod := math.Mod(vx, wx)
	div := (vx - mod) / wx
	if mod != 0 {
		if (wx < 0) != (mod < 0) {
			mod += wx
			div -= 1.0
		}
	} else {
		mod = math.Copysign(0, wx)
	}
	var floordiv float64
	if div != 0 {
		floordiv = math.Floor(div)
		if div-floordiv > 0.5 {
			floordiv += 1.0
		}
	} else {
		floordiv = math.Copysign(0, vx/wx)
	}
	return floordiv, mod
}

func pyFloorDiv(a, b Value) (Value, error) {
	if !isNumber(a) || !isNumber(b) {
		return nil, unsupported("//", a, b)
	}
	ia, aInt := asInt(a)
	ib, bInt := asInt(b)
	if aInt && bInt {
		if ib.Sign() == 0 {
			return nil, newPyErr("ZeroDivisionError", "integer division or modulo by zero")
		}
		q, _ := intFloorDivMod(ia, ib)
		return mkBig(q), nil
	}
	fa, err := toFloat(a)
	if err != nil {
		return nil, err
	}
	fb, err := toFloat(b)
	if err != nil {
		return nil, err
	}
	if fb == 0 {
		return nil, newPyErr("ZeroDivisionError", "float floor division by zero")
	}
	q, _ := floatDivMod(fa, fb)
	return floatV(q), nil
}

func pyMod(a, b Value) (Value, error) {
	switch x := a.(type) {
	case strV:
		s, err := percentFormat(string(x), b)
		if err != nil {
			return nil, err
		}
		return strV(s), nil
	case bytesV:
		return nil, unsupported("%", a, b)
	}
	if !isNumber(a) || !isNumber(b) {
		return nil, unsupported("%", a, b)
	}
	ia, aInt := asInt(a)
	ib, bInt := asInt(b)
	if aInt && bInt {
		if ib.Sign() == 0 {
			return nil, newPyErr("ZeroDivisionError", "integer modulo by zero")
		}
		_, r := intFloorDivMod(ia, ib)
		return mkBig(r), nil
	}
	fa, err := toFloat(a)
	if err != nil {
		return nil, err
	}
	fb, err := toFloat(b)
	if err != nil {
		return nil, err
	}
	if fb == 0 {
		return nil, newPyErr("ZeroDivisionError", "float modulo")
	}
	_, m := floatDivMod(fa, fb)
	return floatV(m), nil
}

// pyDivmod is the divmod() builtin.
func pyDivmod(a, b Value) (Value, error) {
	if !isNumber(a) || !isNumber(b) {
		return nil, unsupported("divmod()", a, b)
	}
	ia, aInt := asInt(a)
	ib, bInt := asInt(b)
	if aInt && bInt {
		if ib.Sign() == 0 {
			return nil, newPyErr("ZeroDivisionError", "integer division or modulo by zero")
		}
		q, r := intFloorDivMod(ia, ib)
		return newTuple([]Value{mkBig(q), mkBig(r)}), nil
	}
	fa, err := toFloat(a)
	if err != nil {
		return nil, err
	}
	fb, err := toFloat(b)
	if err != nil {
		return nil, err
	}
	if fb == 0 {
		return nil, newPyErr("ZeroDivisionError", "float divmod()")
	}
	q, m := floatDivMod(fa, fb)
	return newTuple([]Value{floatV(q), floatV(m)}), nil
}

// ---------------------------------------------------------------- **

func pyPow(a, b Value) (Value, error) {
	if isComplexOperand(a, b) {
		ca, err := toComplex(a)
		if err != nil {
			return nil, err
		}
		cb, err := toComplex(b)
		if err != nil {
			return nil, err
		}
		return complexPow(ca, cb)
	}
	if !isNumber(a) || !isNumber(b) {
		return nil, typeErr("unsupported operand type(s) for ** or pow(): '%s' and '%s'", a.pyType(), b.pyType())
	}
	ia, aInt := asInt(a)
	ib, bInt := asInt(b)
	if aInt && bInt && ib.Sign() >= 0 {
		if ib.BitLen() > 40 && ia.CmpAbs(big.NewInt(1)) > 0 {
			return nil, newPyErr("MemoryError", "")
		}
		return mkBig(new(big.Int).Exp(ia, ib, nil)), nil
	}
	fa, err := toFloat(a)
	if err != nil {
		return nil, err
	}
	fb, err := toFloat(b)
	if err != nil {
		return nil, err
	}
	return floatPow(fa, fb)
}

// floatPow is CPython's float_pow. A negative base with a non-integral
// exponent is computed as a complex power, as float_pow delegates it.
func floatPow(iv, iw float64) (Value, error) {
	if iw == 0 {
		return floatV(1.0), nil
	}
	if math.IsNaN(iv) {
		return floatV(iv), nil
	}
	if math.IsNaN(iw) {
		if iv == 1.0 {
			return floatV(1.0), nil
		}
		return floatV(iw), nil
	}
	if math.IsInf(iw, 0) {
		av := math.Abs(iv)
		switch {
		case av == 1.0:
			return floatV(1.0), nil
		case (iw > 0) == (av > 1.0):
			return floatV(math.Abs(iw)), nil
		default:
			return floatV(0.0), nil
		}
	}
	if math.IsInf(iv, 0) {
		odd := math.Mod(math.Abs(iw), 2.0) == 1.0
		if iw > 0 {
			if odd {
				return floatV(iv), nil
			}
			return floatV(math.Abs(iv)), nil
		}
		if odd {
			return floatV(math.Copysign(0, iv)), nil
		}
		return floatV(0.0), nil
	}
	if iv == 0 {
		odd := math.Mod(math.Abs(iw), 2.0) == 1.0
		if iw < 0 {
			return nil, newPyErr("ZeroDivisionError", "0.0 cannot be raised to a negative power")
		}
		if odd {
			return floatV(iv), nil
		}
		return floatV(0.0), nil
	}
	negate := false
	if iv < 0 {
		if iw != math.Floor(iw) {
			return complexPow(complex(iv, 0), complex(iw, 0))
		}
		iv = -iv
		negate = math.Mod(math.Abs(iw), 2.0) == 1.0
	}
	if iv == 1.0 {
		if negate {
			return floatV(-1.0), nil
		}
		return floatV(1.0), nil
	}
	ix := accuratePow(iv, iw)
	if math.IsInf(ix, 0) {
		return nil, newPyErr("OverflowError", "(34, 'Numerical result out of range')")
	}
	if negate {
		ix = -ix
	}
	return floatV(ix), nil
}

// accuratePow is pow(x, y) for x > 0 rounded as the C library rounds it:
// exactly (rounded once) for integer exponents, sqrt for 0.5.
func accuratePow(x, y float64) float64 {
	if y == 0.5 {
		return math.Sqrt(x)
	}
	if y != math.Trunc(y) || math.Abs(y) > 4096 {
		return precisePow(x, y)
	}
	n := int64(math.Abs(y))
	const prec = 4096
	base := new(big.Float).SetPrec(prec).SetFloat64(x)
	acc := new(big.Float).SetPrec(prec).SetInt64(1)
	for n > 0 {
		if n&1 == 1 {
			acc.Mul(acc, base)
		}
		base.Mul(base, base)
		n >>= 1
	}
	if y < 0 {
		acc.Quo(new(big.Float).SetPrec(prec).SetInt64(1), acc)
	}
	f, _ := acc.Float64()
	return f
}

// complexPow is CPython's complex_pow: repeated multiplication for small
// integral exponents (c_powi), the polar formula (_Py_c_pow) otherwise.
func complexPow(a, b complex128) (Value, error) {
	var r complex128
	n := real(b)
	if imag(b) == 0 && n == math.Trunc(n) && math.Abs(n) <= 100 {
		k := int64(n)
		if k >= 0 {
			r = complexPowu(a, k)
		} else {
			p := complexPowu(a, -k)
			q, ok := complexQuot(1, p)
			if !ok {
				return nil, newPyErr("ZeroDivisionError", "0.0 to a negative or complex power")
			}
			r = q
		}
	} else {
		switch {
		case b == 0:
			r = 1
		case a == 0:
			if imag(b) != 0 || real(b) < 0 {
				return nil, newPyErr("ZeroDivisionError", "0.0 to a negative or complex power")
			}
			r = 0
		default:
			vabs := cHypot(real(a), imag(a))
			length := accuratePowSigned(vabs, real(b))
			at := math.Atan2(imag(a), real(a))
			phase := at * real(b)
			if imag(b) != 0 {
				length /= preciseExp(float64(at * imag(b)))
				phase += float64(imag(b) * preciseLog(vabs))
			}
			r = complex(float64(length*math.Cos(phase)), float64(length*math.Sin(phase)))
		}
	}
	if math.IsInf(real(r), 0) || math.IsInf(imag(r), 0) {
		return nil, newPyErr("OverflowError", "complex exponentiation")
	}
	return complexV(r), nil
}

func complexPowu(x complex128, n int64) complex128 {
	r := complex128(1)
	p := x
	for mask := int64(1); mask > 0 && n >= mask; mask <<= 1 {
		if n&mask != 0 {
			r = complexMul(r, p)
		}
		p = complexMul(p, p)
	}
	return r
}

// ---------------------------------------------------------------- bits

func pyShift(op string, a, b Value) (Value, error) {
	ia, aok := asInt(a)
	ib, bok := asInt(b)
	if !aok || !bok {
		return nil, unsupported(op, a, b)
	}
	if ib.Sign() < 0 {
		return nil, newPyErr("ValueError", "negative shift count")
	}
	if op == ">>" {
		if !ib.IsInt64() || ib.Int64() > 1<<40 {
			if ia.Sign() < 0 {
				return mkInt(-1), nil
			}
			return mkInt(0), nil
		}
		return mkBig(new(big.Int).Rsh(ia, uint(ib.Int64()))), nil
	}
	if ia.Sign() == 0 {
		return mkInt(0), nil
	}
	if !ib.IsInt64() || ib.Int64() > 1<<40 {
		return nil, newPyErr("OverflowError", "too many digits in integer")
	}
	return mkBig(new(big.Int).Lsh(ia, uint(ib.Int64()))), nil
}

func pyBitop(op string, a, b Value) (Value, error) {
	if ba, ok := a.(boolV); ok {
		if bb, ok := b.(boolV); ok {
			switch op {
			case "&":
				return pyBool(bool(ba) && bool(bb)), nil
			case "|":
				return pyBool(bool(ba) || bool(bb)), nil
			}
			return pyBool(ba != bb), nil
		}
	}
	if ia, ok := asInt(a); ok {
		if ib, ok := asInt(b); ok {
			switch op {
			case "&":
				return mkBig(new(big.Int).And(ia, ib)), nil
			case "|":
				return mkBig(new(big.Int).Or(ia, ib)), nil
			}
			return mkBig(new(big.Int).Xor(ia, ib)), nil
		}
	}
	if sa, ok := a.(*setV); ok {
		if sb, ok := b.(*setV); ok {
			switch op {
			case "&":
				return setIntersection(sa, sb, sa.frozen)
			case "|":
				return setUnion(sa, []Value{sb}, sa.frozen)
			}
			return setSymmetricDifference(sa, sb, sa.frozen)
		}
	}
	if da, ok := a.(*dictV); ok && op == "|" {
		if db, ok := b.(*dictV); ok {
			r := da.copy()
			for i, k := range db.keys {
				if err := r.set(k, db.vals[i]); err != nil {
					return nil, err
				}
			}
			return r, nil
		}
	}
	return nil, unsupported(op, a, b)
}

// ---------------------------------------------------------------- unary

// unop applies a unary operator by its ast class name.
func unop(op string, v Value) (Value, error) {
	switch op {
	case "Not":
		t, err := truthy(v)
		return pyBool(!t), err
	case "Invert":
		if i, ok := asInt(v); ok {
			return mkBig(new(big.Int).Not(i)), nil
		}
		return nil, typeErr("bad operand type for unary ~: '%s'", v.pyType())
	}
	sym := map[string]string{"USub": "-", "UAdd": "+"}[op]
	switch n := v.(type) {
	case intV, boolV:
		i, _ := asInt(n)
		if sym == "-" {
			return mkBig(new(big.Int).Neg(i)), nil
		}
		return mkBig(i), nil
	case floatV:
		if sym == "-" {
			return floatV(-n), nil
		}
		return n, nil
	case complexV:
		if sym == "-" {
			return complexV(complex(-real(n), -imag(n))), nil
		}
		return n, nil
	}
	return nil, typeErr("bad operand type for unary %s: '%s'", sym, v.pyType())
}

// cHypot is C's hypot, correctly rounded.
func cHypot(x, y float64) float64 {
	x, y = math.Abs(x), math.Abs(y)
	switch {
	case math.IsInf(x, 0) || math.IsInf(y, 0):
		return math.Inf(1)
	case math.IsNaN(x) || math.IsNaN(y):
		return math.NaN()
	case x == 0:
		return y
	case y == 0:
		return x
	}
	bx := new(big.Float).SetPrec(300).SetFloat64(x)
	by := new(big.Float).SetPrec(300).SetFloat64(y)
	bx.Mul(bx, bx)
	by.Mul(by, by)
	r, _ := bx.Add(bx, by).Sqrt(bx).Float64()
	return r
}
