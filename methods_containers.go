// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT
//
// The methods and attributes of list, tuple, dict, int, bool, float and
// range, as CPython 3.12 defines them.

package asteval

import (
	"math"
	"math/big"
)

// methodsSeqFind returns the index of the first item in items[start:stop] equal to
// v (the item on the left of ==, identity first), or -1. start and stop are
// clamped as list.index and tuple.index clamp them.
func methodsSeqFind(items []Value, v Value, start, stop int) (int, error) {
	n := len(items)
	if start < 0 {
		start = max(start+n, 0)
	}
	if stop < 0 {
		stop = max(stop+n, 0)
	}
	for i := start; i < stop && i < len(items); i++ {
		if sameObject(items[i], v) {
			return i, nil
		}
		eq, err := pyEq(items[i], v)
		if err != nil {
			return 0, err
		}
		if eq {
			return i, nil
		}
	}
	return -1, nil
}

// methodsSeqCount counts the items equal to v.
func methodsSeqCount(items []Value, v Value) (Value, error) {
	n := 0
	for _, it := range items {
		if sameObject(it, v) {
			n++
			continue
		}
		eq, err := pyEq(it, v)
		if err != nil {
			return nil, err
		}
		if eq {
			n++
		}
	}
	return mkInt(int64(n)), nil
}

// methodsSeqIndex is list.index and tuple.index after the argument count check:
// value, then start and stop as slice indices that must not be None.
func methodsSeqIndex(items []Value, args []Value) (int, error) {
	start, stop := 0, math.MaxInt
	var err error
	if len(args) > 1 {
		if start, err = methodsSliceIndexNotNone(args[1]); err != nil {
			return 0, err
		}
	}
	if len(args) > 2 {
		if stop, err = methodsSliceIndexNotNone(args[2]); err != nil {
			return 0, err
		}
	}
	return methodsSeqFind(items, args[0], start, stop)
}

// ---------------------------------------------------------------- list

type methodsListFn func(l *listV, args []Value, kw *dictV) (Value, error)

var methodsList map[string]methodsListFn

func init() {
	methodsList = map[string]methodsListFn{
		"append": func(l *listV, args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsOne("list.append", args, kw); err != nil {
				return nil, err
			}
			l.items = append(l.items, args[0])
			return none, nil
		},
		"clear": func(l *listV, args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsNone("list.clear", args, kw); err != nil {
				return nil, err
			}
			if len(l.items) > 0 {
				l.items = nil
			}
			return none, nil
		},
		"copy": func(l *listV, args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsNone("list.copy", args, kw); err != nil {
				return nil, err
			}
			return newList(append([]Value(nil), l.items...)), nil
		},
		"count": func(l *listV, args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsOne("list.count", args, kw); err != nil {
				return nil, err
			}
			return methodsSeqCount(l.items, args[0])
		},
		"extend": func(l *listV, args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsOne("list.extend", args, kw); err != nil {
				return nil, err
			}
			items, err := toSlice(args[0])
			if err != nil {
				return nil, err
			}
			l.items = append(l.items, items...)
			return none, nil
		},
		"index": func(l *listV, args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsPos("list.index", 1, 3, args, kw); err != nil {
				return nil, err
			}
			i, err := methodsSeqIndex(l.items, args)
			if err != nil {
				return nil, err
			}
			if i < 0 {
				r, err := pyRepr(args[0])
				if err != nil {
					return nil, err
				}
				return nil, valueErr("%s is not in list", r)
			}
			return mkInt(int64(i)), nil
		},
		"insert": func(l *listV, args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsPos("list.insert", 2, 2, args, kw); err != nil {
				return nil, err
			}
			i, err := methodsSsize(args[0])
			if err != nil {
				return nil, err
			}
			n := len(l.items)
			if i < 0 {
				i = max(i+n, 0)
			}
			i = min(i, n)
			l.items = append(l.items, nil)
			copy(l.items[i+1:], l.items[i:n])
			l.items[i] = args[1]
			return none, nil
		},
		"pop": func(l *listV, args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsPos("list.pop", 0, 1, args, kw); err != nil {
				return nil, err
			}
			i := -1
			if len(args) > 0 {
				var err error
				if i, err = methodsSsize(args[0]); err != nil {
					return nil, err
				}
			}
			n := len(l.items)
			if n == 0 {
				return nil, newPyErr("IndexError", "pop from empty list")
			}
			if i < 0 {
				i += n
			}
			if i < 0 || i >= n {
				return nil, newPyErr("IndexError", "pop index out of range")
			}
			v := l.items[i]
			l.items = append(l.items[:i], l.items[i+1:]...)
			return v, nil
		},
		"remove": func(l *listV, args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsOne("list.remove", args, kw); err != nil {
				return nil, err
			}
			i, err := methodsSeqFind(l.items, args[0], 0, math.MaxInt)
			if err != nil {
				return nil, err
			}
			if i < 0 {
				return nil, valueErr("list.remove(x): x not in list")
			}
			l.items = append(l.items[:i], l.items[i+1:]...)
			return none, nil
		},
		"reverse": func(l *listV, args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsNone("list.reverse", args, kw); err != nil {
				return nil, err
			}
			for i, j := 0, len(l.items)-1; i < j; i, j = i+1, j-1 {
				l.items[i], l.items[j] = l.items[j], l.items[i]
			}
			return none, nil
		},
		"sort": methodsListSort,
	}
}

// methodsListSort is list.sort(*, key=None, reverse=False). Like CPython it
// empties the list while the keys are computed and the items sorted, raises
// ValueError "list modified during sort" if the list grew meanwhile, leaves
// the items in their original order when a key call fails and in the
// partially sorted order timsort reached when a comparison fails.
func methodsListSort(l *listV, args []Value, kw *dictV) (Value, error) {
	buf, err := methodsArgsKw("sort", []string{"key", "reverse"}, 0, args, kw)
	if err != nil {
		return nil, err
	}
	key := buf[0]
	if _, isNone := key.(noneV); isNone {
		key = nil
	}
	reverse := false
	if buf[1] != nil {
		if reverse, err = truthy(buf[1]); err != nil {
			return nil, err
		}
	}
	saved := l.items
	l.items = []Value{}
	defer func() { l.items = saved }()
	items := make([]sortItem, len(saved))
	for i, v := range saved {
		items[i] = sortItem{k: v, v: v}
		if key != nil {
			k, err := callValue(nil, key, []Value{v}, nil)
			if err != nil {
				return nil, err
			}
			items[i].k = k
		}
	}
	if reverse {
		sortReverse(items)
	}
	err = methodsSort(items)
	if err == nil && (len(l.items) != 0 || cap(l.items) != 0) {
		err = valueErr("list modified during sort")
	}
	if reverse {
		sortReverse(items)
	}
	for i, it := range items {
		saved[i] = it.v
	}
	if err != nil {
		return nil, err
	}
	return none, nil
}

// ---------------------------------------------------------------- tuple

// methodsTupleFn is a tuple method; typ is "tuple" or the named tuple's type.
type methodsTupleFn func(typ string, items []Value, args []Value, kw *dictV) (Value, error)

var methodsTuple = map[string]methodsTupleFn{
	"count": func(typ string, items []Value, args []Value, kw *dictV) (Value, error) {
		if err := methodsArgsOne(typ+".count", args, kw); err != nil {
			return nil, err
		}
		return methodsSeqCount(items, args[0])
	},
	"index": func(typ string, items []Value, args []Value, kw *dictV) (Value, error) {
		if err := methodsArgsPos(typ+".index", 1, 3, args, kw); err != nil {
			return nil, err
		}
		i, err := methodsSeqIndex(items, args)
		if err != nil {
			return nil, err
		}
		if i < 0 {
			return nil, valueErr("tuple.index(x): x not in tuple")
		}
		return mkInt(int64(i)), nil
	},
}

// ---------------------------------------------------------------- dict

type methodsDictFn func(d *dictV, args []Value, kw *dictV) (Value, error)

var methodsDict map[string]methodsDictFn

// methodsKeyError is the KeyError dict raises for a missing key: its str() is the
// key's repr().
func methodsKeyError(key Value) error {
	r, err := pyRepr(key)
	if err != nil {
		return err
	}
	return newPyErr("KeyError", r)
}

func methodsDictView(kind string) methodsDictFn {
	return func(d *dictV, args []Value, kw *dictV) (Value, error) {
		if err := methodsArgsNone("dict."+kind, args, kw); err != nil {
			return nil, err
		}
		return &dictViewV{d: d, kind: kind}, nil
	}
}

func init() {
	methodsDict = map[string]methodsDictFn{
		"clear": func(d *dictV, args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsNone("dict.clear", args, kw); err != nil {
				return nil, err
			}
			d.keys, d.vals, d.index = nil, nil, map[any]int{}
			return none, nil
		},
		"copy": func(d *dictV, args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsNone("dict.copy", args, kw); err != nil {
				return nil, err
			}
			return d.copy(), nil
		},
		"keys":   methodsDictView("keys"),
		"values": methodsDictView("values"),
		"items":  methodsDictView("items"),
		"get": func(d *dictV, args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsPos("dict.get", 1, 2, args, kw); err != nil {
				return nil, err
			}
			v, found, err := d.get(args[0])
			if err != nil {
				return nil, err
			}
			if found {
				return v, nil
			}
			if len(args) > 1 {
				return args[1], nil
			}
			return none, nil
		},
		"pop": func(d *dictV, args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsPos("dict.pop", 1, 2, args, kw); err != nil {
				return nil, err
			}
			// An empty dict answers before hashing the key.
			if d.Len() > 0 {
				v, found, err := d.get(args[0])
				if err != nil {
					return nil, err
				}
				if found {
					_, _ = d.del(args[0])
					return v, nil
				}
			}
			if len(args) > 1 {
				return args[1], nil
			}
			return nil, methodsKeyError(args[0])
		},
		"popitem": func(d *dictV, args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsNone("dict.popitem", args, kw); err != nil {
				return nil, err
			}
			n := d.Len()
			if n == 0 {
				return nil, methodsKeyError(strV("popitem(): dictionary is empty"))
			}
			k, v := d.keys[n-1], d.vals[n-1]
			_, _ = d.del(k)
			return newTuple([]Value{k, v}), nil
		},
		"setdefault": func(d *dictV, args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsPos("dict.setdefault", 1, 2, args, kw); err != nil {
				return nil, err
			}
			v, found, err := d.get(args[0])
			if err != nil || found {
				return v, err
			}
			dflt := none
			if len(args) > 1 {
				dflt = args[1]
			}
			return dflt, d.set(args[0], dflt)
		},
		"update": methodsDictUpdate,
	}
}

// methodsDictUpdate is dict.update([other], **kwargs): other is a dict, or an
// iterable of key/value pairs; items are stored as they are read, so a
// failure leaves the earlier ones in place.
func methodsDictUpdate(d *dictV, args []Value, kw *dictV) (Value, error) {
	if err := methodsCheckPositional("update", len(args), 0, 1); err != nil {
		return nil, err
	}
	if len(args) == 1 {
		if src, ok := args[0].(*dictV); ok {
			if err := methodsDictMerge(d, src); err != nil {
				return nil, err
			}
		} else if err := methodsDictMergePairs(d, args[0]); err != nil {
			return nil, err
		}
	}
	if kw != nil {
		if err := methodsDictMerge(d, kw); err != nil {
			return nil, err
		}
	}
	return none, nil
}

func methodsDictMerge(d, src *dictV) error {
	keys, vals := append([]Value(nil), src.keys...), append([]Value(nil), src.vals...)
	for i, k := range keys {
		if err := d.set(k, vals[i]); err != nil {
			return err
		}
	}
	return nil
}

// methodsDictMergePairs is PyDict_MergeFromSeq2.
func methodsDictMergePairs(d *dictV, seq Value) error {
	it, err := pyIter(seq)
	if err != nil {
		return err
	}
	for i := 0; ; i++ {
		item, ok, err := it.next()
		if err != nil || !ok {
			return err
		}
		pair, err := toSlice(item)
		if err != nil {
			if isExc(err, "TypeError") {
				return typeErr("cannot convert dictionary update sequence element #%d to a sequence", i)
			}
			return err
		}
		if len(pair) != 2 {
			return valueErr("dictionary update sequence element #%d has length %d; 2 is required", i, len(pair))
		}
		if err := d.set(pair[0], pair[1]); err != nil {
			return err
		}
	}
}

// ---------------------------------------------------------------- numbers

// methodsIntAttr is builtinAttr for an int or a bool.
func methodsIntAttr(v Value, name string) (Value, bool) {
	n, _ := asInt(v)
	switch name {
	case "bit_length":
		return methodsBound(v, name, func(args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsNone(v.pyType()+".bit_length", args, kw); err != nil {
				return nil, err
			}
			return mkInt(int64(n.BitLen())), nil
		}), true
	case "is_integer":
		return methodsBound(v, name, func(args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsNone(v.pyType()+".is_integer", args, kw); err != nil {
				return nil, err
			}
			return trueV, nil
		}), true
	case "real", "numerator":
		return mkBig(new(big.Int).Set(n)), true
	case "imag":
		return mkInt(0), true
	case "denominator":
		return mkInt(1), true
	}
	return nil, false
}

// methodsFloatAttr is builtinAttr for a float.
func methodsFloatAttr(f floatV, name string) (Value, bool) {
	switch name {
	case "is_integer":
		return methodsBound(f, name, func(args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsNone("float.is_integer", args, kw); err != nil {
				return nil, err
			}
			x := float64(f)
			return pyBool(!math.IsInf(x, 0) && x == math.Trunc(x)), nil
		}), true
	case "real":
		return f, true
	case "imag":
		return floatV(0), true
	}
	return nil, false
}

// ---------------------------------------------------------------- range

// methodsRangeAttr is builtinAttr for a range.
func methodsRangeAttr(r rangeV, name string) (Value, bool) {
	switch name {
	case "start":
		return mkInt(r.start), true
	case "stop":
		return mkInt(r.stop), true
	case "step":
		return mkInt(r.step), true
	case "count":
		return methodsBound(r, name, func(args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsOne("range.count", args, kw); err != nil {
				return nil, err
			}
			if n, ok := asInt(args[0]); ok {
				if _, found := methodsRangeFind(r, n); found {
					return mkInt(1), nil
				}
				return mkInt(0), nil
			}
			count := int64(0)
			for i := int64(0); i < r.length(); i++ {
				eq, err := pyEq(mkInt(r.at(i)), args[0])
				if err != nil {
					return nil, err
				}
				if eq {
					count++
				}
			}
			return mkInt(count), nil
		}), true
	case "index":
		return methodsBound(r, name, func(args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsOne("range.index", args, kw); err != nil {
				return nil, err
			}
			if n, ok := asInt(args[0]); ok {
				if i, found := methodsRangeFind(r, n); found {
					return mkInt(i), nil
				}
				s, err := pyRepr(args[0])
				if err != nil {
					return nil, err
				}
				return nil, valueErr("%s is not in range", s)
			}
			for i := int64(0); i < r.length(); i++ {
				eq, err := pyEq(mkInt(r.at(i)), args[0])
				if err != nil {
					return nil, err
				}
				if eq {
					return mkInt(i), nil
				}
			}
			return nil, valueErr("sequence.index(x): x not in sequence")
		}), true
	}
	return nil, false
}

// methodsRangeFind returns the position of the integer n in r.
func methodsRangeFind(r rangeV, n *big.Int) (int64, bool) {
	if !n.IsInt64() {
		return 0, false
	}
	x := n.Int64()
	if r.step > 0 && (x < r.start || x >= r.stop) || r.step < 0 && (x > r.start || x <= r.stop) {
		return 0, false
	}
	// x-start can overflow int64 for ranges spanning more than half of it.
	d := new(big.Int).Sub(n, big.NewInt(r.start))
	q, m := new(big.Int).QuoRem(d, big.NewInt(r.step), new(big.Int))
	if m.Sign() != 0 {
		return 0, false
	}
	return q.Int64(), true
}
