// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT
//
// Go port of asteval. Runtime objects other than plain data: builtin
// functions and methods, classes, astutils.Group, dict views, iterator
// objects, and Python's subscription and attribute access on all of them.

package asteval

import (
	"math/big"
	"strings"
	"sync"
	"unicode/utf8"
)

// callCtx is what a builtin receives besides its arguments.
type callCtx struct {
	in *Interpreter
}

// callable is a builtin function or a bound builtin method.
type callable struct {
	name string
	// typ is the Python type name: "builtin_function_or_method" for C
	// functions and methods, "function" for the Python functions asteval
	// defines (_type, _open), "method" for a bound Python method.
	typ string
	// self is the bound object of a method, nil for a function.
	self Value
	// qual names a Python-defined function or method in its repr.
	qual string
	fn   func(c *callCtx, args []Value, kw *dictV) (Value, error)
}

func (c *callable) pyType() string { return c.typ }

func (c *callable) repr(st *reprState) (string, error) {
	switch {
	case c.typ == "function":
		return "<function " + c.qual + " at " + objAddr(c) + ">", nil
	case c.typ == "method":
		s, err := reprWith(c.self, st)
		if err != nil {
			return "", err
		}
		return "<bound method " + c.qual + " of " + s + ">", nil
	case c.self == nil:
		return "<built-in function " + c.name + ">", nil
	case isClass(c.self):
		return "<built-in method " + c.name + " of type object at " + objAddr(c.self) + ">", nil
	}
	return "<built-in method " + c.name + " of " + c.self.pyType() + " object at " + objAddr(c.self) + ">", nil
}

func newBuiltin(name string, fn func(c *callCtx, args []Value, kw *dictV) (Value, error)) *callable {
	return &callable{name: name, typ: "builtin_function_or_method", fn: fn}
}

// classV is a class: a builtin type, an exception class or Group.
type classV struct {
	name string
	// module names the class's module in its repr, empty for builtins.
	module string
	base   *classV
	// construct calls the class.
	construct func(c *callCtx, args []Value, kw *dictV) (Value, error)
}

func (*classV) pyType() string { return "type" }

func (k *classV) repr(*reprState) (string, error) {
	if k.module != "" {
		return "<class '" + k.module + "." + k.name + "'>", nil
	}
	return "<class '" + k.name + "'>", nil
}

func isClass(v Value) bool {
	_, ok := v.(*classV)
	return ok
}

// subclassOf reports whether class k is other or derives from it.
func (k *classV) subclassOf(other *classV) bool {
	for c := k; c != nil; c = c.base {
		if c == other {
			return true
		}
	}
	return false
}

// callValue calls a value as Python calls it.
func callValue(c *callCtx, fn Value, args []Value, kw *dictV) (Value, error) {
	if kw == nil {
		kw = newDict()
	}
	switch f := fn.(type) {
	case *callable:
		if c == nil {
			c = &callCtx{}
		}
		return f.fn(c, args, kw)
	case *classV:
		if c == nil {
			c = &callCtx{}
		}
		return f.construct(c, args, kw)
	case *procV:
		return f.call(args, kw)
	case *methodDescV:
		return f.fn(c, args, kw)
	}
	return nil, typeErr("'%s' object is not callable", fn.pyType())
}

// isCallable is Python's callable().
func isCallable(v Value) bool {
	switch v.(type) {
	case *callable, *classV, *procV, *methodDescV:
		return true
	}
	return false
}

// ---------------------------------------------------------------- identity

var (
	idMu    sync.Mutex
	idTable = map[any]uint64{}
)

// objID gives each object a stable identity number, used for id(), for
// hashing by identity and for the addresses printed in default reprs.
func objID(v any) uint64 {
	idMu.Lock()
	defer idMu.Unlock()
	if id, ok := idTable[v]; ok {
		return id
	}
	id := uint64(0x7f3a00001000 + len(idTable)*0x30)
	idTable[v] = id
	return id
}

// objAddr is the address CPython prints in a default repr.
func objAddr(v any) string {
	return "0x" + strings.ToLower(hexUpper(int(objID(v)), 12))
}

// ---------------------------------------------------------------- Group

// groupV is astutils.Group: a dict whose items are also its attributes.
// Its name is stored as the item '__name__' and its search groups as the
// item '_searchgroups', as Group.__setattr__ puts them there.
type groupV struct{ d *dictV }

func (*groupV) pyType() string { return "Group" }

func (g *groupV) length() (int, error) { return len(g.d.keys), nil }

func (g *groupV) iter() (iterator, error) { return &dictIter{d: g.d, size: len(g.d.keys)}, nil }

func (g *groupV) contains(item Value) (bool, error) {
	_, ok, err := g.d.get(item)
	return ok, err
}

func (g *groupV) eq(other Value) (bool, error) { return Value(g) == other, nil }

func (g *groupV) repr(*reprState) (string, error) {
	n := 0
	for _, k := range g.d.keys {
		if s, ok := k.(strV); !ok || s != "__name__" {
			n++
		}
	}
	name, _ := g.d.getStr("__name__")
	ns, err := pyStr(name)
	if err != nil {
		return "", err
	}
	return "Group('" + ns + "', " + itoa(n) + " symbols)", nil
}

// validVarname is astutils.valid_varname.
func validVarname(name Value) (bool, error) {
	s, ok := name.(strV)
	if !ok {
		return false, newPyErr("AttributeError", "'"+name.pyType()+"' object has no attribute 'isidentifier'")
	}
	return pyIsIdentifier(string(s)) && !isReserved(string(s)), nil
}

// setattr is Group.__setattr__.
func (g *groupV) setattr(name string, value Value) error {
	ok, _ := validVarname(strV(name))
	if !ok {
		return newPyErr("SyntaxError", "invalid attribute name '"+name+"'")
	}
	return g.d.set(strV(name), value)
}

// setitem is Group.__setitem__.
func (g *groupV) setitem(key, value Value) error {
	ok, err := validVarname(key)
	if err != nil {
		return err
	}
	if ok {
		return g.d.set(key, value)
	}
	s, _ := key.(strV)
	return g.setattr(string(s), value)
}

// getattrDefault is Group.__getattr__(name, default): the item, else the
// default when it is not None, else AttributeError.
func (g *groupV) getattrDefault(name string, def Value) (Value, error) {
	if v, ok := g.d.getStr(name); ok {
		return v, nil
	}
	if def != nil {
		if _, isNone := def.(noneV); !isNone {
			return def, nil
		}
	}
	return nil, newPyErr("AttributeError", "no attribute named '"+name+"'")
}

// get is Group.get(key, default): the item, else the item in one of the
// search groups, else the default.
func (g *groupV) get(key string, def Value) (Value, error) {
	val, err := g.getattrDefault(key, returnedNone)
	if err != nil {
		return nil, err
	}
	if _, empty := val.(emptyV); !empty {
		return val, nil
	}
	sg, _ := g.d.getStr("_searchgroups")
	if sg != nil {
		if _, isNone := sg.(noneV); !isNone {
			names, err := toSlice(sg)
			if err != nil {
				return nil, err
			}
			for _, n := range names {
				ns, _ := n.(strV)
				grp, err := g.getattrDefault(string(ns), none)
				if err != nil {
					return nil, err
				}
				switch sub := grp.(type) {
				case *groupV:
					v, err := sub.getattrDefault(key, returnedNone)
					if err != nil {
						return nil, err
					}
					if _, empty := v.(emptyV); !empty {
						return v, nil
					}
				case *dictV:
					return nil, newPyErr("AttributeError", "'dict' object has no attribute '__getattr__'")
				}
			}
		}
	}
	return def, nil
}

// newGroup is Group(name, searchgroups, **kws).
func newGroup(name Value, searchgroups Value, kws []kwItem) (*groupV, error) {
	g := &groupV{d: newDict()}
	if _, isNone := name.(noneV); isNone || name == nil {
		name = strV(objAddr(g))
	}
	if err := g.setattr("__name__", name); err != nil {
		return nil, err
	}
	for _, kv := range kws {
		if err := g.d.set(strV(kv.name), kv.value); err != nil {
			return nil, err
		}
	}
	if searchgroups == nil {
		searchgroups = none
	}
	if err := g.setattr("_searchgroups", searchgroups); err != nil {
		return nil, err
	}
	return g, nil
}

type kwItem struct {
	name  string
	value Value
}

// ---------------------------------------------------------------- dict views

type dictViewV struct {
	d    *dictV
	kind string // keys, values or items
}

func (v *dictViewV) pyType() string { return "dict_" + v.kind }

func (v *dictViewV) items() []Value {
	switch v.kind {
	case "keys":
		return append([]Value(nil), v.d.keys...)
	case "values":
		return append([]Value(nil), v.d.vals...)
	}
	out := make([]Value, len(v.d.keys))
	for i := range v.d.keys {
		out[i] = newTuple([]Value{v.d.keys[i], v.d.vals[i]})
	}
	return out
}

func (v *dictViewV) length() (int, error) { return len(v.d.keys), nil }

func (v *dictViewV) iter() (iterator, error) {
	size, i := len(v.d.keys), 0
	return funcIter(func() (Value, bool, error) {
		if len(v.d.keys) != size {
			size = -1
			return nil, false, newPyErr("RuntimeError", "dictionary changed size during iteration")
		}
		if i >= len(v.d.keys) {
			return nil, false, nil
		}
		i++
		switch v.kind {
		case "keys":
			return v.d.keys[i-1], true, nil
		case "values":
			return v.d.vals[i-1], true, nil
		}
		return newTuple([]Value{v.d.keys[i-1], v.d.vals[i-1]}), true, nil
	}), nil
}

func (v *dictViewV) contains(item Value) (bool, error) {
	switch v.kind {
	case "keys":
		_, ok, err := v.d.get(item)
		return ok, err
	case "values":
		return seqContains(v.d.vals, item)
	}
	t, ok := item.(*tupleV)
	if !ok || len(t.items) != 2 {
		return false, nil
	}
	val, found, err := v.d.get(t.items[0])
	if err != nil || !found {
		return false, err
	}
	return pyEq(val, t.items[1])
}

func (v *dictViewV) repr(st *reprState) (string, error) {
	if !st.enter(v) {
		return "...", nil
	}
	defer st.leave()
	s, err := seqRepr("[", "]", v.items(), false, st)
	if err != nil {
		return "", err
	}
	return v.pyType() + "(" + s + ")", nil
}

// ---------------------------------------------------------------- iterators

// iterObj is an iterator object: map, filter, zip, enumerate, reversed and
// the iterators of the builtin containers.
type iterObj struct {
	typ string
	it  iterator
}

func (o *iterObj) pyType() string { return o.typ }

func (o *iterObj) iter() (iterator, error) { return o.it, nil }

// ---------------------------------------------------------------- subscription

// pyGetitem is obj[key].
func pyGetitem(obj, key Value) (Value, error) {
	switch x := obj.(type) {
	case *listV:
		return seqGetitem("list", x.items, key, func(items []Value) Value { return newList(items) })
	case *tupleV:
		return seqGetitem("tuple", x.items, key, func(items []Value) Value { return newTuple(items) })
	case strV:
		if sl, ok := key.(*sliceV); ok {
			s, err := pySliceStr(string(x), sl.start, sl.stop, sl.step)
			return strV(s), err
		}
		rs := []rune(string(x))
		i, ok, err := pyIndexNorm(len(rs), key)
		if err == errNotIndex {
			return nil, pyIndexTypeErr("str", key)
		}
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, pyIndexRangeErr("str")
		}
		return strV(string(rs[i])), nil
	case bytesV, *bytearrayV:
		b, _ := bytesOf(x)
		if sl, ok := key.(*sliceV); ok {
			start, stop, step, err := pySliceIndices(len(b), sl.start, sl.stop, sl.step)
			if err != nil {
				return nil, err
			}
			var out []byte
			for i, n := start, pySliceLen(start, stop, step); n > 0; i, n = i+step, n-1 {
				out = append(out, b[i])
			}
			if _, isArr := x.(*bytearrayV); isArr {
				return &bytearrayV{b: out}, nil
			}
			return bytesV(out), nil
		}
		i, ok, err := pyIndexNorm(len(b), key)
		if err == errNotIndex {
			return nil, typeErr("byte indices must be integers or slices, not %s", key.pyType())
		}
		if err != nil {
			return nil, err
		}
		if !ok {
			if _, isArr := x.(*bytearrayV); isArr {
				return nil, newPyErr("IndexError", "bytearray index out of range")
			}
			return nil, newPyErr("IndexError", "index out of range")
		}
		return mkInt(int64(b[i])), nil
	case rangeV:
		if sl, ok := key.(*sliceV); ok {
			n := int(x.length())
			start, stop, step, err := pySliceIndices(n, sl.start, sl.stop, sl.step)
			if err != nil {
				return nil, err
			}
			return rangeV{start: x.at(int64(start)), stop: x.at(int64(stop)), step: x.step * int64(step)}, nil
		}
		i, ok, err := pyIndexNorm(int(x.length()), key)
		if err == errNotIndex {
			return nil, typeErr("range indices must be integers or slices, not %s", key.pyType())
		}
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, newPyErr("IndexError", "range object index out of range")
		}
		return mkInt(x.at(int64(i))), nil
	case *dictV:
		v, ok, err := x.get(key)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, newExc("KeyError", []Value{key})
		}
		return v, nil
	case *groupV:
		v, ok, err := x.d.get(key)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, newExc("KeyError", []Value{key})
		}
		return v, nil
	case *classV:
		return nil, typeErr("type '%s' is not subscriptable", x.name)
	}
	return nil, typeErr("'%s' object is not subscriptable", obj.pyType())
}

func seqGetitem(name string, items []Value, key Value, mk func([]Value) Value) (Value, error) {
	if sl, ok := key.(*sliceV); ok {
		out, err := pySliceSeq(items, sl.start, sl.stop, sl.step)
		if err != nil {
			return nil, err
		}
		return mk(out), nil
	}
	i, ok, err := pyIndexNorm(len(items), key)
	if err == errNotIndex {
		return nil, pyIndexTypeErr(name, key)
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, pyIndexRangeErr(name)
	}
	return items[i], nil
}

// pySetitem is obj[key] = value.
func pySetitem(obj, key, value Value) error {
	switch x := obj.(type) {
	case *listV:
		if sl, ok := key.(*sliceV); ok {
			return listSetSlice(x, sl, value)
		}
		i, ok, err := pyIndexNorm(len(x.items), key)
		if err == errNotIndex {
			return pyIndexTypeErr("list", key)
		}
		if err != nil {
			return err
		}
		if !ok {
			return newPyErr("IndexError", "list assignment index out of range")
		}
		x.items[i] = value
		return nil
	case *dictV:
		return x.set(key, value)
	case *groupV:
		return x.setitem(key, value)
	case *bytearrayV:
		i, ok, err := pyIndexNorm(len(x.b), key)
		if err == errNotIndex {
			return typeErr("bytearray indices must be integers or slices, not %s", key.pyType())
		}
		if err != nil {
			return err
		}
		if !ok {
			return newPyErr("IndexError", "bytearray index out of range")
		}
		n, isInt := asInt(value)
		if !isInt {
			return typeErr("'%s' object cannot be interpreted as an integer", value.pyType())
		}
		if !n.IsInt64() || n.Int64() < 0 || n.Int64() > 255 {
			return newPyErr("ValueError", "byte must be in range(0, 256)")
		}
		x.b[i] = byte(n.Int64())
		return nil
	}
	return typeErr("'%s' object does not support item assignment", obj.pyType())
}

// listSetSlice is list[slice] = iterable.
func listSetSlice(l *listV, sl *sliceV, value Value) error {
	start, stop, step, err := pySliceIndices(len(l.items), sl.start, sl.stop, sl.step)
	if err != nil {
		return err
	}
	var items []Value
	if value == Value(l) {
		items = append([]Value(nil), l.items...)
	} else {
		items, err = toSlice(value)
		if err != nil {
			if isExc(err, "TypeError") {
				return typeErr("must assign iterable to extended slice")
			}
			return err
		}
	}
	if step == 1 {
		if stop < start {
			stop = start
		}
		out := append([]Value(nil), l.items[:start]...)
		out = append(out, items...)
		l.items = append(out, l.items[stop:]...)
		return nil
	}
	n := pySliceLen(start, stop, step)
	if len(items) != n {
		return newPyErr("ValueError", "attempt to assign sequence of size "+itoa(len(items))+" to extended slice of size "+itoa(n))
	}
	for i, j := start, 0; j < n; i, j = i+step, j+1 {
		l.items[i] = items[j]
	}
	return nil
}

// pyDelitem is del obj[key].
func pyDelitem(obj, key Value) error {
	switch x := obj.(type) {
	case *listV:
		if sl, ok := key.(*sliceV); ok {
			start, stop, step, err := pySliceIndices(len(x.items), sl.start, sl.stop, sl.step)
			if err != nil {
				return err
			}
			n := pySliceLen(start, stop, step)
			drop := map[int]bool{}
			for i, j := start, 0; j < n; i, j = i+step, j+1 {
				drop[i] = true
			}
			var out []Value
			for i, it := range x.items {
				if !drop[i] {
					out = append(out, it)
				}
			}
			x.items = out
			return nil
		}
		i, ok, err := pyIndexNorm(len(x.items), key)
		if err == errNotIndex {
			return pyIndexTypeErr("list", key)
		}
		if err != nil {
			return err
		}
		if !ok {
			return newPyErr("IndexError", "list assignment index out of range")
		}
		x.items = append(x.items[:i:i], x.items[i+1:]...)
		return nil
	case *dictV:
		found, err := x.del(key)
		if err != nil {
			return err
		}
		if !found {
			return newExc("KeyError", []Value{key})
		}
		return nil
	case *groupV:
		found, err := x.d.del(key)
		if err != nil {
			return err
		}
		if !found {
			return newExc("KeyError", []Value{key})
		}
		return nil
	}
	return typeErr("'%s' object doesn't support item deletion", obj.pyType())
}

// ---------------------------------------------------------------- attributes

// pyGetattr is getattr(obj, name) without a default: the attribute, or
// Python's AttributeError.
func pyGetattr(obj Value, name string) (Value, error) {
	if v, ok := attrOf(obj, name); ok {
		return v, nil
	}
	if g, ok := obj.(*groupV); ok {
		return g.getattrDefault(name, none)
	}
	if k, ok := obj.(*classV); ok {
		return nil, newPyErr("AttributeError", "type object '"+k.name+"' has no attribute '"+name+"'")
	}
	return nil, newPyErr("AttributeError", "'"+obj.pyType()+"' object has no attribute '"+name+"'")
}

// attrOf looks an attribute up on the value's type and the value.
func attrOf(obj Value, name string) (Value, bool) {
	switch x := obj.(type) {
	case *excV:
		if name == "args" {
			return newTuple(append([]Value(nil), x.args...)), true
		}
		return excMethod(x, name)
	case *procV:
		return x.attr(name)
	case *groupV:
		return dictMethod(obj, x.d, name, true)
	case *dictV:
		return dictMethod(obj, x, name, false)
	case *setV:
		return setMethod(x, name)
	case *sliceV:
		switch name {
		case "start":
			return x.start, true
		case "stop":
			return x.stop, true
		case "step":
			return x.step, true
		case "indices":
			return sliceIndicesMethod(x), true
		}
		return nil, false
	case complexV:
		switch name {
		case "real":
			return floatV(real(x)), true
		case "imag":
			return floatV(imag(x)), true
		case "conjugate":
			return methodsBound(obj, name, func(args []Value, kw *dictV) (Value, error) {
				if err := methodsArgsNone("complex.conjugate", args, kw); err != nil {
					return nil, err
				}
				return complexV(complex(real(x), -imag(x))), nil
			}), true
		}
		return nil, false
	case bytesV, *bytearrayV:
		return bytesMethod(obj, name)
	case *classV:
		return classAttr(x, name)
	}
	return builtinAttr(obj, name)
}

// pySetattr is setattr(obj, name, value).
func pySetattr(obj Value, name string, value Value) error {
	switch x := obj.(type) {
	case *groupV:
		return x.setattr(name, value)
	case *procV:
		return x.setattr(name, value)
	case *excV:
		if name == "args" {
			items, err := toSlice(value)
			if err != nil {
				return err
			}
			x.args = items
			x.Message = excStr(x.Class, items)
			return nil
		}
		if x.attrs == nil {
			x.attrs = map[string]Value{}
		}
		x.attrs[name] = value
		return nil
	case *classV:
		return typeErr("cannot set '%s' attribute of immutable type '%s'", name, x.name)
	}
	if _, ok := attrOf(obj, name); ok {
		return newPyErr("AttributeError", "'"+obj.pyType()+"' object attribute '"+name+"' is read-only")
	}
	return newPyErr("AttributeError", "'"+obj.pyType()+"' object has no attribute '"+name+"'")
}

// pyDelattr is delattr(obj, name).
func pyDelattr(obj Value, name string) error {
	if g, ok := obj.(*groupV); ok {
		found, _ := g.d.del(strV(name))
		if found {
			return nil
		}
		return newPyErr("AttributeError", name)
	}
	if _, ok := attrOf(obj, name); ok {
		return newPyErr("AttributeError", "'"+obj.pyType()+"' object attribute '"+name+"' is read-only")
	}
	return newPyErr("AttributeError", "'"+obj.pyType()+"' object has no attribute '"+name+"'")
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

var bigOne = big.NewInt(1)
