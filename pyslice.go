// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT
//
// CPython 3.12's sequence slicing and subscripting: how str, list and tuple
// turn a slice or an integer index into positions.

package asteval

import (
	"math"
	"math/big"
	"unicode/utf8"
)

// pySliceIndex converts one slice bound as _PyEval_SliceIndex does: an int
// or bool, with ints outside the Go int range clamped to it. none reports
// Python's None.
func pySliceIndex(v Value) (i int, none bool, err error) {
	if _, ok := v.(noneV); ok {
		return 0, true, nil
	}
	n, ok := asInt(v)
	if !ok {
		return 0, false, typeErr("slice indices must be integers or None or have an __index__ method")
	}
	if i, ok := pyBigToInt(n); ok {
		return i, false, nil
	}
	switch {
	case n.Sign() < 0:
		return math.MinInt, false, nil
	default:
		return math.MaxInt, false, nil
	}
}

// pySliceIndices resolves start:stop:step against a sequence of the given
// length as subscripting does (PySlice_Unpack then PySlice_AdjustIndices),
// which is slice(start, stop, step).indices(length) except that a step
// beyond the Go int range is clamped. Each bound is None, an int or a bool;
// anything else is TypeError "slice indices must be integers or None or
// have an __index__ method". The step is checked first, then start, then
// stop; a zero step is ValueError "slice step cannot be zero".
func pySliceIndices(length int, start, stop, step Value) (int, int, int, error) {
	st, none, err := pySliceIndex(step)
	if err != nil {
		return 0, 0, 0, err
	}
	if none {
		st = 1
	} else if st == 0 {
		return 0, 0, 0, valueErr("slice step cannot be zero")
	} else if st < -math.MaxInt {
		st = -math.MaxInt
	}
	lo, none, err := pySliceIndex(start)
	if err != nil {
		return 0, 0, 0, err
	}
	if none {
		lo = 0
		if st < 0 {
			lo = math.MaxInt
		}
	}
	hi, none, err := pySliceIndex(stop)
	if err != nil {
		return 0, 0, 0, err
	}
	if none {
		hi = math.MaxInt
		if st < 0 {
			hi = math.MinInt
		}
	}
	return pyAdjustSliceBound(lo, length, st), pyAdjustSliceBound(hi, length, st), st, nil
}

// pyAdjustSliceBound clamps one bound as PySlice_AdjustIndices does.
func pyAdjustSliceBound(i, length, step int) int {
	if i < 0 {
		if i += length; i < 0 {
			if step < 0 {
				return -1
			}
			return 0
		}
	} else if i >= length {
		if step < 0 {
			return length - 1
		}
		return length
	}
	return i
}

// pySliceLen is the number of items start:stop:step selects, for bounds
// pySliceIndices returned.
func pySliceLen(start, stop, step int) int {
	if step < 0 {
		if stop < start {
			return (start-stop-1)/(-step) + 1
		}
	} else if start < stop {
		return (stop-start-1)/step + 1
	}
	return 0
}

// pySliceStr is s[start:stop:step] in code points; see pySliceIndices for
// the bounds and errors.
func pySliceStr(s string, start, stop, step Value) (string, error) {
	ascii := pyIsASCII(s)
	n := len(s)
	if !ascii {
		n = utf8.RuneCountInString(s)
	}
	lo, hi, st, err := pySliceIndices(n, start, stop, step)
	if err != nil {
		return "", err
	}
	count := pySliceLen(lo, hi, st)
	switch {
	case count == 0:
		return "", nil
	case st == 1 && ascii:
		return s[lo:hi], nil
	case st == 1:
		w, _ := pyWindow(s, lo, hi)
		return w, nil
	}
	rs := []rune(s)
	out := make([]rune, count)
	for k := range out {
		out[k] = rs[lo+k*st]
	}
	return string(out), nil
}

// pySliceSeq is items[start:stop:step] as a new slice; see pySliceIndices
// for the bounds and errors.
func pySliceSeq(items []Value, start, stop, step Value) ([]Value, error) {
	lo, hi, st, err := pySliceIndices(len(items), start, stop, step)
	if err != nil {
		return nil, err
	}
	out := make([]Value, pySliceLen(lo, hi, st))
	for k := range out {
		out[k] = items[lo+k*st]
	}
	return out, nil
}

// errNotIndex is what pyIndexNorm returns for an index that is not an int:
// the caller raises its own TypeError, such as pyIndexTypeErr.
var errNotIndex = typeErr("indices must be integers")

// pyIndexNorm resolves an integer subscript seq[idx] for a sequence of the
// given length, as str, list and tuple do:
//
//   - idx an int or bool within [-length, length): (idx, or idx+length when
//     negative), true, nil;
//   - idx an int or bool outside that range: 0, false, nil, and the caller
//     raises IndexError, such as pyIndexRangeErr;
//   - idx an int beyond the Go int range: IndexError "cannot fit 'int' into
//     an index-sized integer" (Python does not clamp subscripts);
//   - anything else (None, float, str, ...): errNotIndex, and the caller
//     raises its TypeError (or, for a slice, uses pySliceIndices).
func pyIndexNorm(length int, idx Value) (int, bool, error) {
	n, ok := asInt(idx)
	if !ok {
		return 0, false, errNotIndex
	}
	i, ok := pyBigToInt(n)
	if !ok {
		return 0, false, newPyErr("IndexError", "cannot fit 'int' into an index-sized integer")
	}
	if i < 0 {
		i += length
	}
	if i < 0 || i >= length {
		return 0, false, nil
	}
	return i, true, nil
}

// pyBigToInt returns n as an int if it fits one (Py_ssize_t on 64-bit).
func pyBigToInt(n *big.Int) (int, bool) {
	if !n.IsInt64() || n.Int64() < math.MinInt || n.Int64() > math.MaxInt {
		return 0, false
	}
	return int(n.Int64()), true
}

// pyIndexTypeErr is the TypeError str, list and tuple raise for a subscript
// that is neither an int nor a slice; container is "str", "list" or "tuple".
func pyIndexTypeErr(container string, idx Value) *excV {
	if container == "str" {
		return typeErr("string indices must be integers, not '%s'", idx.pyType())
	}
	return typeErr("%s indices must be integers or slices, not %s", container, idx.pyType())
}

// pyIndexRangeErr is the IndexError str, list and tuple raise for an index
// out of range; container is "str", "list" or "tuple".
func pyIndexRangeErr(container string) *excV {
	if container == "str" {
		container = "string"
	}
	return newPyErr("IndexError", container+" index out of range")
}
