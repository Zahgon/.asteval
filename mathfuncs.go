// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT
//
// Go port of asteval. The math module functions asteval copies into its
// symbol table (astutils.FROM_MATH), with CPython 3.12's domain and range
// errors (mathmodule.c).

package asteval

import (
	"math"
	"math/big"
)

// fromMath is astutils.FROM_MATH, in order.
var fromMath = []string{"acos", "acosh", "asin", "asinh", "atan", "atan2", "atanh", "ceil", "copysign", "cos",
	"cosh", "degrees", "e", "exp", "fabs", "factorial", "floor", "fmod", "frexp", "fsum", "hypot", "isinf",
	"isnan", "ldexp", "log", "log10", "log1p", "modf", "pi", "pow", "radians", "sin", "sinh", "sqrt", "tan",
	"tanh", "trunc"}

var mathTable = map[string]Value{}

func mathItems() []kwItem {
	out := make([]kwItem, len(fromMath))
	for i, name := range fromMath {
		out[i] = kwItem{name, mathTable[name]}
	}
	return out
}

func domainErr() error { return valueErr("math domain error") }
func rangeErr() error  { return newPyErr("OverflowError", "math range error") }

// mathFloat is PyFloat_AsDouble for a math function's argument.
func mathFloat(v Value) (float64, error) {
	switch v.(type) {
	case intV, boolV, floatV:
		return toFloat(v)
	}
	return 0, typeErr("must be real number, not %s", v.pyType())
}

// math1 is mathmodule's math_1: a function of one float, with its NaN and
// infinity checks.
func math1(name string, fn func(float64) float64, canOverflow bool) func(c *callCtx, args []Value, kw *dictV) (Value, error) {
	return func(c *callCtx, args []Value, kw *dictV) (Value, error) {
		if err := methodsArgsOne("math."+name, args, kw); err != nil {
			return nil, err
		}
		x, err := mathFloat(args[0])
		if err != nil {
			return nil, err
		}
		r := fn(x)
		if math.IsNaN(r) && !math.IsNaN(x) {
			return nil, domainErr()
		}
		if math.IsInf(r, 0) && !math.IsInf(x, 0) && !math.IsNaN(x) {
			if canOverflow {
				return nil, rangeErr()
			}
			return nil, domainErr()
		}
		return floatV(r), nil
	}
}

// math2 is mathmodule's math_2.
func math2(name string, fn func(x, y float64) float64) func(c *callCtx, args []Value, kw *dictV) (Value, error) {
	return func(c *callCtx, args []Value, kw *dictV) (Value, error) {
		if err := posOnly(name, 2, 2, args, kw); err != nil {
			return nil, err
		}
		x, err := mathFloat(args[0])
		if err != nil {
			return nil, err
		}
		y, err := mathFloat(args[1])
		if err != nil {
			return nil, err
		}
		r := fn(x, y)
		finite := func(f float64) bool { return !math.IsInf(f, 0) && !math.IsNaN(f) }
		if math.IsNaN(r) {
			if !math.IsNaN(x) && !math.IsNaN(y) {
				return nil, domainErr()
			}
		} else if math.IsInf(r, 0) && finite(x) && finite(y) {
			return nil, rangeErr()
		}
		return floatV(r), nil
	}
}

func init() {
	mathTable["e"] = floatV(math.E)
	mathTable["pi"] = floatV(math.Pi)
	fns := map[string]func(c *callCtx, args []Value, kw *dictV) (Value, error){
		"acos":     math1("acos", preciseAcos, false),
		"acosh":    math1("acosh", preciseAcosh, false),
		"asin":     math1("asin", preciseAsin, false),
		"asinh":    math1("asinh", preciseAsinh, false),
		"atan":     math1("atan", preciseAtan, false),
		"atanh":    math1("atanh", preciseAtanh, false),
		"cos":      math1("cos", preciseCos, false),
		"cosh":     math1("cosh", preciseCosh, true),
		"sin":      math1("sin", preciseSin, false),
		"sinh":     math1("sinh", preciseSinh, true),
		"tan":      math1("tan", preciseTan, false),
		"tanh":     math1("tanh", preciseTanh, false),
		"exp":      math1("exp", math.Exp, true),
		"fabs":     math1("fabs", math.Abs, false),
		"sqrt":     math1("sqrt", math.Sqrt, false),
		"log1p":    math1("log1p", preciseLog1p, false),
		"degrees":  math1("degrees", func(x float64) float64 { return x * (180.0 / math.Pi) }, false),
		"radians":  math1("radians", func(x float64) float64 { return x * (math.Pi / 180.0) }, false),
		"atan2":    math2("atan2", preciseAtan2),
		"copysign": math2("copysign", math.Copysign),
		"fmod": math2("fmod", func(x, y float64) float64 {
			if math.IsInf(y, 0) && !math.IsInf(x, 0) && !math.IsNaN(x) {
				return x
			}
			return math.Mod(x, y)
		}),
		"ceil":      mathRound("ceil", math.Ceil),
		"floor":     mathRound("floor", math.Floor),
		"trunc":     mathTrunc,
		"factorial": mathFactorial,
		"frexp":     mathFrexp,
		"modf":      mathModf,
		"ldexp":     mathLdexp,
		"fsum":      mathFsum,
		"hypot":     mathHypot,
		"isinf":     mathPred("isinf", func(x float64) bool { return math.IsInf(x, 0) }),
		"isnan":     mathPred("isnan", math.IsNaN),
		"log":       mathLog,
		"log10":     mathLogN("log10", preciseLog10),
		"pow":       mathPow,
	}
	for name, fn := range fns {
		mathTable[name] = newBuiltin(name, fn)
	}
}

func mathPred(name string, fn func(float64) bool) func(c *callCtx, args []Value, kw *dictV) (Value, error) {
	return func(c *callCtx, args []Value, kw *dictV) (Value, error) {
		if err := methodsArgsOne("math."+name, args, kw); err != nil {
			return nil, err
		}
		x, err := mathFloat(args[0])
		if err != nil {
			return nil, err
		}
		return pyBool(fn(x)), nil
	}
}

// mathRound is math.ceil / math.floor: an int's own value, a float rounded
// and converted to int.
func mathRound(name string, fn func(float64) float64) func(c *callCtx, args []Value, kw *dictV) (Value, error) {
	return func(c *callCtx, args []Value, kw *dictV) (Value, error) {
		if err := methodsArgsOne("math."+name, args, kw); err != nil {
			return nil, err
		}
		if i, ok := asInt(args[0]); ok {
			return mkBig(new(big.Int).Set(i)), nil
		}
		x, err := mathFloat(args[0])
		if err != nil {
			return nil, err
		}
		return floatToInt(fn(x))
	}
}

func floatToInt(f float64) (Value, error) {
	if math.IsInf(f, 0) {
		return nil, newPyErr("OverflowError", "cannot convert float infinity to integer")
	}
	if math.IsNaN(f) {
		return nil, valueErr("cannot convert float NaN to integer")
	}
	i, _ := big.NewFloat(f).Int(nil)
	return mkBig(i), nil
}

func mathTrunc(c *callCtx, args []Value, kw *dictV) (Value, error) {
	if err := methodsArgsOne("math.trunc", args, kw); err != nil {
		return nil, err
	}
	switch x := args[0].(type) {
	case intV, boolV:
		i, _ := asInt(x)
		return mkBig(new(big.Int).Set(i)), nil
	case floatV:
		return floatToInt(math.Trunc(float64(x)))
	}
	return nil, typeErr("type %s doesn't define __trunc__ method", args[0].pyType())
}

func mathFactorial(c *callCtx, args []Value, kw *dictV) (Value, error) {
	if err := methodsArgsOne("math.factorial", args, kw); err != nil {
		return nil, err
	}
	n, err := methodsIndex(args[0])
	if err != nil {
		return nil, err
	}
	if !n.IsInt64() {
		if n.Sign() < 0 {
			return nil, valueErr("factorial() not defined for negative values")
		}
		return nil, newPyErr("OverflowError", "factorial() argument should not exceed 9223372036854775807")
	}
	if n.Int64() < 0 {
		return nil, valueErr("factorial() not defined for negative values")
	}
	if n.Int64() > 20000 {
		return nil, newPyErr("MemoryError", "")
	}
	return mkBig(new(big.Int).MulRange(1, n.Int64())), nil
}

func mathFrexp(c *callCtx, args []Value, kw *dictV) (Value, error) {
	if err := methodsArgsOne("math.frexp", args, kw); err != nil {
		return nil, err
	}
	x, err := mathFloat(args[0])
	if err != nil {
		return nil, err
	}
	if math.IsInf(x, 0) || math.IsNaN(x) || x == 0 {
		return newTuple([]Value{floatV(x), mkInt(0)}), nil
	}
	m, e := math.Frexp(x)
	return newTuple([]Value{floatV(m), mkInt(int64(e))}), nil
}

func mathModf(c *callCtx, args []Value, kw *dictV) (Value, error) {
	if err := methodsArgsOne("math.modf", args, kw); err != nil {
		return nil, err
	}
	x, err := mathFloat(args[0])
	if err != nil {
		return nil, err
	}
	if math.IsInf(x, 0) {
		return newTuple([]Value{floatV(math.Copysign(0, x)), floatV(x)}), nil
	}
	ip, frac := math.Modf(x)
	return newTuple([]Value{floatV(frac), floatV(ip)}), nil
}

func mathLdexp(c *callCtx, args []Value, kw *dictV) (Value, error) {
	if err := posOnly("ldexp", 2, 2, args, kw); err != nil {
		return nil, err
	}
	x, err := mathFloat(args[0])
	if err != nil {
		return nil, err
	}
	i, ok := asInt(args[1])
	if !ok {
		return nil, typeErr("Expected an int as second argument to ldexp.")
	}
	if x == 0 || math.IsInf(x, 0) || math.IsNaN(x) {
		return floatV(x), nil
	}
	var exp int
	switch {
	case i.Cmp(big.NewInt(100000)) > 0:
		return nil, rangeErr()
	case i.Cmp(big.NewInt(-100000)) < 0:
		return floatV(math.Copysign(0, x)), nil
	default:
		exp = int(i.Int64())
	}
	r := math.Ldexp(x, exp)
	if math.IsInf(r, 0) {
		return nil, rangeErr()
	}
	return floatV(r), nil
}

// mathFsum is math.fsum, mathmodule.c's math_fsum: Shewchuk's exact
// partial sums, with CPython's overflow and special-value handling.
func mathFsum(c *callCtx, args []Value, kw *dictV) (Value, error) {
	if err := methodsArgsOne("math.fsum", args, kw); err != nil {
		return nil, err
	}
	it, err := pyIter(args[0])
	if err != nil {
		return nil, err
	}
	var p []float64
	specialSum, infSum := 0.0, 0.0
	for {
		item, ok, err := it.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		x, err := mathFloat(item)
		if err != nil {
			return nil, err
		}
		xsave := x
		i := 0
		for _, y := range p {
			if math.Abs(x) < math.Abs(y) {
				x, y = y, x
			}
			hi := x + y
			yr := hi - x
			lo := y - yr
			if lo != 0 {
				p[i] = lo
				i++
			}
			x = hi
		}
		p = p[:i]
		if x != 0 {
			if math.IsInf(x, 0) || math.IsNaN(x) {
				if !math.IsInf(xsave, 0) && !math.IsNaN(xsave) {
					return nil, newPyErr("OverflowError", "intermediate overflow in fsum")
				}
				if math.IsInf(xsave, 0) {
					infSum += xsave
				}
				specialSum += xsave
				p = p[:0]
			} else {
				p = append(p, x)
			}
		}
	}
	if specialSum != 0 || math.IsNaN(specialSum) {
		if math.IsNaN(infSum) {
			return nil, valueErr("-inf + inf in fsum")
		}
		return floatV(specialSum), nil
	}
	hi := 0.0
	if n := len(p); n > 0 {
		n--
		hi = p[n]
		lo := 0.0
		for n > 0 {
			x := hi
			n--
			y := p[n]
			hi = x + y
			yr := hi - x
			lo = y - yr
			if lo != 0 {
				break
			}
		}
		if n > 0 && (lo < 0 && p[n-1] < 0 || lo > 0 && p[n-1] > 0) {
			y := lo * 2.0
			x := hi + y
			yr := x - hi
			if y == yr {
				hi = x
			}
		}
	}
	return floatV(hi), nil
}

// mathHypot is math.hypot(*coordinates), CPython 3.12's vector_norm.
func mathHypot(c *callCtx, args []Value, kw *dictV) (Value, error) {
	if err := noKw("hypot", kw); err != nil {
		return nil, err
	}
	vec := make([]float64, len(args))
	maxv, foundNaN := 0.0, false
	for i, a := range args {
		x, err := mathFloat(a)
		if err != nil {
			return nil, err
		}
		x = math.Abs(x)
		vec[i] = x
		foundNaN = foundNaN || math.IsNaN(x)
		if x > maxv {
			maxv = x
		}
	}
	return floatV(vectorNorm(vec, maxv, foundNaN)), nil
}

func vectorNorm(vec []float64, maxv float64, foundNaN bool) float64 {
	if math.IsInf(maxv, 0) {
		return maxv
	}
	if foundNaN {
		return math.NaN()
	}
	if maxv == 0 || len(vec) <= 1 {
		return maxv
	}
	_, maxE := math.Frexp(maxv)
	if maxE < -1023 {
		for i := range vec {
			vec[i] /= 2.2250738585072014e-308
		}
		return 2.2250738585072014e-308 * vectorNorm(vec, maxv/2.2250738585072014e-308, foundNaN)
	}
	scale := math.Ldexp(1.0, -maxE)
	csum, frac1, frac2 := 1.0, 0.0, 0.0
	for _, x := range vec {
		x = float64(x * scale)
		prHi := float64(x * x)
		prLo := math.FMA(x, x, -prHi)
		smHi := csum + prHi
		smLo := prHi - (smHi - csum)
		csum = smHi
		frac1 += prLo
		frac2 += smLo
	}
	h := math.Sqrt(csum - 1.0 + (frac1 + frac2))
	prHi := float64(-h * h)
	prLo := math.FMA(-h, h, -prHi)
	smHi := csum + prHi
	smLo := prHi - (smHi - csum)
	csum = smHi
	frac1 += prLo
	frac2 += smLo
	x := csum - 1.0 + (frac1 + frac2)
	h += x / (2.0 * h)
	return h / scale
}

// loghelper is mathmodule's loghelper: ints of any size are handled exactly
// enough not to overflow.
func loghelper(v Value, fn func(float64) float64) (float64, error) {
	if i, ok := asInt(v); ok {
		if i.Sign() <= 0 {
			return 0, domainErr()
		}
		f, err := bigToFloat(i)
		if err == nil {
			return fn(f), nil
		}
		e := i.BitLen()
		m := new(big.Float).SetInt(i)
		m.SetMantExp(m, -e)
		mf, _ := m.Float64()
		return fn(mf) + float64(float64(e)*fn(2.0)), nil
	}
	x, err := mathFloat(v)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(x) {
		return x, nil
	}
	if x <= 0 {
		return 0, domainErr()
	}
	if math.IsInf(x, 1) {
		return x, nil
	}
	return fn(x), nil
}

func mathLog(c *callCtx, args []Value, kw *dictV) (Value, error) {
	if err := posOnly("log", 1, 2, args, kw); err != nil {
		return nil, err
	}
	num, err := loghelper(args[0], preciseLog)
	if err != nil {
		return nil, err
	}
	if len(args) == 1 {
		return floatV(num), nil
	}
	den, err := loghelper(args[1], preciseLog)
	if err != nil {
		return nil, err
	}
	return pyTrueDiv(floatV(num), floatV(den))
}

func mathLogN(name string, fn func(float64) float64) func(c *callCtx, args []Value, kw *dictV) (Value, error) {
	return func(c *callCtx, args []Value, kw *dictV) (Value, error) {
		if err := methodsArgsOne("math."+name, args, kw); err != nil {
			return nil, err
		}
		r, err := loghelper(args[0], fn)
		return floatV(r), err
	}
}

// mathPow is math.pow.
func mathPow(c *callCtx, args []Value, kw *dictV) (Value, error) {
	if err := posOnly("pow", 2, 2, args, kw); err != nil {
		return nil, err
	}
	x, err := mathFloat(args[0])
	if err != nil {
		return nil, err
	}
	y, err := mathFloat(args[1])
	if err != nil {
		return nil, err
	}
	finite := func(f float64) bool { return !math.IsInf(f, 0) && !math.IsNaN(f) }
	var r float64
	switch {
	case !finite(x) || !finite(y):
		switch {
		case math.IsNaN(x):
			if y == 0 {
				r = 1
			} else {
				r = x
			}
		case math.IsNaN(y):
			if x == 1 {
				r = 1
			} else {
				r = y
			}
		case math.IsInf(x, 0):
			oddY := finite(y) && math.Mod(math.Abs(y), 2.0) == 1.0
			if y > 0 {
				if oddY {
					r = x
				} else {
					r = math.Abs(x)
				}
			} else if y == 0 {
				r = 1
			} else if oddY {
				r = math.Copysign(0, x)
			} else {
				r = 0
			}
		default: // y infinite
			if math.Abs(x) == 1 {
				r = 1
			} else if (y > 0) == (math.Abs(x) > 1) {
				r = y
			} else {
				r = 0
			}
		}
	default:
		if x == 0 && y < 0 {
			return nil, domainErr()
		}
		r = accuratePowSigned(x, y)
		if math.IsNaN(r) {
			return nil, domainErr()
		}
		if math.IsInf(r, 0) {
			return nil, rangeErr()
		}
	}
	return floatV(r), nil
}

// accuratePowSigned is C pow(x, y) for finite x and y.
func accuratePowSigned(x, y float64) float64 {
	if y == 0 {
		return 1
	}
	if x < 0 {
		if y != math.Trunc(y) {
			return math.NaN()
		}
		r := accuratePow(-x, y)
		if math.Mod(math.Abs(y), 2) == 1 {
			return -r
		}
		return r
	}
	if x == 0 {
		if math.Mod(math.Abs(y), 2) == 1 {
			return x
		}
		return 0
	}
	if x == 1 {
		return 1
	}
	return accuratePow(x, y)
}
