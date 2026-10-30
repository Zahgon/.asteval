// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT
//
// Go port of asteval. exp, log and pow rounded correctly: the C library
// CPython calls rounds them correctly in practice, and Go's math package
// does not always, so these are computed in extended precision and rounded
// once.

package asteval

import (
	"math"
	"math/big"
)

const precBits = 160

var (
	bigLn2  = lnSeries(new(big.Float).SetPrec(precBits).SetInt64(2))
	bigLn10 = bigLnOf(new(big.Float).SetPrec(precBits).SetInt64(10))
)

func bf(x float64) *big.Float { return new(big.Float).SetPrec(precBits).SetFloat64(x) }

// lnSeries is ln(x) for x in [0.5, 2] by the atanh series:
// ln(x) = 2 * sum z^(2k+1)/(2k+1) with z = (x-1)/(x+1).
func lnSeries(x *big.Float) *big.Float {
	one := new(big.Float).SetPrec(precBits).SetInt64(1)
	num := new(big.Float).SetPrec(precBits).Sub(x, one)
	den := new(big.Float).SetPrec(precBits).Add(x, one)
	z := new(big.Float).SetPrec(precBits).Quo(num, den)
	z2 := new(big.Float).SetPrec(precBits).Mul(z, z)
	sum := new(big.Float).SetPrec(precBits)
	term := new(big.Float).SetPrec(precBits).Set(z)
	for k := 0; k < 400; k++ {
		t := new(big.Float).SetPrec(precBits).Quo(term, new(big.Float).SetPrec(precBits).SetInt64(int64(2*k+1)))
		if t.Sign() == 0 {
			break
		}
		sum.Add(sum, t)
		if e := t.MantExp(nil) - sum.MantExp(nil); e < -precBits-4 {
			break
		}
		term.Mul(term, z2)
	}
	return sum.Mul(sum, new(big.Float).SetPrec(precBits).SetInt64(2))
}

// bigLnOf is ln(x) for x > 0.
func bigLnOf(x *big.Float) *big.Float {
	m := new(big.Float).SetPrec(precBits)
	e := x.MantExp(m) // x = m * 2^e, m in [0.5, 1)
	r := lnSeries(m)
	if e != 0 {
		r.Add(r, new(big.Float).SetPrec(precBits).Mul(bigLn2, new(big.Float).SetPrec(precBits).SetInt64(int64(e))))
	}
	return r
}

// bigExpOf is e**t, rounded to a float64 (an overflow gives +Inf).
func bigExpOf(t *big.Float) float64 {
	if t.Cmp(bf(710)) > 0 {
		return math.Inf(1)
	}
	if t.Cmp(bf(-746)) < 0 {
		return 0
	}
	kf := new(big.Float).SetPrec(precBits).Quo(t, bigLn2)
	k, _ := kf.Int64()
	r := new(big.Float).SetPrec(precBits).Sub(t, new(big.Float).SetPrec(precBits).Mul(bigLn2, new(big.Float).SetPrec(precBits).SetInt64(k)))
	// exp(r) with |r| < 1 by Taylor series on r/2^16, then squared 16 times.
	const halvings = 16
	r.SetMantExp(r, -halvings)
	sum := new(big.Float).SetPrec(precBits).SetInt64(1)
	term := new(big.Float).SetPrec(precBits).SetInt64(1)
	for n := 1; n < 60; n++ {
		term.Mul(term, r)
		term.Quo(term, new(big.Float).SetPrec(precBits).SetInt64(int64(n)))
		if term.Sign() == 0 {
			break
		}
		sum.Add(sum, term)
	}
	for i := 0; i < halvings; i++ {
		sum.Mul(sum, sum)
	}
	sum.SetMantExp(sum, int(k))
	f, _ := sum.Float64()
	return f
}

// preciseExp is exp(x) correctly rounded.
func preciseExp(x float64) float64 {
	switch {
	case math.IsNaN(x):
		return x
	case math.IsInf(x, 1):
		return x
	case math.IsInf(x, -1):
		return 0
	case x == 0:
		return 1
	}
	return bigExpOf(bf(x))
}

// preciseLog is log(x) correctly rounded, for x > 0 finite.
func preciseLog(x float64) float64 {
	if x == 1 {
		return 0
	}
	f, _ := bigLnOf(bf(x)).Float64()
	return f
}

// preciseLog10 is log10(x) correctly rounded, exact for powers of ten.
func preciseLog10(x float64) float64 {
	if p := math.Log10(x); p == math.Trunc(p) && math.Pow(10, p) == x {
		return p
	}
	l := bigLnOf(bf(x))
	f, _ := l.Quo(l, bigLn10).Float64()
	return f
}

// precisePow is pow(x, y) for x > 0 and finite y, correctly rounded.
func precisePow(x, y float64) float64 {
	if x == 1 || y == 0 {
		return 1
	}
	t := bigLnOf(bf(x))
	t.Mul(t, bf(y))
	return bigExpOf(t)
}

// ---------------------------------------------------------------- circular and hyperbolic

const piBits = 1400

// bigPi is pi to piBits bits (Machin's formula), enough to reduce any double.
var bigPi = func() *big.Float {
	at := func(inv int64) *big.Float {
		// atan(1/inv) = sum (-1)^k / ((2k+1) inv^(2k+1))
		x := new(big.Float).SetPrec(piBits).Quo(new(big.Float).SetPrec(piBits).SetInt64(1), new(big.Float).SetPrec(piBits).SetInt64(inv))
		x2 := new(big.Float).SetPrec(piBits).Mul(x, x)
		sum := new(big.Float).SetPrec(piBits)
		term := new(big.Float).SetPrec(piBits).Set(x)
		for k := 0; k < 2000; k++ {
			t := new(big.Float).SetPrec(piBits).Quo(term, new(big.Float).SetPrec(piBits).SetInt64(int64(2*k+1)))
			if t.Sign() == 0 || t.MantExp(nil) < -piBits-8 {
				break
			}
			if k%2 == 0 {
				sum.Add(sum, t)
			} else {
				sum.Sub(sum, t)
			}
			term.Mul(term, x2)
		}
		return sum
	}
	p := new(big.Float).SetPrec(piBits).Mul(at(5), new(big.Float).SetPrec(piBits).SetInt64(16))
	return p.Sub(p, new(big.Float).SetPrec(piBits).Mul(at(239), new(big.Float).SetPrec(piBits).SetInt64(4)))
}()

func bp(x *big.Float) *big.Float { return new(big.Float).SetPrec(precBits).Set(x) }

func toF(x *big.Float) float64 {
	f, _ := x.Float64()
	return f
}

// taylor sums x^start/start! terms stepping by 2 with alternating signs
// (sin: start 1, cos: start 0) or same signs (sinh, cosh).
func taylor(x *big.Float, start int, alternate bool) *big.Float {
	x2 := new(big.Float).SetPrec(precBits).Mul(x, x)
	term := new(big.Float).SetPrec(precBits).SetInt64(1)
	if start == 1 {
		term.Set(x)
	}
	sum := new(big.Float).SetPrec(precBits).Set(term)
	for n := start + 1; n < 400; n += 2 {
		term.Mul(term, x2)
		term.Quo(term, new(big.Float).SetPrec(precBits).SetInt64(int64(n*(n+1))))
		if alternate {
			term.Neg(term)
		}
		if term.Sign() == 0 || sum.Sign() != 0 && term.MantExp(nil)-sum.MantExp(nil) < -precBits-4 {
			break
		}
		sum.Add(sum, term)
	}
	return sum
}

// reduceHalfPi returns r and k with x = k*pi/2 + r, |r| <= pi/4.
func reduceHalfPi(x float64) (*big.Float, int) {
	bx := new(big.Float).SetPrec(piBits).SetFloat64(x)
	halfPi := new(big.Float).SetPrec(piBits).Quo(bigPi, new(big.Float).SetPrec(piBits).SetInt64(2))
	q := new(big.Float).SetPrec(piBits).Quo(bx, halfPi)
	k, _ := new(big.Float).SetPrec(piBits).Add(q, new(big.Float).SetPrec(piBits).SetFloat64(0.5)).Int(nil)
	if q.Sign() < 0 {
		k, _ = new(big.Float).SetPrec(piBits).Sub(q, new(big.Float).SetPrec(piBits).SetFloat64(0.5)).Int(nil)
	}
	r := new(big.Float).SetPrec(piBits).Sub(bx, new(big.Float).SetPrec(piBits).Mul(halfPi, new(big.Float).SetPrec(piBits).SetInt(k)))
	m := new(big.Int).Mod(k, big.NewInt(4))
	return bp(r), int(m.Int64())
}

func finite(x float64) bool { return !math.IsInf(x, 0) && !math.IsNaN(x) }

func preciseSin(x float64) float64 {
	if !finite(x) || x == 0 {
		return math.Sin(x)
	}
	r, k := reduceHalfPi(x)
	switch k {
	case 0:
		return toF(taylor(r, 1, true))
	case 1:
		return toF(taylor(r, 0, true))
	case 2:
		return -toF(taylor(r, 1, true))
	}
	return -toF(taylor(r, 0, true))
}

func preciseCos(x float64) float64 {
	if !finite(x) {
		return math.Cos(x)
	}
	r, k := reduceHalfPi(x)
	switch k {
	case 0:
		return toF(taylor(r, 0, true))
	case 1:
		return -toF(taylor(r, 1, true))
	case 2:
		return -toF(taylor(r, 0, true))
	}
	return toF(taylor(r, 1, true))
}

func preciseTan(x float64) float64 {
	if !finite(x) || x == 0 {
		return math.Tan(x)
	}
	r, k := reduceHalfPi(x)
	s, c := taylor(r, 1, true), taylor(r, 0, true)
	if k%2 == 1 {
		return toF(new(big.Float).SetPrec(precBits).Quo(c, s.Neg(s)))
	}
	return toF(new(big.Float).SetPrec(precBits).Quo(s, c))
}

// bigAtan is atan(x) for a big x.
func bigAtan(x *big.Float) *big.Float {
	one := new(big.Float).SetPrec(precBits).SetInt64(1)
	neg := x.Sign() < 0
	a := new(big.Float).SetPrec(precBits).Abs(x)
	invert := a.Cmp(one) > 0
	if invert {
		a.Quo(one, a)
	}
	// halve the argument: atan(a) = 2 atan(a / (1 + sqrt(1 + a^2)))
	doublings := 0
	for a.Cmp(new(big.Float).SetPrec(precBits).SetFloat64(0.01)) > 0 {
		s := new(big.Float).SetPrec(precBits).Mul(a, a)
		s.Add(s, one).Sqrt(s).Add(s, one)
		a.Quo(a, s)
		doublings++
	}
	// atan(a) = sum (-1)^k a^(2k+1)/(2k+1)
	a2 := new(big.Float).SetPrec(precBits).Mul(a, a)
	term := new(big.Float).SetPrec(precBits).Set(a)
	sum := new(big.Float).SetPrec(precBits)
	for k := 0; k < 400; k++ {
		t := new(big.Float).SetPrec(precBits).Quo(term, new(big.Float).SetPrec(precBits).SetInt64(int64(2*k+1)))
		if t.Sign() == 0 || sum.Sign() != 0 && t.MantExp(nil)-sum.MantExp(nil) < -precBits-4 {
			break
		}
		if k%2 == 0 {
			sum.Add(sum, t)
		} else {
			sum.Sub(sum, t)
		}
		term.Mul(term, a2)
	}
	sum.SetMantExp(sum, doublings)
	if invert {
		halfPi := new(big.Float).SetPrec(precBits).Quo(bp(bigPi), new(big.Float).SetPrec(precBits).SetInt64(2))
		sum = halfPi.Sub(halfPi, sum)
	}
	if neg {
		sum.Neg(sum)
	}
	return sum
}

func preciseAtan(x float64) float64 {
	if !finite(x) || x == 0 {
		return math.Atan(x)
	}
	return toF(bigAtan(bf(x)))
}

func preciseAtan2(y, x float64) float64 {
	if !finite(x) || !finite(y) || x == 0 || y == 0 {
		return math.Atan2(y, x)
	}
	q := new(big.Float).SetPrec(precBits).Quo(bf(y), bf(x))
	a := bigAtan(q)
	if x < 0 {
		if y >= 0 {
			a.Add(a, bp(bigPi))
		} else {
			a.Sub(a, bp(bigPi))
		}
	}
	return toF(a)
}

// bigSqrt1mx2 is sqrt(1 - x^2) for a double x with |x| < 1.
func bigSqrt1mx2(x float64) *big.Float {
	b := bf(x)
	b.Mul(b, b)
	one := new(big.Float).SetPrec(precBits).SetInt64(1)
	return one.Sub(one, b).Sqrt(one)
}

func preciseAsin(x float64) float64 {
	if !finite(x) || x == 0 || math.Abs(x) >= 1 {
		return math.Asin(x)
	}
	q := new(big.Float).SetPrec(precBits).Quo(bf(x), bigSqrt1mx2(x))
	return toF(bigAtan(q))
}

func preciseAcos(x float64) float64 {
	if !finite(x) || math.Abs(x) >= 1 {
		return math.Acos(x)
	}
	halfPi := new(big.Float).SetPrec(precBits).Quo(bp(bigPi), new(big.Float).SetPrec(precBits).SetInt64(2))
	if x == 0 {
		return toF(halfPi)
	}
	a := bigAtan(new(big.Float).SetPrec(precBits).Quo(bf(x), bigSqrt1mx2(x)))
	return toF(halfPi.Sub(halfPi, a))
}

// bigExpFull is e**t as a big float (no overflow).
func bigExpFull(t *big.Float) *big.Float {
	kf := new(big.Float).SetPrec(precBits).Quo(t, bigLn2)
	k, _ := kf.Int64()
	r := new(big.Float).SetPrec(precBits).Sub(t, new(big.Float).SetPrec(precBits).Mul(bigLn2, new(big.Float).SetPrec(precBits).SetInt64(k)))
	sum := taylorExp(r)
	return sum.SetMantExp(sum, int(k))
}

func taylorExp(r *big.Float) *big.Float {
	const halvings = 16
	x := new(big.Float).SetPrec(precBits).SetMantExp(r, -halvings)
	sum := new(big.Float).SetPrec(precBits).SetInt64(1)
	term := new(big.Float).SetPrec(precBits).SetInt64(1)
	for n := 1; n < 60; n++ {
		term.Mul(term, x)
		term.Quo(term, new(big.Float).SetPrec(precBits).SetInt64(int64(n)))
		if term.Sign() == 0 {
			break
		}
		sum.Add(sum, term)
	}
	for i := 0; i < halvings; i++ {
		sum.Mul(sum, sum)
	}
	return sum
}

func preciseSinh(x float64) float64 {
	if !finite(x) || x == 0 || math.Abs(x) > 710.5 {
		return math.Sinh(x)
	}
	if math.Abs(x) < 1 {
		return toF(taylor(bf(x), 1, false))
	}
	e := bigExpFull(bf(x))
	inv := new(big.Float).SetPrec(precBits).Quo(new(big.Float).SetPrec(precBits).SetInt64(1), e)
	e.Sub(e, inv)
	return toF(e.SetMantExp(e, -1))
}

func preciseCosh(x float64) float64 {
	if !finite(x) || math.Abs(x) > 710.5 {
		return math.Cosh(x)
	}
	if math.Abs(x) < 1 {
		return toF(taylor(bf(x), 0, false))
	}
	e := bigExpFull(bf(x))
	inv := new(big.Float).SetPrec(precBits).Quo(new(big.Float).SetPrec(precBits).SetInt64(1), e)
	e.Add(e, inv)
	return toF(e.SetMantExp(e, -1))
}

func preciseTanh(x float64) float64 {
	if !finite(x) || x == 0 || math.Abs(x) > 40 {
		return math.Tanh(x)
	}
	if math.Abs(x) < 1 {
		s, c := taylor(bf(x), 1, false), taylor(bf(x), 0, false)
		return toF(s.Quo(s, c))
	}
	e2 := bigExpFull(bf(2 * x))
	one := new(big.Float).SetPrec(precBits).SetInt64(1)
	num := new(big.Float).SetPrec(precBits).Sub(e2, one)
	den := new(big.Float).SetPrec(precBits).Add(e2, one)
	return toF(num.Quo(num, den))
}

// bigLog1p is ln(1 + x) for a big x > -1, accurate for small x.
func bigLog1p(x *big.Float) *big.Float {
	if x.MantExp(nil) < -40 {
		// x - x^2/2 + x^3/3 - ...
		sum := new(big.Float).SetPrec(precBits)
		term := new(big.Float).SetPrec(precBits).Set(x)
		for n := 1; n < 20; n++ {
			t := new(big.Float).SetPrec(precBits).Quo(term, new(big.Float).SetPrec(precBits).SetInt64(int64(n)))
			if n%2 == 1 {
				sum.Add(sum, t)
			} else {
				sum.Sub(sum, t)
			}
			term.Mul(term, x)
		}
		return sum
	}
	one := new(big.Float).SetPrec(precBits + 200).SetInt64(1)
	return bp(bigLnOf(one.Add(one, x)))
}

func preciseLog1p(x float64) float64 {
	if !finite(x) || x == 0 || x <= -1 {
		return math.Log1p(x)
	}
	return toF(bigLog1p(bf(x)))
}

func preciseAsinh(x float64) float64 {
	if !finite(x) || x == 0 {
		return math.Asinh(x)
	}
	a := math.Abs(x)
	b := bf(a)
	var r *big.Float
	if a < 1 {
		// asinh(a) = log1p(a + a^2 / (1 + sqrt(1 + a^2)))
		s := new(big.Float).SetPrec(precBits).Mul(b, b)
		t := new(big.Float).SetPrec(precBits).Add(s, new(big.Float).SetPrec(precBits).SetInt64(1))
		t.Sqrt(t).Add(t, new(big.Float).SetPrec(precBits).SetInt64(1))
		s.Quo(s, t).Add(s, b)
		r = bigLog1p(s)
	} else {
		s := new(big.Float).SetPrec(precBits).Mul(b, b)
		s.Add(s, new(big.Float).SetPrec(precBits).SetInt64(1)).Sqrt(s).Add(s, b)
		r = bigLnOf(s)
	}
	f := toF(r)
	if x < 0 {
		return -f
	}
	return f
}

func preciseAcosh(x float64) float64 {
	if !finite(x) || x < 1 {
		return math.Acosh(x)
	}
	if x == 1 {
		return 0
	}
	b := bf(x)
	s := new(big.Float).SetPrec(precBits).Mul(b, b)
	s.Sub(s, new(big.Float).SetPrec(precBits).SetInt64(1)).Sqrt(s).Add(s, b)
	return toF(bigLnOf(s))
}

func preciseAtanh(x float64) float64 {
	if !finite(x) || x == 0 || math.Abs(x) >= 1 {
		return math.Atanh(x)
	}
	// atanh(x) = log1p(2x / (1 - x)) / 2
	b := bf(x)
	num := new(big.Float).SetPrec(precBits).Mul(b, new(big.Float).SetPrec(precBits).SetInt64(2))
	den := new(big.Float).SetPrec(precBits).Sub(new(big.Float).SetPrec(precBits).SetInt64(1), b)
	r := bigLog1p(num.Quo(num, den))
	return toF(r.SetMantExp(r, -1))
}
