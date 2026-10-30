// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT
//
// Go port of asteval.astutils: ExceptionHolder, Procedure, the symbol
// table, the safe attribute and format wrappers, and the name checks.

package asteval

import (
	"strings"
)

// reservedWords is astutils.RESERVED_WORDS.
var reservedWords = []string{"False", "None", "True", "and", "as", "assert", "async", "await", "break", "class",
	"continue", "def", "del", "elif", "else", "except", "finally", "for", "from", "global", "if", "import", "in",
	"is", "lambda", "nonlocal", "not", "or", "pass", "raise", "return", "try", "while", "with", "yield", "exec",
	"eval", "execfile", "__import__", "__package__", "__fstring__"}

func isReserved(name string) bool { return contains(reservedWords, name) }

// unsafeAttrs is astutils.UNSAFE_ATTRS.
var unsafeAttrs = []string{"__subclasses__", "__bases__", "__globals__", "__code__", "__reduce__",
	"__reduce_ex__", "__mro__", "__closure__", "__func__", "__self__", "__module__", "__dict__", "__class__",
	"__call__", "__get__", "__getattribute__", "__subclasshook__", "__new__", "__init__", "func_globals",
	"func_code", "func_closure", "im_class", "im_func", "im_self", "gi_code", "gi_frame", "f_locals",
	"__asteval__", "mro"}

// validSymbolName is astutils.valid_symbol_name: not a reserved word, and a
// single NAME token as Python's tokenizer reads it.
func validSymbolName(name string) bool {
	if isReserved(name) {
		return false
	}
	return pyIsIdentifier(name)
}

// ---------------------------------------------------------------- ExceptionHolder

// ExceptionHolder is astutils.ExceptionHolder: one entry of an
// interpreter's error list.
type ExceptionHolder struct {
	node      *node
	expr      *string
	msg       string
	exc       string // the exception class name, "" for None
	text      *string
	lineno    *int
	endLineno *int
	colOffset int
	excInfo   *excV // sys.exc_info()[1] when the holder was made
}

func newHolder(n *node, exc, msg string, expr, text *string, lineno *int, info *excV) *ExceptionHolder {
	h := &ExceptionHolder{node: n, expr: expr, msg: msg, exc: exc, text: text, lineno: lineno, endLineno: lineno,
		excInfo: info}
	if lineno == nil && n != nil && n.hasPos {
		l, e := n.lineno, n.endLineno
		h.lineno, h.endLineno, h.colOffset = &l, &e, n.col
	}
	if h.exc == "" && info != nil {
		h.exc = info.Class
	}
	if h.msg == "" && info != nil {
		h.msg = info.Message
	}
	return h
}

func (h *ExceptionHolder) excClass() string {
	if h.exc == "" {
		return "Exception"
	}
	return h.exc
}

// getErrorText is get_error()[1].
func (h *ExceptionHolder) getErrorText() string {
	excName := h.exc
	if excName == "" || excName == "None" {
		excName = "UnknownError"
	}
	var out []string
	text := "None"
	if h.text != nil {
		text = *h.text
	}
	code := strings.Split(text, "\n")
	if h.lineno == nil || h.endLineno == nil {
		if h.expr == nil {
			out = append(out, "None")
		} else {
			out = append(out, *h.expr)
		}
	} else {
		lo, hi := pySliceRange(len(code), *h.lineno-1, *h.endLineno)
		if lo < hi {
			out = append(out, strings.Join(code[lo:hi], "\n"))
		} else {
			out = append(out, "")
		}
	}
	if h.colOffset > 0 {
		out = append(out, strings.Repeat(" ", h.colOffset)+"^^^^")
	}
	out = append(out, excName+": "+h.msg)
	return strings.Join(out, "\n")
}

// pySliceRange resolves seq[start:stop] bounds as Python does.
func pySliceRange(n, start, stop int) (int, int) {
	norm := func(i int) int {
		if i < 0 {
			i += n
			if i < 0 {
				i = 0
			}
		}
		if i > n {
			i = n
		}
		return i
	}
	return norm(start), norm(stop)
}

// ---------------------------------------------------------------- Procedure

type procKw struct {
	name  string
	value Value
}

// procV is astutils.Procedure: a function defined by def or lambda.
type procV struct {
	name     string
	in       *Interpreter
	doc      Value // the docstring constant, nil for none
	lineno   int
	body     []*node
	text     string
	argnames []string
	kwargs   []procKw
	vararg   *string
	varkws   *string
	isLambda bool
}

func (*procV) pyType() string { return "Procedure" }

func (p *procV) signature() (string, error) {
	sig := strings.Join(p.argnames, ", ")
	if p.vararg != nil {
		sig += "*" + *p.vararg
	}
	if len(p.kwargs) > 0 {
		if len(sig) > 0 {
			sig += ", "
		}
		parts := make([]string, len(p.kwargs))
		for i, kv := range p.kwargs {
			s, err := pyStr(kv.value)
			if err != nil {
				return "", err
			}
			parts[i] = kv.name + "=" + s
		}
		sig += strings.Join(parts, ", ")
		if p.varkws != nil {
			sig += ", **" + *p.varkws
		}
	}
	return p.name + "(" + sig + ")", nil
}

func (p *procV) repr(*reprState) (string, error) {
	sig, err := p.signature()
	if err != nil {
		return "", err
	}
	rep := "<Procedure " + sig + ">"
	if p.doc != nil {
		d, err := pyStr(p.doc)
		if err != nil {
			return "", err
		}
		rep += "\n " + d
	}
	return rep, nil
}

func (p *procV) attr(name string) (Value, bool) {
	switch name {
	case "name":
		return strV(p.name), true
	case "lineno":
		return mkInt(int64(p.lineno)), true
	}
	return nil, false
}

// setattr is Procedure.__setattr__ after construction.
func (p *procV) setattr(string, Value) error {
	return p.in.raiseException(nil, "TypeError", "procedure is read-only", nil, nil)
}

// call is Procedure.__call__.
func (p *procV) call(argsIn []Value, kwIn *dictV) (Value, error) {
	in := p.in
	topsym := in.symtable
	nested := in.config["nested_symtable"]
	var symlocals Value
	if nested {
		var sargs []kwItem
		sargs = append(sargs, kwItem{"_main", topsym})
		sgroups, err := symGet(topsym, "_searchgroups", none)
		if err != nil {
			return nil, err
		}
		if !isNoneV(sgroups) {
			names, err := toSlice(sgroups)
			if err != nil {
				return nil, err
			}
			for _, sx := range names {
				sxs, _ := sx.(strV)
				v, err := symGet(topsym, string(sxs), none)
				if err != nil {
					return nil, err
				}
				replaced := false
				for i := range sargs {
					if sargs[i].name == string(sxs) {
						sargs[i].value, replaced = v, true
					}
				}
				if !replaced {
					sargs = append(sargs, kwItem{string(sxs), v})
				}
			}
		}
		g, err := newGroup(strV("symtable_"+p.name+"_"), nil, sargs)
		if err != nil {
			return nil, err
		}
		keys := make([]Value, len(sargs))
		for i, kv := range sargs {
			keys[i] = strV(kv.name)
		}
		if err := g.setattr("_searchgroups", newList(keys)); err != nil {
			return nil, err
		}
		symlocals = g
	} else {
		symlocals = newDict()
	}

	args := append([]Value(nil), argsIn...)
	kwargs := kwIn.copy()
	nargs, nkws := len(args), kwargs.Len()
	expected := len(p.argnames)
	if nargs < expected && nkws > 0 {
		for _, name := range p.argnames[nargs:] {
			if v, ok := kwargs.getStr(name); ok {
				_, _ = kwargs.del(strV(name))
				args = append(args, v)
			}
		}
		nargs = len(args)
	}
	if nargs < expected {
		return nil, in.raiseException(nil, "TypeError", p.name+"() takes at least "+itoa(expected)+" arguments, got "+itoa(nargs), nil, nil)
	}
	if len(p.argnames) > 0 {
		for _, targ := range p.argnames {
			if _, ok := kwargs.getStr(targ); ok {
				l := p.lineno
				return nil, in.raiseException(nil, "TypeError", "multiple values for keyword argument '"+targ+"' in Procedure "+p.name, nil, &l)
			}
		}
	}
	if nargs > expected && p.vararg == nil {
		if nargs-expected > len(p.kwargs) {
			return nil, in.raiseException(nil, "TypeError", "too many arguments for "+p.name+"() expected at most "+
				itoa(len(p.kwargs)+expected)+", got "+itoa(nargs), nil, nil)
		}
		for i, xarg := range args[expected:] {
			kwName := p.kwargs[i].name
			if _, ok := kwargs.getStr(kwName); !ok {
				kwargs.setStr(kwName, xarg)
			}
		}
	}
	for _, argname := range p.argnames {
		if err := symSet(symlocals, argname, args[0]); err != nil {
			return nil, err
		}
		args = args[1:]
	}
	if err := p.bindRest(symlocals, args, kwargs); err != nil {
		if !isExc(err, "ValueError", "LookupError", "TypeError", "NameError", "AttributeError") {
			return nil, err
		}
		var raised error
		_ = in.handling(err, func() error {
			l := p.lineno
			raised = in.raiseException(nil, "", "incorrect arguments for Procedure "+p.name, nil, &l)
			return nil
		})
		return nil, raised
	}

	var save Value
	if nested {
		save = in.symtable
		in.symtable = symlocals
	} else {
		save = symCopy(in.symtable)
		if err := symUpdate(in.symtable, symlocals); err != nil {
			return nil, err
		}
	}
	in.retval = nil
	if in.calldepth == 0 && len(in.errors) > 0 {
		in.errors = nil
	}
	in.calldepth++
	var retval Value
	in.codeText = append(in.codeText, p.text)
	restore := func() {
		in.symtable = save
		in.codeText = in.codeText[:len(in.codeText)-1]
		in.calldepth--
		in.interrupt = nil
	}
	for _, n := range p.body {
		l := n.lineno
		out, err := in.run(n, nil, &l, true)
		if err != nil {
			restore()
			return nil, err
		}
		if p.isLambda {
			retval = out
			break
		}
		if len(in.errors) > 0 {
			break
		}
		if in.retval != nil {
			retval = in.retval
			in.retval = nil
			if _, empty := retval.(emptyV); empty {
				retval = nil
			}
			break
		}
	}
	restore()
	if retval == nil {
		return none, nil
	}
	return retval, nil
}

// bindRest is the part of Procedure.__call__ inside its try block.
func (p *procV) bindRest(symlocals Value, args []Value, kwargs *dictV) error {
	if p.vararg != nil {
		if err := symSet(symlocals, *p.vararg, newTuple(append([]Value(nil), args...))); err != nil {
			return err
		}
	}
	for _, kv := range p.kwargs {
		val := kv.value
		if v, ok := kwargs.getStr(kv.name); ok {
			_, _ = kwargs.del(strV(kv.name))
			val = v
		}
		if err := symSet(symlocals, kv.name, val); err != nil {
			return err
		}
	}
	if p.varkws != nil {
		return symSet(symlocals, *p.varkws, kwargs)
	}
	if kwargs.Len() > 0 {
		names := make([]string, len(kwargs.keys))
		for i, k := range kwargs.keys {
			names[i], _ = pyStr(k)
		}
		l := p.lineno
		return p.in.raiseException(nil, "TypeError", "extra keyword arguments for Procedure "+p.name+": "+strings.Join(names, ","), nil, &l)
	}
	return nil
}

// ---------------------------------------------------------------- symbol tables

// The symbol table is a dict, or a Group for a nested one. These helpers
// apply the dict operations asteval uses, through Group's overrides.

func symGet(st Value, name string, def Value) (Value, error) {
	switch t := st.(type) {
	case *groupV:
		return t.get(name, def)
	case *dictV:
		if v, ok := t.getStr(name); ok {
			return v, nil
		}
		return def, nil
	}
	return nil, newPyErr("AttributeError", "'"+st.pyType()+"' object has no attribute 'get'")
}

func symSet(st Value, name string, v Value) error {
	return pySetitem(st, strV(name), v)
}

func symGetitem(st Value, name string) (Value, error) { return pyGetitem(st, strV(name)) }

func symDict(st Value) *dictV {
	switch t := st.(type) {
	case *groupV:
		return t.d
	case *dictV:
		return t
	}
	return newDict()
}

func symPop(st Value, name string) (Value, error) {
	d := symDict(st)
	v, ok := d.getStr(name)
	if !ok {
		return nil, newExc("KeyError", []Value{strV(name)})
	}
	_, _ = d.del(strV(name))
	return v, nil
}

func symPopDefault(st Value, name string) (Value, error) {
	d := symDict(st)
	v, ok := d.getStr(name)
	if !ok {
		return none, nil
	}
	_, _ = d.del(strV(name))
	return v, nil
}

func symContains(st Value, name string) (bool, error) {
	_, ok := symDict(st).getStr(name)
	return ok, nil
}

func symKeys(st Value) []Value { return append([]Value(nil), symDict(st).keys...) }

func symItems(st Value) ([]Value, []Value) {
	d := symDict(st)
	return append([]Value(nil), d.keys...), append([]Value(nil), d.vals...)
}

// symCopy is dict.copy(), which gives a plain dict for a Group too.
func symCopy(st Value) Value { return symDict(st).copy() }

// symUpdate is dict.update, which bypasses Group.__setitem__.
func symUpdate(st Value, src Value) error {
	d, s := symDict(st), symDict(src)
	for i, k := range s.keys {
		if err := d.set(k, s.vals[i]); err != nil {
			return err
		}
	}
	return nil
}

// makeSymbolTable is astutils.make_symbol_table(use_numpy=False, nested,
// top, **kws).
func makeSymbolTable(nested, top bool, kws []Symbol) Value {
	var items []kwItem
	for _, s := range kws {
		items = append(items, kwItem{s.Name, s.Value})
	}
	var st Value
	if nested {
		name := "_"
		if top {
			name = "_main"
			for i, kv := range items {
				if kv.name == "name" {
					name, _ = pyStr(kv.value)
					items = append(items[:i:i], items[i+1:]...)
					break
				}
			}
		}
		g, _ := newGroup(strV(name), nil, []kwItem{{"Group", groupClass}})
		st = g
	} else {
		st = newDict()
	}
	d := symDict(st)
	for _, name := range fromPy {
		d.setStr(name, builtinsTable[name])
	}
	d.setStr("open", localOpen)
	d.setStr("type", localType)
	for _, kv := range items {
		d.setStr(kv.name, kv.value)
	}
	if nested {
		mg, _ := newGroup(strV("math"), nil, mathItems())
		d.setStr("math", mg)
		d.setStr("Group", groupClass)
		_ = st.(*groupV).setattr("_searchgroups", newTuple([]Value{strV("math")}))
	} else {
		for _, kv := range mathItems() {
			d.setStr(kv.name, kv.value)
		}
	}
	for _, kv := range items {
		d.setStr(kv.name, kv.value)
	}
	return st
}

// ---------------------------------------------------------------- safe wrappers

func isDunder(attr string) bool {
	return strings.HasPrefix(attr, "__") && strings.HasSuffix(attr, "__")
}

// safeGetattr is astutils.safe_getattr.
func (in *Interpreter) safeGetattr(obj Value, attr string, n *node, allowUnsafeModules bool) (Value, error) {
	unsafe := contains(unsafeAttrs, attr) || isDunder(attr)
	if !unsafe && (attr == "format" || attr == "format_map") {
		_, isStr := obj.(strV)
		unsafe = isStr || obj == Value(builtinsTable["str"])
	}
	if !unsafe && !allowUnsafeModules {
		// getattr(obj, attr) is compared with each unsafe module; none is
		// reachable, so only its AttributeError matters.
		for i := 0; i < 4; i++ {
			if _, err := pyGetattr(obj, attr); err != nil {
				return nil, err
			}
		}
	}
	if unsafe {
		r, err := pyRepr(obj)
		if err != nil {
			return nil, err
		}
		return nil, in.raiseException(n, "AttributeError", "no safe attribute '"+attr+"' for "+r, nil, nil)
	}
	v, err := pyGetattr(obj, attr)
	if err != nil {
		if isExc(err, "AttributeError") {
			return none, nil
		}
		return nil, err
	}
	return v, nil
}

// safeSetattr is astutils.safe_setattr.
func (in *Interpreter) safeSetattr(obj Value, attr string, val Value, n *node) error {
	if contains(unsafeAttrs, attr) || isDunder(attr) {
		return in.raiseException(n, "AttributeError", "cannot assign to unsafe attribute "+attr, nil, nil)
	}
	return pySetattr(obj, attr, val)
}

// safeFormat is astutils.safe_format: string.Formatter.vformat with
// attribute lookups through safe_getattr.
func (in *Interpreter) safeFormat(format string, n *node, val Value) (Value, error) {
	kw := newDict()
	kw.setStr("__fstring__", val)
	s, err := strFormat(format, nil, kw, fmtHooks{
		getattr: func(obj Value, name string) (Value, error) {
			return in.safeGetattr(obj, name, n, false)
		},
		getitem:     pyGetitem,
		pyFormatter: true,
	})
	if err != nil {
		return nil, err
	}
	return strV(s), nil
}

// ---------------------------------------------------------------- deepcopy

// deepcopy is copy.deepcopy for the values a symbol can hold.
func deepcopy(v Value) (Value, error) {
	return deepcopyMemo(v, map[Value]Value{})
}

func deepcopyMemo(v Value, memo map[Value]Value) (Value, error) {
	switch x := v.(type) {
	case *listV:
		if c, ok := memo[v]; ok {
			return c, nil
		}
		out := newList(nil)
		memo[v] = out
		for _, it := range x.items {
			c, err := deepcopyMemo(it, memo)
			if err != nil {
				return nil, err
			}
			out.items = append(out.items, c)
		}
		return out, nil
	case *tupleV:
		if c, ok := memo[v]; ok {
			return c, nil
		}
		items := make([]Value, len(x.items))
		same := true
		for i, it := range x.items {
			c, err := deepcopyMemo(it, memo)
			if err != nil {
				return nil, err
			}
			items[i] = c
			same = same && c == it
		}
		if same {
			return x, nil
		}
		t := newTuple(items)
		memo[v] = t
		return t, nil
	case *dictV:
		if c, ok := memo[v]; ok {
			return c, nil
		}
		out := newDict()
		memo[v] = out
		for i, k := range x.keys {
			kc, err := deepcopyMemo(k, memo)
			if err != nil {
				return nil, err
			}
			vc, err := deepcopyMemo(x.vals[i], memo)
			if err != nil {
				return nil, err
			}
			if err := out.set(kc, vc); err != nil {
				return nil, err
			}
		}
		return out, nil
	case *setV:
		out := newSet(x.frozen)
		memo[v] = out
		for _, it := range x.items() {
			c, err := deepcopyMemo(it, memo)
			if err != nil {
				return nil, err
			}
			if err := out.add(c); err != nil {
				return nil, err
			}
		}
		return out, nil
	case *groupV:
		out := &groupV{d: newDict()}
		memo[v] = out
		for i, k := range x.d.keys {
			c, err := deepcopyMemo(x.d.vals[i], memo)
			if err != nil {
				return nil, err
			}
			if err := out.setitem(k, c); err != nil {
				return nil, err
			}
		}
		return out, nil
	case *bytearrayV:
		return &bytearrayV{b: append([]byte(nil), x.b...)}, nil
	case *sliceV:
		return &sliceV{start: x.start, stop: x.stop, step: x.step}, nil
	case *excV:
		args := make([]Value, len(x.args))
		for i, a := range x.args {
			c, err := deepcopyMemo(a, memo)
			if err != nil {
				return nil, err
			}
			args[i] = c
		}
		return newExc(x.Class, args), nil
	case *dictViewV, *iterObj:
		return nil, typeErr("cannot pickle '%s' object", v.pyType())
	}
	return v, nil
}

// ---------------------------------------------------------------- other objects

// astNodeV is an ast node that reached Python code as a value (asteval
// returns the Break and Continue nodes of an interrupt).
type astNodeV struct{ n *node }

func (a *astNodeV) pyType() string { return a.n.kind }

func (a *astNodeV) repr(*reprState) (string, error) {
	return "<ast." + a.n.kind + " object at " + objAddr(a.n) + ">", nil
}

// opaqueV is an object of a class the port does not otherwise model; it
// only prints.
type opaqueV struct{ typ, qual string }

func (o *opaqueV) pyType() string { return o.typ }

func (o *opaqueV) repr(*reprState) (string, error) {
	return "<" + o.qual + " object at " + objAddr(o) + ">", nil
}

// typeRepr is repr(type(v)).
func typeRepr(v Value) string {
	switch x := v.(type) {
	case *procV:
		return "<class 'asteval.astutils.Procedure'>"
	case *groupV:
		return "<class 'asteval.astutils.Group'>"
	case emptyV:
		return "<class 'asteval.astutils.Empty'>"
	case *astNodeV:
		return "<class 'ast." + x.n.kind + "'>"
	case *opaqueV:
		return "<class '" + x.qual + "'>"
	}
	return "<class '" + v.pyType() + "'>"
}
