// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT
//
// Go port of asteval. The attributes of sets, bytes, exceptions, slices,
// dicts and Groups, and the unbound methods of the builtin classes.

package asteval

import (
	"strings"
)

// excMethod is an exception's attribute other than args.
func excMethod(x *excV, name string) (Value, bool) {
	v, ok := x.attrs[name]
	return v, ok
}

// dictMethod is an attribute of a dict or of a Group (which overrides get).
func dictMethod(obj Value, d *dictV, name string, isGroup bool) (Value, bool) {
	if isGroup && name == "get" {
		g := obj.(*groupV)
		return &callable{name: "get", typ: "method", qual: "Group.get", self: obj,
			fn: func(c *callCtx, args []Value, kw *dictV) (Value, error) {
				f := &methodsPyFunc{qual: "Group.get", names: []string{"key", "default"}, defaults: []Value{none}}
				locals, _, _, err := f.bind(args, kw)
				if err != nil {
					return nil, err
				}
				key, ok := locals[0].(strV)
				if !ok {
					if v, found, err := g.d.get(locals[0]); err != nil || found {
						return v, err
					}
					return locals[1], nil
				}
				return g.get(string(key), locals[1])
			}}, true
	}
	if m, ok := methodsDict[name]; ok {
		return methodsBound(obj, name, func(args []Value, kw *dictV) (Value, error) { return m(d, args, kw) }), true
	}
	return nil, false
}

// setMethod is an attribute of a set or frozenset.
func setMethod(s *setV, name string) (Value, bool) {
	typ := s.pyType()
	bound := func(fn func(args []Value, kw *dictV) (Value, error)) (Value, bool) {
		return methodsBound(s, name, fn), true
	}
	others := func(args []Value) ([]*setV, error) {
		out := make([]*setV, len(args))
		for i, a := range args {
			o, err := toSet(a)
			if err != nil {
				return nil, err
			}
			out[i] = o
		}
		return out, nil
	}
	switch name {
	case "copy":
		return bound(func(args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsNone(typ+".copy", args, kw); err != nil {
				return nil, err
			}
			if s.frozen {
				return s, nil
			}
			return s.copyAs(false), nil
		})
	case "union", "intersection", "difference", "symmetric_difference":
		return bound(func(args []Value, kw *dictV) (Value, error) {
			if err := noKw(typ+"."+name, kw); err != nil {
				return nil, err
			}
			if name == "symmetric_difference" {
				if err := methodsArgsOne(typ+".symmetric_difference", args, kw); err != nil {
					return nil, err
				}
				return setSymmetricDifference(s, args[0], s.frozen)
			}
			if name == "union" {
				return setUnion(s, args, s.frozen)
			}
			r := s.copyAs(s.frozen)
			if len(args) == 0 {
				return r, nil
			}
			for _, a := range args {
				var err error
				if name == "intersection" {
					r, err = setIntersection(r, a, s.frozen)
				} else {
					r, err = setDifference(r, a, s.frozen)
				}
				if err != nil {
					return nil, err
				}
			}
			return r, nil
		})
	case "isdisjoint", "issubset", "issuperset":
		return bound(func(args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsOne(typ+"."+name, args, kw); err != nil {
				return nil, err
			}
			os, err := others(args)
			if err != nil {
				return nil, err
			}
			o := os[0]
			switch name {
			case "issubset":
				r, err := s.subsetOf(o)
				return pyBool(r), err
			case "issuperset":
				r, err := o.subsetOf(s)
				return pyBool(r), err
			}
			inter, err := setIntersection(s, o, true)
			return pyBool(err == nil && inter.used == 0), err
		})
	}
	if s.frozen {
		return nil, false
	}
	switch name {
	case "add", "remove", "discard":
		return bound(func(args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsOne("set."+name, args, kw); err != nil {
				return nil, err
			}
			if name == "add" {
				return none, s.add(args[0])
			}
			found, err := s.discard(args[0])
			if err != nil {
				return nil, err
			}
			if !found && name == "remove" {
				return nil, newExc("KeyError", []Value{args[0]})
			}
			return none, nil
		})
	case "pop":
		return bound(func(args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsNone("set.pop", args, kw); err != nil {
				return nil, err
			}
			return s.pop()
		})
	case "clear":
		return bound(func(args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsNone("set.clear", args, kw); err != nil {
				return nil, err
			}
			s.clear()
			return none, nil
		})
	case "update":
		return bound(func(args []Value, kw *dictV) (Value, error) {
			if err := noKw("set.update", kw); err != nil {
				return nil, err
			}
			for _, a := range args {
				if err := s.update(a); err != nil {
					return nil, err
				}
			}
			return none, nil
		})
	case "intersection_update", "difference_update", "symmetric_difference_update":
		return bound(func(args []Value, kw *dictV) (Value, error) {
			if err := noKw("set."+name, kw); err != nil {
				return nil, err
			}
			r := s
			for _, a := range args {
				var err error
				switch name {
				case "intersection_update":
					r, err = setIntersection(r, a, false)
				case "difference_update":
					r, err = setDifference(r, a, false)
				default:
					r, err = setSymmetricDifference(r, a, false)
				}
				if err != nil {
					return nil, err
				}
			}
			if r != s {
				s.clear()
				_ = s.merge(r)
			}
			return none, nil
		})
	}
	return nil, false
}

func sliceIndicesMethod(s *sliceV) Value {
	return methodsBound(s, "indices", func(args []Value, kw *dictV) (Value, error) {
		if err := methodsArgsOne("slice.indices", args, kw); err != nil {
			return nil, err
		}
		n, err := methodsIndex(args[0])
		if err != nil {
			return nil, err
		}
		if n.Sign() < 0 {
			return nil, valueErr("length should not be negative")
		}
		start, stop, step, err := pySliceIndices(int(n.Int64()), s.start, s.stop, s.step)
		if err != nil {
			return nil, err
		}
		return newTuple([]Value{mkInt(int64(start)), mkInt(int64(stop)), mkInt(int64(step))}), nil
	})
}

// bytesMethod is an attribute of bytes or bytearray.
func bytesMethod(obj Value, name string) (Value, bool) {
	b, _ := bytesOf(obj)
	typ := obj.pyType()
	wrap := func(s string) Value {
		if typ == "bytearray" {
			return &bytearrayV{b: []byte(s)}
		}
		return bytesV(s)
	}
	switch name {
	case "decode":
		return methodsBound(obj, name, func(args []Value, kw *dictV) (Value, error) {
			buf, err := methodsArgsKw("decode", []string{"encoding", "errors"}, 2, args, kw)
			if err != nil {
				return nil, err
			}
			return decodeBytes(b, buf[0], buf[1])
		}), true
	case "hex":
		return methodsBound(obj, name, func(args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsNone(typ+".hex", args, kw); err != nil {
				return nil, err
			}
			var sb strings.Builder
			for i := 0; i < len(b); i++ {
				sb.WriteString(hexN(int(b[i]), 2))
			}
			return strV(sb.String()), nil
		}), true
	case "upper", "lower":
		return methodsBound(obj, name, func(args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsNone(typ+"."+name, args, kw); err != nil {
				return nil, err
			}
			out := []byte(b)
			for i, c := range out {
				if name == "upper" && c >= 'a' && c <= 'z' {
					out[i] = c - 32
				} else if name == "lower" && c >= 'A' && c <= 'Z' {
					out[i] = c + 32
				}
			}
			return wrap(string(out)), nil
		}), true
	case "startswith", "endswith", "count", "find":
		return methodsBound(obj, name, func(args []Value, kw *dictV) (Value, error) {
			if err := methodsArgsPos(typ+"."+name, 1, 1, args, kw); err != nil {
				return nil, err
			}
			sub, ok := bytesOf(args[0])
			if !ok {
				return nil, typeErr("a bytes-like object is required, not '%s'", args[0].pyType())
			}
			switch name {
			case "startswith":
				return pyBool(strings.HasPrefix(b, sub)), nil
			case "endswith":
				return pyBool(strings.HasSuffix(b, sub)), nil
			case "count":
				if sub == "" {
					return mkInt(int64(len(b) + 1)), nil
				}
				return mkInt(int64(strings.Count(b, sub))), nil
			}
			return mkInt(int64(strings.Index(b, sub))), nil
		}), true
	}
	return nil, false
}

// classAttr is an attribute of a builtin class: its methods, unbound.
func classAttr(k *classV, name string) (Value, bool) {
	if (k.name == "dict" || k == groupClass) && name == "fromkeys" {
		return &callable{name: "fromkeys", typ: "builtin_function_or_method", self: k,
			fn: func(c *callCtx, args []Value, kw *dictV) (Value, error) {
				if err := posOnly("fromkeys", 1, 2, args, kw); err != nil {
					return nil, err
				}
				var val Value = none
				if len(args) == 2 {
					val = args[1]
				}
				keys, err := toSlice(args[0])
				if err != nil {
					return nil, err
				}
				d := newDict()
				for _, key := range keys {
					if err := d.set(key, val); err != nil {
						return nil, err
					}
				}
				return d, nil
			}}, true
	}
	sample := map[string]Value{"str": strV(""), "list": newList(nil), "dict": newDict(), "tuple": newTuple(nil),
		"set": newSet(false), "frozenset": newSet(true), "int": mkInt(0), "float": floatV(0), "bytes": bytesV("")}[k.name]
	if sample == nil {
		return nil, false
	}
	if _, ok := attrOf(sample, name); !ok {
		return nil, false
	}
	if _, isCall := mustAttr(sample, name).(*callable); !isCall {
		return nil, false
	}
	desc := &callable{name: name, typ: "method_descriptor", qual: k.name + "." + name}
	desc.fn = func(c *callCtx, args []Value, kw *dictV) (Value, error) {
		if len(args) == 0 {
			return nil, typeErr("unbound method %s.%s() needs an argument", k.name, name)
		}
		if !classOf(args[0]).subclassOf(k) {
			return nil, typeErr("descriptor '%s' for '%s' objects doesn't apply to a '%s' object", name, k.name, args[0].pyType())
		}
		m, ok := attrOf(args[0], name)
		if !ok {
			return nil, newPyErr("AttributeError", name)
		}
		return callValue(c, m, args[1:], kw)
	}
	return &methodDescV{desc}, true
}

func mustAttr(v Value, name string) Value {
	a, _ := attrOf(v, name)
	return a
}

// methodDescV is an unbound method of a builtin class.
type methodDescV struct{ *callable }

func (m *methodDescV) repr(*reprState) (string, error) {
	i := strings.IndexByte(m.qual, '.')
	return "<method '" + m.name + "' of '" + m.qual[:i] + "' objects>", nil
}
