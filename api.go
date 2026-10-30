// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT

// Package asteval is a Go port of asteval: a safe(ish) evaluator of a subset
// of Python 3 that parses source text into a syntax tree and runs that tree
// with its own interpreter, against a symbol table of Python values.
package asteval

import (
	"io"
	"math/big"
)

// None returns Python's None.
func None() Value { return none }

// Bool returns True or False.
func Bool(b bool) Value { return pyBool(b) }

// Int returns an int.
func Int(i int64) Value { return mkInt(i) }

// BigInt returns an int of any size. The argument is copied.
func BigInt(i *big.Int) Value { return mkBig(new(big.Int).Set(i)) }

// Float returns a float.
func Float(f float64) Value { return floatV(f) }

// Complex returns a complex.
func Complex(c complex128) Value { return complexV(c) }

// String returns a str holding s, which must be valid UTF-8.
func String(s string) Value { return strV(s) }

// Bytes returns a bytes object. The argument is copied.
func Bytes(b []byte) Value { return bytesV(b) }

// List returns a new list holding the items.
func List(items ...Value) Value { return newList(append([]Value(nil), items...)) }

// Tuple returns a tuple holding the items.
func Tuple(items ...Value) Value { return newTuple(append([]Value(nil), items...)) }

// NewDict returns a dict built as dict(zip(keys, values)) is, or the
// TypeError Python raises for an unhashable key.
func NewDict(keys, values []Value) (Value, error) {
	d := newDict()
	for i := range keys {
		if err := d.set(keys[i], values[i]); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// Repr returns Python's repr() of the value, or the exception repr() raises
// (a ValueError for an int of more than 4300 digits).
func Repr(v Value) (string, error) {
	s, err := pyRepr(v)
	return s, toException(err)
}

// Str returns Python's str() of the value, or the exception str() raises.
func Str(v Value) (string, error) {
	s, err := pyStr(v)
	return s, toException(err)
}

// TypeName returns type(v).__name__.
func TypeName(v Value) string { return typeName(v) }

// Symbol is one name and value of a symbol table.
type Symbol struct {
	Name  string
	Value Value
}

// NodeFlag is one of Interpreter's no_<node>= or with_<node>= keyword
// arguments: Name is the whole keyword, such as "no_if" or "with_while".
type NodeFlag struct {
	Name  string
	Value bool
}

// Options holds the arguments of asteval.Interpreter(...). Start from
// DefaultOptions, which gives asteval's defaults.
type Options struct {
	// Symtable is the symtable argument: a dict (from NewDict) or a Group
	// (from MakeSymbolTable) used as the symbol table itself; nil makes one
	// with make_symbol_table.
	Symtable Value
	// NestedSymtable is nested_symtable.
	NestedSymtable bool
	// UserSymbols is user_symbols, in order.
	UserSymbols []Symbol
	// Writer and ErrWriter are writer and err_writer; nil is the process's
	// standard output and standard error.
	Writer    io.Writer
	ErrWriter io.Writer
	// MaxStatementLength is max_statement_length.
	MaxStatementLength int
	// Minimal is minimal.
	Minimal bool
	// ReadonlySymbols is readonly_symbols; nil is None.
	ReadonlySymbols []string
	// BuiltinsReadonly is builtins_readonly.
	BuiltinsReadonly bool
	// Config is config: node names (and "print") mapped to whether they are
	// enabled; nil is None.
	Config map[string]bool
	// NodeFlags are the no_<node>= and with_<node>= keyword arguments, in
	// the order they are passed.
	NodeFlags []NodeFlag
}

// DefaultOptions returns the defaults of asteval.Interpreter(): every field
// zero except MaxStatementLength, which is 50000.
func DefaultOptions() Options { return Options{MaxStatementLength: 50000} }

// New is asteval.Interpreter(**options), with use_numpy=False.
func New(o Options) *Interpreter { return newInterpreter(o) }

// Tree is a parsed module: the result of Interpreter.parse.
type Tree struct{ n *node }

// Exception is a Python exception raised out of Eval or Run: Class is the
// name of its class and Msg its str().
type Exception struct {
	Class string
	Msg   string
}

// Error returns Class + ": " + Msg.
func (e *Exception) Error() string { return e.Class + ": " + e.Msg }

func toException(err error) error {
	if err == nil {
		return nil
	}
	x := asExc(err)
	return &Exception{Class: x.Class, Msg: x.Message}
}

// Eval is Interpreter.eval(expr, lineno, show_errors, raise_errors). The
// error is the exception eval raises (only ever with raiseErrors).
func (in *Interpreter) Eval(expr string, lineno int, showErrors, raiseErrors bool) (Value, error) {
	v, err := in.eval(expr, lineno, showErrors, raiseErrors)
	if err != nil {
		return nil, toException(err)
	}
	return v, nil
}

// Parse is Interpreter.parse(text): the tree, or the exception parse raises.
func (in *Interpreter) Parse(text string) (*Tree, error) {
	n, err := in.parse(text)
	if err != nil {
		return nil, toException(err)
	}
	return &Tree{n: n}, nil
}

// Run is Interpreter.run(tree, with_raise=withRaise): the value, or the
// exception run raises.
func (in *Interpreter) Run(t *Tree, withRaise bool) (Value, error) {
	var n *node
	if t != nil {
		n = t.n
	}
	v, err := in.run(n, nil, nil, withRaise)
	if err != nil {
		return nil, toException(err)
	}
	return v, nil
}

// Errors returns the interpreter's error list (Interpreter.error), oldest
// first.
func (in *Interpreter) Errors() []*ExceptionHolder {
	return append([]*ExceptionHolder(nil), in.errors...)
}

// Class returns the name of the exception class (ExceptionHolder.exc).
func (e *ExceptionHolder) Class() string { return e.excClass() }

// Msg returns ExceptionHolder.msg.
func (e *ExceptionHolder) Msg() string { return e.msg }

// GetError is ExceptionHolder.get_error(): the exception class name and the
// error text.
func (e *ExceptionHolder) GetError() (string, string) {
	name := e.exc
	if name == "" || name == "None" {
		name = "UnknownError"
	}
	return name, e.getErrorText()
}

// Symtable returns the interpreter's symbol table: a dict, or a Group when
// it is nested.
func (in *Interpreter) Symtable() Value { return in.symtable }

// Lookup returns symtable[name] and whether the table holds that key.
func (in *Interpreter) Lookup(name string) (Value, bool) {
	v, ok := symDict(in.symtable).getStr(name)
	return v, ok
}

// SetSymbol is symtable[name] = value (Group.__setitem__ for a Group).
func (in *Interpreter) SetSymbol(name string, value Value) error {
	return symSet(in.symtable, name, value)
}

// UserDefinedSymbols is Interpreter.user_defined_symbols(), sorted.
func (in *Interpreter) UserDefinedSymbols() []string {
	var out []string
	for _, k := range symKeys(in.symtable) {
		s, ok := k.(strV)
		if ok && !contains(in.noDeepcopy, string(s)) {
			out = append(out, string(s))
		}
	}
	sortStrings(out)
	return out
}

// MakeSymbolTable is make_symbol_table(use_numpy=False, nested=nested,
// **kws).
func MakeSymbolTable(nested bool, kws []Symbol) Value { return makeSymbolTable(nested, true, kws) }

// ValidSymbolName is valid_symbol_name(name).
func ValidSymbolName(name string) bool { return validSymbolName(name) }

// GetASTNames is get_ast_names(tree): the Name ids of the tree in the order
// ast.NodeVisitor visits them, each once.
func GetASTNames(t *Tree) []string {
	var names []string
	var visit func(n *node)
	visit = func(n *node) {
		for _, c := range n.children() {
			if c.kind == "Name" && !contains(names, c.id) {
				names = append(names, c.id)
			}
			visit(c)
		}
	}
	if t != nil && t.n != nil {
		visit(t.n)
	}
	return names
}
