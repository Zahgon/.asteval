// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT
//
// Go port of asteval. Hashing: Python's hash() for the values that have one,
// and the canonical keys dicts use to find equal keys.

package asteval

import (
	"hash/fnv"
	"math"
	"math/big"
	"math/bits"
	"strconv"
	"strings"
)

// hashModulus is CPython's _PyHASH_MODULUS, 2**61 - 1.
var hashModulus = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 61), big.NewInt(1))

const (
	hashBits = 61
	hashInf  = 314159
	hashImag = 1000003
)

// pyHash is Python's hash(): the documented numeric hash for numbers, the
// tuple hash of CPython 3.12 for tuples, and an arbitrary but stable hash for
// str and bytes (CPython's depends on PYTHONHASHSEED). It fails with
// TypeError for an unhashable value.
func pyHash(v Value) (int64, error) {
	switch x := v.(type) {
	case noneV:
		return 0xFCA86420 >> 4, nil
	case boolV:
		if x {
			return 1, nil
		}
		return 0, nil
	case intV:
		return intHash(x.v), nil
	case floatV:
		return floatHash(float64(x), v), nil
	case complexV:
		h := uint64(floatHash(real(x), v)) + hashImag*uint64(floatHash(imag(x), v))
		if int64(h) == -1 {
			return -2, nil
		}
		return int64(h), nil
	case strV:
		return bytesHash("s" + string(x)), nil
	case bytesV:
		return bytesHash("b" + string(x)), nil
	case *tupleV:
		return tupleHash(x.items)
	case *setV:
		if !x.frozen {
			return 0, unhashable(v)
		}
		return frozensetHash(x), nil
	case *listV, *dictV, *bytearrayV, *groupV:
		return 0, unhashable(v)
	case *sliceV:
		return tupleHash([]Value{x.start, x.stop, x.step})
	case rangeV:
		return tupleHash([]Value{mkInt(x.length()), mkInt(x.start), mkInt(x.step)})
	}
	return int64(objID(v) >> 4), nil
}

func unhashable(v Value) error {
	return newPyErr("TypeError", "unhashable type: '"+v.pyType()+"'")
}

func bytesHash(s string) int64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	r := int64(h.Sum64())
	if r == -1 {
		r = -2
	}
	return r
}

// intHash is the numeric hash of an int: n mod 2**61-1 with n's sign.
func intHash(n *big.Int) int64 {
	var r int64
	if n.IsInt64() {
		a := n.Int64()
		neg := a < 0
		u := uint64(a)
		if neg {
			u = uint64(-a)
		}
		r = int64(u % (1<<61 - 1))
		if neg {
			r = -r
		}
	} else {
		m := new(big.Int).Rem(new(big.Int).Abs(n), hashModulus)
		r = m.Int64()
		if n.Sign() < 0 {
			r = -r
		}
	}
	if r == -1 {
		r = -2
	}
	return r
}

// floatHash is the numeric hash of a float, as documented for Python's
// numeric types. A NaN hashes by identity.
func floatHash(f float64, obj Value) int64 {
	if math.IsInf(f, 0) {
		if f > 0 {
			return hashInf
		}
		return -hashInf
	}
	if math.IsNaN(f) {
		return int64(objID(obj) >> 4)
	}
	m, e := math.Frexp(f)
	sign := int64(1)
	if m < 0 {
		sign = -1
		m = -m
	}
	var x uint64
	for m != 0 {
		x = ((x << 28) & (1<<61 - 1)) | x>>(hashBits-28)
		m *= 268435456.0
		e -= 28
		y := uint64(m)
		m -= float64(y)
		x += y
		if x >= 1<<61-1 {
			x -= 1<<61 - 1
		}
	}
	if e >= 0 {
		e = e % hashBits
	} else {
		e = hashBits - 1 - ((-1 - e) % hashBits)
	}
	x = ((x << uint(e)) & (1<<61 - 1)) | x>>uint(hashBits-e)
	r := int64(x) * sign
	if r == -1 {
		r = -2
	}
	return r
}

// tupleHash is CPython 3.12's tuple hash (an xxHash-based mix).
func tupleHash(items []Value) (int64, error) {
	const (
		prime1 uint64 = 11400714785074694791
		prime2 uint64 = 14029467366897019727
		prime5 uint64 = 2870177450012600261
	)
	acc := prime5
	for _, it := range items {
		lane, err := pyHash(it)
		if err != nil {
			return 0, err
		}
		acc += uint64(lane) * prime2
		acc = bits.RotateLeft64(acc, 31)
		acc *= prime1
	}
	acc += uint64(len(items)) ^ (prime5 ^ 3527539)
	if int64(acc) == -1 {
		return 1546275796, nil
	}
	return int64(acc), nil
}

// frozensetHash is CPython 3.12's frozenset hash.
func frozensetHash(s *setV) int64 {
	var hash uint64
	shuffle := func(h uint64) uint64 { return ((h ^ 89869747) ^ (h << 16)) * 3644798167 }
	for _, e := range s.table {
		if e.live() {
			hash ^= shuffle(uint64(e.hash))
		}
	}
	if s.used&1 != 0 {
		hash ^= shuffle(^uint64(0))
	}
	hash ^= (uint64(s.used) + 1) * 1927868237
	hash ^= (hash >> 11) ^ (hash >> 25)
	hash = hash*69069 + 907133923
	if int64(hash) == -1 {
		hash = 590923713
	}
	return int64(hash)
}

// hashKey returns a comparable key, equal for values Python considers equal
// and hashable, or Python's TypeError for an unhashable value. Dicts index
// their keys by it.
func hashKey(v Value) (any, error) {
	switch x := v.(type) {
	case noneV:
		return x, nil
	case boolV:
		if x {
			return "i1", nil
		}
		return "i0", nil
	case intV:
		return "i" + x.v.String(), nil
	case floatV:
		return floatKey(float64(x)), nil
	case complexV:
		if imag(x) == 0 {
			return floatKey(real(x)), nil
		}
		return "c" + strconv.FormatFloat(real(x), 'g', -1, 64) + "," + strconv.FormatFloat(imag(x), 'g', -1, 64), nil
	case strV:
		return "s" + string(x), nil
	case bytesV:
		return "b" + string(x), nil
	case *tupleV:
		parts := make([]string, len(x.items))
		for i, it := range x.items {
			k, err := hashKey(it)
			if err != nil {
				return nil, err
			}
			parts[i] = keyString(k)
		}
		return "t(" + strings.Join(parts, "\x00") + ")", nil
	case *setV:
		if !x.frozen {
			return nil, unhashable(v)
		}
		var parts []string
		for _, e := range x.table {
			if e.live() {
				k, _ := hashKey(e.key)
				parts = append(parts, keyString(k))
			}
		}
		sortStrings(parts)
		return "f(" + strings.Join(parts, "\x00") + ")", nil
	case *listV, *dictV, *bytearrayV, *groupV:
		return nil, unhashable(v)
	case *sliceV:
		k, err := hashKey(newTuple([]Value{x.start, x.stop, x.step}))
		if err != nil {
			return nil, err
		}
		return "l" + keyString(k), nil
	case rangeV:
		if x.length() == 0 {
			return "r0", nil
		}
		return "r" + strconv.FormatInt(x.length(), 10) + ":" + strconv.FormatInt(x.start, 10) + ":" + strconv.FormatInt(x.step, 10), nil
	}
	return v, nil
}

func floatKey(f float64) any {
	if !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f) {
		bi, _ := new(big.Float).SetFloat64(f).Int(nil)
		return "i" + bi.String()
	}
	if math.IsNaN(f) {
		return &f // NaN is only ever equal to itself, by identity
	}
	return floatV(f)
}

func keyString(k any) string {
	switch x := k.(type) {
	case string:
		return x
	case floatV:
		return "f" + strconv.FormatFloat(float64(x), 'g', -1, 64)
	case noneV:
		return "n"
	}
	return "o" + strconv.FormatUint(uint64(objID(k)), 16)
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
