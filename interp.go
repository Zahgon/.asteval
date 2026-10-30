// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT
//
// Go port of asteval.asteval: the Interpreter, its error bookkeeping and the
// handler of every supported node. The port follows the original statement
// by statement, including how a Python exception travels through it: an
// error recorded at every level of the tree, sys.exc_info() as seen at each
// point, and the messages re-raised from the first one.

package asteval

import (
	"fmt"
	"io"
	"math/big"
	"os"
	"sort"
	"strings"
)

// allNodes are the node kinds asteval has a handler slot for (ALL_NODES).
var allNodes = []string{"arg", "assert", "assign", "attribute", "augassign", "binop", "boolop", "break", "call",
	"compare", "constant", "continue", "delete", "dict", "dictcomp", "excepthandler", "expr", "for",
	"functiondef", "if", "ifexp", "import", "importfrom", "interrupt", "lambda", "list", "listcomp", "module",
	"name", "pass", "raise", "return", "set", "setcomp", "slice", "subscript", "try", "tuple", "unaryop",
	"while", "with", "formattedvalue", "joinedstr"}

// configurable are the node flags MINIMAL_CONFIG turns off and
// DEFAULT_CONFIG turns on.
var configurable = []string{"assert", "augassign", "delete", "if", "ifexp", "for", "formattedvalue",
	"functiondef", "print", "raise", "lambda", "listcomp", "dictcomp", "setcomp", "try", "while", "with"}

const noRaise = "asteval will not raise"

type handler func(n *node) (Value, error)

// Interpreter is asteval.Interpreter.
type Interpreter struct {
	config             map[string]bool
	writer, errWriter  io.Writer
	maxStatementLength int
	symtable           Value // *dictV, or *groupV for a nested symbol table
	interrupt          *node // _interrupt: a Break, Continue, Return or Raise node
	errors             []*ExceptionHolder
	errorMsg           *string
	expr               *string
	retval             Value // nil is Python's None
	calldepth          int
	lineno             int
	codeText           []string
	handlers           map[string]handler
	allowUnsafeModules bool
	readonly           map[string]bool
	noDeepcopy         []string
	// excInfo is the stack of exceptions being handled: sys.exc_info() is
	// its top.
	excInfo []*excV
	printer *callable
}

func (in *Interpreter) currentExc() *excV {
	if len(in.excInfo) == 0 {
		return nil
	}
	return in.excInfo[len(in.excInfo)-1]
}

// handling runs fn as the body of an `except` clause catching err.
func (in *Interpreter) handling(err error, fn func() error) error {
	in.excInfo = append(in.excInfo, asExc(err))
	defer func() { in.excInfo = in.excInfo[:len(in.excInfo)-1] }()
	return fn()
}

func strp(s string) *string { return &s }

// newInterpreter is Interpreter.__init__.
func newInterpreter(o Options) *Interpreter {
	in := &Interpreter{config: map[string]bool{"import": false, "importfrom": false}}
	for _, k := range configurable {
		in.config[k] = !o.Minimal
	}
	for k, v := range o.Config {
		in.config[k] = v
	}
	in.config["nested_symtable"] = o.NestedSymtable
	for _, f := range o.NodeFlags {
		switch {
		case strings.HasPrefix(f.Name, "no_") && contains(allNodes, f.Name[3:]):
			in.config[f.Name[3:]] = !f.Value
		case strings.HasPrefix(f.Name, "with_") && contains(allNodes, f.Name[5:]):
			in.config[f.Name[5:]] = f.Value
		}
	}
	in.writer, in.errWriter = o.Writer, o.ErrWriter
	if in.writer == nil {
		in.writer = os.Stdout
	}
	if in.errWriter == nil {
		in.errWriter = os.Stderr
	}
	in.maxStatementLength = max(1, min(100000000, o.MaxStatementLength))
	if o.Symtable != nil {
		in.symtable = o.Symtable
	} else {
		in.symtable = makeSymbolTable(o.NestedSymtable, true, o.UserSymbols)
	}
	in.printer = &callable{name: "_printer", typ: "method", qual: "Interpreter._printer",
		self: &opaqueV{typ: "Interpreter", qual: "asteval.asteval.Interpreter"}, fn: in.printFn}
	_ = symSet(in.symtable, "print", in.printer)
	in.handlers = map[string]handler{}
	for _, name := range allNodes {
		var h handler = in.unimplemented
		enabled, set := in.config[name]
		if !set || enabled {
			if fn := in.nodeHandler(name); fn != nil {
				h = fn
			}
		}
		in.handlers[name] = h
	}
	in.allowUnsafeModules = in.config["import"]
	in.handlers["tryexcept"] = in.handlers["try"]
	in.handlers["tryfinally"] = in.handlers["try"]
	in.readonly = map[string]bool{}
	for _, s := range o.ReadonlySymbols {
		in.readonly[s] = true
	}
	if o.BuiltinsReadonly {
		for _, k := range symKeys(in.symtable) {
			if s, ok := k.(strV); ok {
				in.readonly[string(s)] = true
			}
		}
	}
	keys, vals := symItems(in.symtable)
	for i, k := range keys {
		if isCallable(vals[i]) {
			if s, ok := k.(strV); ok {
				in.noDeepcopy = append(in.noDeepcopy, string(s))
			}
		}
	}
	return in
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (in *Interpreter) nodeHandler(name string) handler {
	switch name {
	case "expr":
		return in.onExpr
	case "import", "importfrom":
		return in.onImport
	case "return":
		return in.onReturn
	case "module":
		return in.onModule
	case "pass":
		return func(*node) (Value, error) { return none, nil }
	case "interrupt", "break", "continue":
		return in.onInterrupt
	case "assert":
		return in.onAssert
	case "list":
		return in.onList
	case "tuple":
		return in.onTuple
	case "set":
		return in.onSet
	case "dict":
		return in.onDict
	case "constant":
		return func(n *node) (Value, error) { return n.value, nil }
	case "joinedstr":
		return in.onJoinedStr
	case "formattedvalue":
		return in.onFormattedValue
	case "name":
		return in.onName
	case "attribute":
		return in.onAttribute
	case "assign":
		return in.onAssign
	case "augassign":
		return in.onAugAssign
	case "slice":
		return in.onSlice
	case "subscript":
		return in.onSubscript
	case "delete":
		return in.onDelete
	case "unaryop":
		return in.onUnaryOp
	case "binop":
		return in.onBinOp
	case "boolop":
		return in.onBoolOp
	case "compare":
		return in.onCompare
	case "if":
		return in.onIf
	case "ifexp":
		return in.onIfExp
	case "while":
		return in.onWhile
	case "for":
		return in.onFor
	case "with":
		return in.onWith
	case "listcomp":
		return in.onListComp
	case "setcomp":
		return in.onSetComp
	case "dictcomp":
		return in.onDictComp
	case "excepthandler":
		return in.onExceptHandler
	case "try":
		return in.onTry
	case "raise":
		return in.onRaise
	case "call":
		return in.onCall
	case "arg":
		return func(n *node) (Value, error) { return strV(n.id), nil }
	case "functiondef":
		return func(n *node) (Value, error) { return in.onFunctionDef(n, false) }
	case "lambda":
		return func(n *node) (Value, error) { return in.onFunctionDef(n, true) }
	}
	return nil
}

// unimplemented is the handler of a disabled node.
func (in *Interpreter) unimplemented(n *node) (Value, error) {
	return nil, in.raiseException(n, "NotImplementedError", n.kind+" not supported", nil, nil)
}

// raiseException is Interpreter.raise_exception: it records an
// ExceptionHolder and returns the exception to raise. exc "" is None.
func (in *Interpreter) raiseException(n *node, exc, msg string, expr *string, lineno *int) error {
	if expr != nil {
		in.expr = expr
	}
	text := in.expr
	if len(in.codeText) > 0 {
		text = strp(in.codeText[len(in.codeText)-1])
	}
	err := newHolder(n, exc, msg, in.expr, text, lineno, in.currentExc())
	in.interrupt = &node{kind: "Raise"}
	in.errors = append(in.errors, err)
	if in.errorMsg == nil {
		in.errorMsg = strp(msg)
	}
	if exc == "" {
		exc = in.errors[len(in.errors)-1].exc
		for exc == "" && len(in.errors) > 0 {
			err = in.errors[len(in.errors)-1]
			in.errors = in.errors[:len(in.errors)-1]
			exc = err.exc
		}
	}
	if exc == "" {
		exc = "Exception"
	}
	if len(err.msg) == 0 && len(*in.errorMsg) == 0 && len(in.errors) > 1 {
		err = in.errors[len(in.errors)-1]
		in.errors = in.errors[:len(in.errors)-1]
		return newExc(err.exc, []Value{strV(err.msg)})
	}
	if len(err.msg) == 0 {
		err.msg = *in.errorMsg
	}
	return newExc(exc, []Value{strV(*in.errorMsg)})
}

// parse is Interpreter.parse.
func (in *Interpreter) parse(text string) (*node, error) {
	if runeLen(text) > in.maxStatementLength {
		msg := fmt.Sprintf("length of text exceeds %d characters", in.maxStatementLength)
		return nil, in.raiseException(nil, "RuntimeError", "", strp(msg), nil)
	}
	in.expr = strp(text)
	mod, serr := parseModule(text)
	if serr != nil {
		class := "SyntaxError"
		if serr.indent {
			class = "IndentationError"
		}
		perr := newPyErr(class, fmt.Sprintf("%s (<unknown>, line %d)", serr.msg, serr.line))
		var raised error
		_ = in.handling(perr, func() error {
			raised = in.raiseException(nil, "SyntaxError", "", strp(text), nil)
			return nil
		})
		return nil, raised
	}
	return mod, nil
}

// run is Interpreter.run for a node (a nil node is None).
func (in *Interpreter) run(n *node, expr *string, lineno *int, withRaise bool) (Value, error) {
	if len(in.errors) > 0 {
		return none, nil
	}
	if in.retval != nil {
		return in.retval, nil
	}
	if in.interrupt != nil && (in.interrupt.kind == "Break" || in.interrupt.kind == "Continue") {
		return &astNodeV{n: in.interrupt}, nil
	}
	if n == nil {
		return none, nil
	}
	if lineno != nil {
		in.lineno = *lineno
	}
	if expr != nil {
		in.expr = expr
		in.codeText = append(in.codeText, *expr)
	}
	h, ok := in.handlers[strings.ToLower(n.kind)]
	if !ok {
		var raised error
		_ = in.handling(newExc("KeyError", []Value{strV(strings.ToLower(n.kind))}), func() error {
			raised = in.raiseException(nil, "NotImplementedError", "", in.expr, nil)
			return nil
		})
		return nil, raised
	}
	ret, err := h(n)
	if err == nil {
		if it, ok := ret.(*iterObj); ok && it.typ == "enumerate" {
			items, err := toSlice(it)
			if err != nil {
				return nil, err
			}
			ret = newList(items)
		}
		return ret, nil
	}
	if !subclass(asExc(err).Class, "Exception") {
		var raised error
		_ = in.handling(err, func() error {
			raised = in.raiseException(n, "RuntimeError", noRaise+" "+asExc(err).Class, nil, nil)
			return nil
		})
		return nil, raised
	}
	if withRaise && in.expr != nil {
		var raised error
		_ = in.handling(err, func() error {
			raised = in.raiseException(n, "", "", in.expr, nil)
			return nil
		})
		return nil, raised
	}
	if len(in.errors) > 2 {
		in.removeDuplicateErrors()
	}
	return none, nil
}

// runText is Interpreter.run for a str: eval with raise_errors=with_raise.
func (in *Interpreter) runText(text string, withRaise bool) (Value, error) {
	return in.eval(text, 0, true, withRaise)
}

func (in *Interpreter) runNode(n *node) (Value, error) { return in.run(n, nil, nil, true) }

// removeDuplicateErrors is Interpreter._remove_duplicate_errors.
func (in *Interpreter) removeDuplicateErrors() {
	out := []*ExceptionHolder{in.errors[0]}
	for _, err := range in.errors[1:] {
		l := out[len(out)-1]
		if (err.exc != l.exc || !sameStrPtr(err.expr, l.expr) || err.msg != l.msg) && len(err.msg) > 0 {
			out = append(out, err)
		}
	}
	in.errors = out
}

func sameStrPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// eval is Interpreter.eval.
func (in *Interpreter) eval(expr string, lineno int, showErrors, raiseErrors bool) (Value, error) {
	in.lineno = lineno
	in.errors = nil
	in.errorMsg = nil
	tree, err := in.parse(expr)
	if err != nil {
		var out error
		_ = in.handling(err, func() error {
			errmsg := asExc(err).Message
			if len(in.errors) > 0 {
				lerr := in.errors[len(in.errors)-1]
				errmsg = lerr.getErrorText()
				if raiseErrors {
					out = newExc(lerr.excClass(), []Value{strV(errmsg)})
					return nil
				}
			}
			if showErrors {
				in.writeLine(in.errWriter, errmsg)
			}
			return nil
		})
		return none, out
	}
	ret, err := in.run(tree, strp(expr), &lineno, raiseErrors)
	if err == nil {
		return ret, nil
	}
	if showErrors && !raiseErrors {
		_ = in.handling(err, func() error {
			errmsg := asExc(err).Message
			if len(in.errors) > 0 {
				errmsg = in.errors[len(in.errors)-1].getErrorText()
			}
			in.writeLine(in.errWriter, errmsg)
			return nil
		})
	}
	if raiseErrors && len(in.errors) > 0 {
		in.removeDuplicateErrors()
		e := in.errors[len(in.errors)-1]
		return none, newExc(e.excClass(), []Value{strV(e.getErrorText())})
	}
	return none, nil
}

func (in *Interpreter) writeLine(w io.Writer, s string) {
	_, _ = io.WriteString(w, s+"\n")
}

// ---------------------------------------------------------------- handlers

func (in *Interpreter) onExpr(n *node) (Value, error) { return in.runNode(n.a) }

func (in *Interpreter) onImport(n *node) (Value, error) {
	// Importing real modules is not part of the port: with import enabled it
	// fails as an import of a module that cannot be found does.
	return nil, in.raiseException(nil, "ImportError", "Import Error", nil, nil)
}

func (in *Interpreter) onReturn(n *node) (Value, error) {
	if in.calldepth == 0 {
		return nil, newPyErr("SyntaxError", "cannot return at top level")
	}
	v, err := in.runNode(n.a)
	if err != nil {
		return nil, err
	}
	in.retval = v
	if _, isNone := v.(noneV); isNone {
		in.retval = returnedNone
	}
	in.interrupt = n
	return none, nil
}

func (in *Interpreter) onModule(n *node) (Value, error) {
	var out Value = none
	for _, t := range n.body {
		v, err := in.runNode(t)
		if err != nil {
			return nil, err
		}
		out = v
	}
	return out, nil
}

func (in *Interpreter) onInterrupt(n *node) (Value, error) {
	in.interrupt = n
	return &astNodeV{n: n}, nil
}

func (in *Interpreter) onAssert(n *node) (Value, error) {
	test, err := in.runNode(n.a)
	if err != nil {
		return nil, err
	}
	ok, err := truthy(test)
	if err != nil {
		return nil, err
	}
	if !ok {
		msg := ""
		if n.b != nil {
			// node.msg.value: only a Constant has one.
			if n.b.kind != "Constant" {
				return nil, newPyErr("AttributeError", "'"+n.b.kind+"' object has no attribute 'value'")
			}
			s, err := pyStr(n.b.value)
			if err != nil {
				return nil, err
			}
			msg = s
		}
		return nil, in.raiseException(n, "AssertionError", msg, nil, nil)
	}
	return trueV, nil
}

func (in *Interpreter) runAll(nodes []*node) ([]Value, error) {
	out := make([]Value, 0, len(nodes))
	for _, e := range nodes {
		v, err := in.runNode(e)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (in *Interpreter) onList(n *node) (Value, error) {
	items, err := in.runAll(n.l1)
	if err != nil {
		return nil, err
	}
	return newList(items), nil
}

func (in *Interpreter) onTuple(n *node) (Value, error) {
	items, err := in.runAll(n.l1)
	if err != nil {
		return nil, err
	}
	return newTuple(items), nil
}

func (in *Interpreter) onSet(n *node) (Value, error) {
	s := newSet(false)
	for _, e := range n.l1 {
		v, err := in.runNode(e)
		if err != nil {
			return nil, err
		}
		if err := s.add(v); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (in *Interpreter) onDict(n *node) (Value, error) {
	d := newDict()
	for i := range n.l1 {
		k, err := in.runNode(n.l1[i])
		if err != nil {
			return nil, err
		}
		v, err := in.runNode(n.l2[i])
		if err != nil {
			return nil, err
		}
		if err := d.set(k, v); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (in *Interpreter) onJoinedStr(n *node) (Value, error) {
	var b strings.Builder
	for _, k := range n.l1 {
		v, err := in.runNode(k)
		if err != nil {
			return nil, err
		}
		s, ok := v.(strV)
		if !ok {
			return nil, typeErr("sequence item %d: expected str instance, %s found", 0, v.pyType())
		}
		b.WriteString(string(s))
	}
	return strV(b.String()), nil
}

func (in *Interpreter) onFormattedValue(n *node) (Value, error) {
	val, err := in.runNode(n.a)
	if err != nil {
		return nil, err
	}
	switch n.conversion {
	case 115:
		s, err := pyStr(val)
		if err != nil {
			return nil, err
		}
		val = strV(s)
	case 114:
		s, err := pyRepr(val)
		if err != nil {
			return nil, err
		}
		val = strV(s)
	case 97:
		s, err := fmtASCII(val)
		if err != nil {
			return nil, err
		}
		val = strV(s)
	}
	format := "{__fstring__}"
	if n.b != nil {
		spec, err := in.runNode(n.b)
		if err != nil {
			return nil, err
		}
		ss, err := pyStr(spec)
		if err != nil {
			return nil, err
		}
		format = "{__fstring__:" + ss + "}"
	}
	return in.safeFormat(format, n, val)
}

func (in *Interpreter) getsym(n *node) (Value, error) {
	val, err := symGet(in.symtable, n.id, returnedNone)
	if err != nil {
		return nil, err
	}
	if _, empty := val.(emptyV); empty {
		return nil, in.raiseException(n, "NameError", "name '"+n.id+"' is not defined", nil, nil)
	}
	return val, nil
}

func (in *Interpreter) onName(n *node) (Value, error) {
	if n.ctx == "Del" {
		return strV(n.id), nil
	}
	return in.getsym(n)
}

// nodeAssign is Interpreter.node_assign.
func (in *Interpreter) nodeAssign(n *node, val Value) error {
	switch n.kind {
	case "Name":
		if !validSymbolName(n.id) || in.readonly[n.id] {
			return in.raiseException(n, "NameError", "invalid symbol name (reserved word?) "+n.id, nil, nil)
		}
		if err := symSet(in.symtable, n.id, val); err != nil {
			return err
		}
		in.dropNoDeepcopy(n.id)
	case "Attribute":
		if n.ctx == "Load" {
			return in.raiseException(n, "AttributeError", "cannot assign to attribute "+n.id, nil, nil)
		}
		obj, err := in.runNode(n.a)
		if err != nil {
			return err
		}
		return in.safeSetattr(obj, n.id, val, n)
	case "Subscript":
		nslice, err := in.runNode(n.b)
		if err != nil {
			return err
		}
		if isHashable(nslice) || isSlice(nslice) {
			obj, err := in.runNode(n.a)
			if err != nil {
				return err
			}
			return pySetitem(obj, nslice, val)
		}
		return in.raiseException(n.b, "TypeError", "unhashable type "+typeRepr(nslice), nil, nil)
	case "Tuple", "List":
		l, err := pyLen(val)
		if err != nil {
			return err
		}
		if l != len(n.l1) {
			return newPyErr("ValueError", "too many values to unpack")
		}
		items, err := toSlice(val)
		if err != nil {
			return err
		}
		for i, t := range n.l1 {
			if i >= len(items) {
				break
			}
			if err := in.nodeAssign(t, items[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (in *Interpreter) dropNoDeepcopy(name string) {
	for i, s := range in.noDeepcopy {
		if s == name {
			in.noDeepcopy = append(in.noDeepcopy[:i:i], in.noDeepcopy[i+1:]...)
			return
		}
	}
}

func isHashable(v Value) bool {
	_, err := pyHash(v)
	return err == nil
}

func isSlice(v Value) bool {
	_, ok := v.(*sliceV)
	return ok
}

func (in *Interpreter) onAttribute(n *node) (Value, error) {
	if n.ctx == "Store" {
		return nil, in.raiseException(n, "RuntimeError", "attribute for storage: shouldn't be here!", nil, nil)
	}
	sym, err := in.runNode(n.a)
	if err != nil {
		return nil, err
	}
	if n.ctx == "Del" {
		return none, pyDelattr(sym, n.id)
	}
	return in.safeGetattr(sym, n.id, n, in.allowUnsafeModules)
}

func (in *Interpreter) onAssign(n *node) (Value, error) {
	val, err := in.runNode(n.a)
	if err != nil {
		return nil, err
	}
	for _, t := range n.l1 {
		if err := in.nodeAssign(t, val); err != nil {
			return nil, err
		}
	}
	return none, nil
}

func (in *Interpreter) onAugAssign(n *node) (Value, error) {
	cur, err := in.runNode(n.a)
	if err != nil {
		return nil, err
	}
	rhs, err := in.runNode(n.b)
	if err != nil {
		return nil, err
	}
	v, err := safeBinop(n.op, cur, rhs)
	if err != nil {
		return nil, err
	}
	return none, in.nodeAssign(n.a, v)
}

func (in *Interpreter) onSlice(n *node) (Value, error) {
	parts, err := in.runAll([]*node{n.a, n.b, n.c})
	if err != nil {
		return nil, err
	}
	return &sliceV{start: parts[0], stop: parts[1], step: parts[2]}, nil
}

func (in *Interpreter) onSubscript(n *node) (Value, error) {
	mkey, err := in.runNode(n.b)
	if err != nil {
		return nil, err
	}
	if isHashable(mkey) || isSlice(mkey) {
		obj, err := in.runNode(n.a)
		if err != nil {
			return nil, err
		}
		return pyGetitem(obj, mkey)
	}
	return nil, in.raiseException(n.b, "TypeError", "unhashable type "+typeRepr(mkey), nil, nil)
}

func (in *Interpreter) onDelete(n *node) (Value, error) {
	// asteval prints this debugging line to the process's standard output.
	fmt.Fprintln(os.Stdout, "on delete, ", "<ast.Delete object at "+objAddr(n)+">")
	for _, t := range n.l1 {
		if t.ctx != "Del" {
			break
		}
		var children []string
		for t.kind == "Attribute" {
			children = append(children, t.id)
			t = t.a
		}
		switch {
		case t.kind == "Name" && !in.readonly[t.id]:
			children = append(children, t.id)
			reverseStrings(children)
			if _, err := symPop(in.symtable, strings.Join(children, ".")); err != nil {
				return nil, err
			}
		case t.kind == "Subscript":
			nslice, err := in.runNode(t.b)
			if err != nil {
				return nil, err
			}
			children = nil
			t = t.a
			for t.kind == "Attribute" {
				children = append(children, t.id)
				t = t.a
			}
			if t.kind == "Name" && !in.readonly[t.id] {
				children = append(children, t.id)
				reverseStrings(children)
				sname := strings.Join(children, ".")
				val, err := in.runText(sname, true)
				if err != nil {
					return nil, err
				}
				if isHashable(nslice) || isSlice(nslice) {
					if err := pyDelitem(val, nslice); err != nil {
						return nil, err
					}
				} else {
					return nil, newPyErr("AttributeError", "'Delete' object has no attribute 'slice'")
				}
				if len(children) == 1 {
					if err := symSet(in.symtable, sname, val); err != nil {
						return nil, err
					}
				} else {
					child, err := symGetitem(in.symtable, children[0])
					if err != nil {
						return nil, err
					}
					for _, cname := range children[1 : len(children)-1] {
						if child, err = pyGetitem(child, strV(cname)); err != nil {
							return nil, err
						}
					}
					if err := pySetattr(child, children[len(children)-1], val); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return none, nil
}

func reverseStrings(a []string) {
	for i, j := 0, len(a)-1; i < j; i, j = i+1, j-1 {
		a[i], a[j] = a[j], a[i]
	}
}

func (in *Interpreter) onUnaryOp(n *node) (Value, error) {
	v, err := in.runNode(n.a)
	if err != nil {
		return nil, err
	}
	return unop(n.op, v)
}

func (in *Interpreter) onBinOp(n *node) (Value, error) {
	l, err := in.runNode(n.a)
	if err != nil {
		return nil, err
	}
	r, err := in.runNode(n.b)
	if err != nil {
		return nil, err
	}
	return safeBinop(n.op, l, r)
}

func (in *Interpreter) onBoolOp(n *node) (Value, error) {
	val, err := in.runNode(n.l1[0])
	if err != nil {
		return nil, err
	}
	isAnd := n.op == "And"
	t, err := truthy(val)
	if err != nil {
		return nil, err
	}
	if isAnd == t {
		for _, e := range n.l1[1:] {
			v, err := in.runNode(e)
			if err != nil {
				return nil, err
			}
			// op2func(op)(val, v): `val and v` or `val or v`
			if tv, err := truthy(val); err != nil {
				return nil, err
			} else if tv == isAnd {
				val = v
			}
			tv, err := truthy(val)
			if err != nil {
				return nil, err
			}
			if tv != isAnd {
				break
			}
		}
	}
	return val, nil
}

// compareOp is OPERATORS for the comparison operators.
func compareOp(op string, a, b Value) (Value, error) {
	switch op {
	case "Is":
		return pyBool(sameObject(a, b)), nil
	case "IsNot":
		return pyBool(!sameObject(a, b)), nil
	case "In", "NotIn":
		in, err := pyContains(b, a)
		if err != nil {
			return nil, err
		}
		return pyBool(in == (op == "In")), nil
	}
	sym := map[string]string{"Eq": "==", "NotEq": "!=", "Lt": "<", "LtE": "<=", "Gt": ">", "GtE": ">="}[op]
	r, err := pyCompare(sym, a, b)
	return pyBool(r), err
}

func (in *Interpreter) onCompare(n *node) (Value, error) {
	lval, err := in.runNode(n.a)
	if err != nil {
		return nil, err
	}
	var results []Value
	multi := len(n.ops) > 1
	var ret Value
	for i, op := range n.ops {
		rval, err := in.runNode(n.l1[i])
		if err != nil {
			return nil, err
		}
		ret, err = compareOp(op, lval, rval)
		if err != nil {
			return nil, err
		}
		if multi {
			results = append(results, ret)
			for _, r := range results {
				if t, _ := truthy(r); !t {
					return falseV, nil
				}
			}
			lval = rval
		}
	}
	if multi {
		return trueV, nil
	}
	return ret, nil
}

// printFn is Interpreter._printer.
func (in *Interpreter) printFn(_ *callCtx, args []Value, kw *dictV) (Value, error) {
	enabled, set := in.config["print"]
	if set && !enabled {
		return none, nil
	}
	kw = kw.copy()
	pop := func(name string, def Value) Value {
		if v, ok := kw.getStr(name); ok {
			_, _ = kw.del(strV(name))
			return v
		}
		return def
	}
	pop("flush", trueV)
	fileh := pop("file", nil)
	sep := pop("sep", strV(" "))
	end := pop("end", strV("\n"))
	sepS, endS := " ", "\n"
	switch s := sep.(type) {
	case strV:
		sepS = string(s)
	case noneV:
	default:
		return nil, typeErr("sep must be None or a string, not %s", sep.pyType())
	}
	switch s := end.(type) {
	case strV:
		endS = string(s)
	case noneV:
	default:
		return nil, typeErr("end must be None or a string, not %s", end.pyType())
	}
	w := in.writer
	if fileh != nil {
		if _, isNone := fileh.(noneV); isNone {
			w = os.Stdout
		} else {
			return nil, newPyErr("AttributeError", "'"+fileh.pyType()+"' object has no attribute 'write'")
		}
	}
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteString(sepS)
		}
		s, err := pyStr(a)
		if err != nil {
			return nil, err
		}
		b.WriteString(s)
	}
	b.WriteString(endS)
	_, _ = io.WriteString(w, b.String())
	return none, nil
}

func (in *Interpreter) runBlock(block []*node) error {
	for _, t := range block {
		if _, err := in.runNode(t); err != nil {
			return err
		}
	}
	return nil
}

func (in *Interpreter) runTruth(n *node) (bool, error) {
	v, err := in.runNode(n)
	if err != nil {
		return false, err
	}
	return truthy(v)
}

func (in *Interpreter) onIf(n *node) (Value, error) {
	block := n.body
	t, err := in.runTruth(n.a)
	if err != nil {
		return nil, err
	}
	if !t {
		block = n.orelse
	}
	return none, in.runBlock(block)
}

func (in *Interpreter) onIfExp(n *node) (Value, error) {
	expr := n.c
	t, err := in.runTruth(n.a)
	if err != nil {
		return nil, err
	}
	if t {
		expr = n.b
	}
	return in.runNode(expr)
}

func (in *Interpreter) breaksLoop() bool {
	return in.interrupt != nil && (in.interrupt.kind == "Break" || in.interrupt.kind == "Return")
}

// loopBody runs a loop body until an interrupt is set.
func (in *Interpreter) loopBody(body []*node) error {
	in.interrupt = nil
	for _, t := range body {
		if _, err := in.runNode(t); err != nil {
			return err
		}
		if in.interrupt != nil {
			break
		}
	}
	return nil
}

func (in *Interpreter) onWhile(n *node) (Value, error) {
	broke := false
	for {
		t, err := in.runTruth(n.a)
		if err != nil {
			return nil, err
		}
		if !t {
			break
		}
		if err := in.loopBody(n.body); err != nil {
			return nil, err
		}
		if in.breaksLoop() {
			broke = true
			break
		}
	}
	if !broke {
		if err := in.runBlock(n.orelse); err != nil {
			return nil, err
		}
	}
	in.interrupt = nil
	return none, nil
}

func (in *Interpreter) onFor(n *node) (Value, error) {
	iterable, err := in.runNode(n.b)
	if err != nil {
		return nil, err
	}
	it, err := pyIter(iterable)
	if err != nil {
		return nil, err
	}
	broke := false
	for {
		val, ok, err := it.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		if err := in.nodeAssign(n.a, val); err != nil {
			return nil, err
		}
		if err := in.loopBody(n.body); err != nil {
			return nil, err
		}
		if in.breaksLoop() {
			broke = true
			break
		}
	}
	if !broke {
		if err := in.runBlock(n.orelse); err != nil {
			return nil, err
		}
	}
	in.interrupt = nil
	return none, nil
}

func (in *Interpreter) onWith(n *node) (Value, error) {
	for _, item := range n.l1 {
		ctx, err := in.runNode(item.a)
		if err != nil {
			return nil, err
		}
		// No value the port supports has __enter__.
		return nil, typeErr("'%s' object does not support the context manager protocol", typeRepr(ctx))
	}
	for _, b := range n.body {
		if _, err := in.runNode(b); err != nil {
			return nil, err
		}
		if in.interrupt != nil {
			break
		}
	}
	return none, nil
}

// extractNames is Interpreter._extract_names_from_target.
func extractNames(t *node) []string {
	switch t.kind {
	case "Name":
		return []string{t.id}
	case "Tuple":
		var out []string
		for _, e := range t.l1 {
			out = append(out, extractNames(e)...)
		}
		return out
	}
	return nil
}

// unpackToTarget is _unpack_to_target over _target_to_structure: names
// get the value; a tuple target zips with the value's items; anything else
// (a list or starred target) assigns nothing.
func (in *Interpreter) unpackToTarget(t *node, value Value) error {
	switch t.kind {
	case "Name":
		return symSet(in.symtable, t.id, value)
	case "Tuple":
		it, err := pyIter(value)
		if err != nil {
			return err
		}
		for _, e := range t.l1 {
			v, ok, err := it.next()
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
			if err := in.unpackToTarget(e, v); err != nil {
				return err
			}
		}
	}
	return nil
}

type savedSym struct {
	name string
	val  Value
}

// compSaveSyms is Interpreter._comp_save_syms.
func (in *Interpreter) compSaveSyms(gens []*node) ([]savedSym, []string, error) {
	var saved []savedSym
	var deleted []string
	for _, g := range gens {
		for _, name := range extractNames(g.a) {
			if !validSymbolName(name) || in.readonly[name] {
				return nil, nil, in.raiseException(g.a, "NameError", "invalid symbol name (reserved word?) "+name, nil, nil)
			}
			if has, err := symContains(in.symtable, name); err != nil {
				return nil, nil, err
			} else if has {
				v, err := symGet(in.symtable, name, none)
				if err != nil {
					return nil, nil, err
				}
				cp, err := deepcopy(v)
				if err != nil {
					return nil, nil, err
				}
				found := false
				for i := range saved {
					if saved[i].name == name {
						saved[i].val, found = cp, true
					}
				}
				if !found {
					saved = append(saved, savedSym{name, cp})
				}
			} else if !contains(deleted, name) {
				deleted = append(deleted, name)
			}
		}
	}
	return saved, deleted, nil
}

// doGenerator is Interpreter.do_generator.
func (in *Interpreter) doGenerator(gens []*node, n *node, outList *[]Value, outDict *dictV) error {
	g := gens[0]
	for _, name := range extractNames(g.a) {
		if !validSymbolName(name) || in.readonly[name] {
			return in.raiseException(g.a, "NameError", "invalid symbol name (reserved word?) "+name, nil, nil)
		}
	}
	iterable, err := in.runNode(g.b)
	if err != nil {
		return err
	}
	it, err := pyIter(iterable)
	if err != nil {
		return err
	}
	for {
		val, ok, err := it.next()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := in.unpackToTarget(g.a, val); err != nil {
			return err
		}
		add := true
		for _, cond := range g.l1 {
			v, err := in.runNode(cond)
			if err != nil {
				return err
			}
			t, err := truthy(v)
			if err != nil {
				return err
			}
			add = add && t
			if !add {
				break
			}
		}
		if !add {
			continue
		}
		switch {
		case len(gens) > 1:
			if err := in.doGenerator(gens[1:], n, outList, outDict); err != nil {
				return err
			}
		case outList != nil:
			v, err := in.runNode(n.a)
			if err != nil {
				return err
			}
			*outList = append(*outList, v)
		default:
			k, err := in.runNode(n.a)
			if err != nil {
				return err
			}
			if isHashable(k) || isSlice(k) {
				v, err := in.runNode(n.b)
				if err != nil {
					return err
				}
				if err := outDict.set(k, v); err != nil {
					return err
				}
			} else {
				return in.raiseException(n.a, "TypeError", "unhashable type "+typeRepr(k), nil, nil)
			}
		}
	}
}

func (in *Interpreter) restoreSyms(saved []savedSym, deleted []string) error {
	for _, s := range saved {
		if err := symSet(in.symtable, s.name, s.val); err != nil {
			return err
		}
	}
	for _, name := range deleted {
		if _, err := symPopDefault(in.symtable, name); err != nil {
			return err
		}
	}
	return nil
}

func (in *Interpreter) onListComp(n *node) (Value, error) {
	saved, deleted, err := in.compSaveSyms(n.l1)
	if err != nil {
		return nil, err
	}
	out := []Value{}
	if err := in.doGenerator(n.l1, n, &out, nil); err != nil {
		return nil, err
	}
	if err := in.restoreSyms(saved, deleted); err != nil {
		return nil, err
	}
	return newList(out), nil
}

func (in *Interpreter) onSetComp(n *node) (Value, error) {
	l, err := in.onListComp(n)
	if err != nil {
		return nil, err
	}
	s := newSet(false)
	if err := s.update(l); err != nil {
		return nil, err
	}
	return s, nil
}

func (in *Interpreter) onDictComp(n *node) (Value, error) {
	saved, deleted, err := in.compSaveSyms(n.l1)
	if err != nil {
		return nil, err
	}
	out := newDict()
	if err := in.doGenerator(n.l1, n, nil, out); err != nil {
		return nil, err
	}
	if err := in.restoreSyms(saved, deleted); err != nil {
		return nil, err
	}
	return out, nil
}

func (in *Interpreter) onExceptHandler(n *node) (Value, error) {
	t, err := in.runNode(n.a)
	if err != nil {
		return nil, err
	}
	var name Value = none
	if !n.noID {
		name = strV(n.id)
	}
	return newTuple([]Value{t, name, &opaqueV{typ: "list", qual: "list"}}), nil
}

func (in *Interpreter) onTry(n *node) (Value, error) {
	noErrors := true
	for _, t := range n.body {
		if _, err := in.run(t, nil, nil, false); err != nil {
			return nil, err
		}
		noErrors = noErrors && len(in.errors) == 0
		if len(in.errors) > 0 {
			info := in.errors[len(in.errors)-1].excInfo
			for _, hnd := range n.l1 {
				var htype Value
				if hnd.a != nil {
					if hnd.a.kind != "Name" {
						return nil, newPyErr("AttributeError", "'"+hnd.a.kind+"' object has no attribute 'id'")
					}
					htype = builtinClass(hnd.a.id)
				}
				// htype is None or isinstance(e_type(), htype): a fresh instance
				// of the class, which fails when no exception was being handled.
				if htype != nil {
					if info == nil {
						return nil, newPyErr("TypeError", "'NoneType' object is not callable")
					}
					match, err := isInstance(newExc(info.Class, nil), htype)
					if err != nil {
						return nil, err
					}
					if !match {
						continue
					}
				}
				in.errors = nil
				in.interrupt = nil
				if !hnd.noID {
					var bound Value = none
					if info != nil {
						bound = info
					}
					if err := symSet(in.symtable, hnd.id, bound); err != nil {
						return nil, err
					}
				}
				for _, line := range hnd.body {
					if _, err := in.runNode(line); err != nil {
						return nil, err
					}
				}
				break
			}
			break
		}
	}
	if noErrors {
		if err := in.runBlock(n.orelse); err != nil {
			return nil, err
		}
	}
	return none, in.runBlock(n.finalbody)
}

func (in *Interpreter) onRaise(n *node) (Value, error) {
	out, err := in.runNode(n.a)
	if err != nil {
		return nil, err
	}
	msg := " "
	switch x := out.(type) {
	case *excV:
		parts := make([]string, len(x.args))
		ok := true
		for i, a := range x.args {
			s, isStr := a.(strV)
			if !isStr {
				ok = false
				break
			}
			parts[i] = string(s)
		}
		if ok {
			msg = strings.Join(parts, " ")
		}
	case noneV:
		return nil, newPyErr("AttributeError", "'NoneType' object has no attribute 'args'")
	case *classV:
	default:
		return nil, newPyErr("AttributeError", "'"+out.pyType()+"' object has no attribute 'args'")
	}
	msg2, err := in.runNode(n.b)
	if err != nil {
		return nil, err
	}
	if s, isStr := msg2.(strV); !(isNoneV(msg2) || isStr && s == "None") {
		ms, ok := msg2.(strV)
		if !ok {
			return nil, typeErr("unsupported format string passed to %s.__format__", msg2.pyType())
		}
		msg = msg + ": " + string(ms)
	}
	if x, ok := out.(*excV); ok && subclass(x.Class, "Exception") {
		return nil, in.raiseException(nil, x.Class, msg, strp(""), nil)
	}
	if k, ok := out.(*classV); ok {
		return nil, in.raiseException(n, "RuntimeError", noRaise+" "+k.name, nil, nil)
	}
	return nil, newPyErr("AttributeError", "'"+out.pyType()+"' object has no attribute '__name__'")
}

func isNoneV(v Value) bool {
	_, ok := v.(noneV)
	return ok
}

// onCall is Interpreter.on_call.
func (in *Interpreter) onCall(n *node) (Value, error) {
	fn, err := in.runNode(n.a)
	if err != nil {
		return nil, err
	}
	if !isCallable(fn) {
		s, err := pyStr(fn)
		if err != nil {
			return nil, err
		}
		return nil, in.raiseException(n, "TypeError", "'"+s+"' is not callable!!", nil, nil)
	}
	args := []Value{}
	for _, a := range n.l1 {
		v, err := in.runNode(a)
		if err != nil {
			return nil, err
		}
		args = append(args, v)
	}
	keywords := newDict()
	for _, key := range n.l2 {
		if key.noID {
			v, err := in.runNode(key.a)
			if err != nil {
				return nil, err
			}
			if err := dictUpdate(keywords, v); err != nil {
				return nil, err
			}
			continue
		}
		if _, dup := keywords.getStr(key.id); dup {
			return nil, in.raiseException(n, "SyntaxError", "keyword argument repeated: "+key.id, nil, nil)
		}
		v, err := in.runNode(key.a)
		if err != nil {
			return nil, err
		}
		keywords.setStr(key.id, v)
	}
	if _, ok := fn.(*procV); ok {
		in.calldepth++
		defer func() { in.calldepth-- }()
	}
	var out Value
	if b, ok := fn.(*callable); ok && b.name == "dir" && b.self == nil && b.typ == "builtin_function_or_method" && len(args) == 0 && keywords.Len() == 0 {
		out = onCallLocals(len(n.l2) > 0)
	} else {
		out, err = callValue(&callCtx{in: in}, fn, args, keywords)
	}
	if err != nil {
		var raised error
		_ = in.handling(err, func() error {
			fname, ferr := funcName(fn)
			if ferr != nil {
				raised = ferr
				return nil
			}
			argsR, rerr := pyStr(newList(args))
			if rerr != nil {
				raised = rerr
				return nil
			}
			kwR, rerr := pyStr(keywords)
			if rerr != nil {
				raised = rerr
				return nil
			}
			msg := "Error running function '" + fname + "' with args '" + argsR + "' and kwargs " + kwR + ": " + asExc(err).Message
			raised = in.raiseException(n, "", msg, nil, nil)
			return nil
		})
		return nil, raised
	}
	return out, nil
}

// onCallLocals is dir() called with no arguments from on_call: the names
// local to on_call at that point, sorted.
func onCallLocals(sawKeyword bool) Value {
	names := []string{"args", "func", "keywords", "kwargs", "node", "self", "starargs"}
	if sawKeyword {
		names = append(names, "key")
	}
	sort.Strings(names)
	items := make([]Value, len(names))
	for i, s := range names {
		items[i] = strV(s)
	}
	return newList(items)
}

// funcName is getattr(func, '__name__', str(func)).
func funcName(fn Value) (string, error) {
	switch f := fn.(type) {
	case *callable:
		return f.name, nil
	case *classV:
		return f.name, nil
	case *procV:
		return f.name, nil
	}
	return pyStr(fn)
}

// dictUpdate is dict.update(other) for a mapping or an iterable of pairs.
func dictUpdate(d *dictV, other Value) error {
	switch o := other.(type) {
	case *dictV:
		for i, k := range o.keys {
			if err := d.set(k, o.vals[i]); err != nil {
				return err
			}
		}
		return nil
	case *groupV:
		return dictUpdate(d, o.d)
	}
	_, err := methodsDict["update"](d, []Value{other}, newDict())
	return err
}

// onFunctionDef is Interpreter.on_functiondef (and on_lambda).
func (in *Interpreter) onFunctionDef(n *node, isLambda bool) (Value, error) {
	var name string
	var body []*node
	if isLambda {
		name = "lambda"
		body = []*node{n.b}
	} else {
		name = n.id
		body = n.body
		if len(n.l1) > 0 {
			return nil, newPyErr("Warning", "decorated procedures not supported!")
		}
		if !validSymbolName(name) || in.readonly[name] {
			return nil, in.raiseException(n, "NameError", "invalid function name (reserved word?) "+name, nil, nil)
		}
	}
	args := n.a
	var kwargs []procKw
	offset := len(args.l2) - len(args.body)
	for idef, defnode := range args.body {
		defval, err := in.runNode(defnode)
		if err != nil {
			return nil, err
		}
		keyval, err := in.runNode(args.l2[idef+offset])
		if err != nil {
			return nil, err
		}
		ks, _ := keyval.(strV)
		kwargs = append(kwargs, procKw{name: string(ks), value: defval})
	}
	var argnames []string
	for _, a := range args.l2[:offset] {
		argnames = append(argnames, a.id)
	}
	var doc Value
	if nb0 := body[0]; nb0.kind == "Expr" && nb0.a.kind == "Constant" {
		doc = nb0.a.value
	}
	p := &procV{name: name, in: in, doc: doc, lineno: in.lineno, body: body, text: in.sourceText(n),
		argnames: argnames, kwargs: kwargs, isLambda: isLambda}
	if args.a != nil {
		p.vararg = strp(args.a.id)
	}
	if args.b != nil {
		p.varkws = strp(args.b.id)
	}
	if isLambda {
		return p, nil
	}
	if err := symSet(in.symtable, name, p); err != nil {
		return nil, err
	}
	in.dropNoDeepcopy(name)
	return none, nil
}

// sourceText is ast.unparse of a function definition or lambda. The port
// takes the definition's source text with the definition's indentation
// removed, which is what ast.unparse prints for a definition written in its
// canonical form.
func (in *Interpreter) sourceText(n *node) string {
	src := ""
	if n.src != nil {
		src = *n.src
	}
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	if n.lineno < 1 || n.endLineno > len(lines) {
		return ""
	}
	seg := lines[n.lineno-1 : n.endLineno]
	out := make([]string, len(seg))
	for i, l := range seg {
		if i == len(seg)-1 && n.endCol <= len(l) {
			l = l[:n.endCol]
		}
		if i == 0 {
			if n.col <= len(l) {
				l = l[n.col:]
			}
		} else if n.kind == "FunctionDef" {
			l = strings.TrimPrefix(l, strings.Repeat(" ", n.col))
		}
		out[i] = l
	}
	return strings.Join(out, "\n")
}

var _ = big.NewInt
