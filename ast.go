// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT
//
// Go port of asteval. The syntax tree ast.parse builds: one node type whose
// kind is the CPython class name (asteval dispatches on its lower-cased
// name), with the fields of that class and its source positions.

package asteval

// node is an ast.AST. Which fields are used depends on kind; children
// returns them in the class's _fields order.
type node struct {
	kind string
	// Positions as CPython reports them: lines from 1, columns as UTF-8
	// byte offsets. hasPos is false for the classes without positions
	// (arguments, comprehension, withitem, and the operator and context
	// singletons).
	lineno, col, endLineno, endCol int
	hasPos                         bool
	// outer is the span of the tokens the expression was parsed from when
	// that differs from its own (parentheses around it): a parent node's
	// span starts and ends there, as CPython's rule spans do.
	outer *[4]int

	// id is Name.id, Attribute.attr, FunctionDef.name, arg.arg,
	// keyword.arg, alias.name, ExceptHandler.name and ImportFrom.module;
	// noID marks Python's None for the optional ones.
	id   string
	noID bool
	// asname is alias.asname (noAsname for None).
	asname   string
	noAsname bool
	// ctx is Load, Store or Del for the expression kinds that carry one.
	ctx string
	// op is the operator class of BinOp, UnaryOp, BoolOp and AugAssign.
	op string
	// ops are Compare's operators.
	ops []string
	// value is Constant.value.
	value Value
	// conversion is FormattedValue.conversion (-1 for none).
	conversion int
	// level is ImportFrom.level.
	level int

	// Single children, by role.
	a, b, c *node
	// List children, by role.
	l1, l2, l3, l4 []*node
	// body, orelse and finalbody of the compound statements; Module.body.
	body, orelse, finalbody []*node
	// src is the source a FunctionDef or Lambda was parsed from.
	src *string
}

// The roles of a, b, c and l1..l4 per kind:
//
//	Expr, Return, Starred, Await          a=value
//	Assign                                l1=targets a=value
//	AugAssign                             a=target b=value
//	AnnAssign                             a=target b=annotation c=value
//	Delete                                l1=targets
//	If, While                             a=test
//	For                                   a=target b=iter
//	With                                  l1=items
//	withitem                              a=context_expr b=optional_vars
//	Try                                   l1=handlers
//	ExceptHandler                         a=type
//	Raise                                 a=exc b=cause
//	Assert                                a=test b=msg
//	Import                                l1=names
//	ImportFrom                            l1=names
//	FunctionDef                           a=args l1=decorator_list b=returns
//	ClassDef                              l1=bases l2=keywords l3=decorator_list
//	Global, Nonlocal                      l1=names (alias nodes holding the identifiers)
//	arguments                             l1=posonlyargs l2=args a=vararg l3=kwonlyargs l4=kw_defaults b=kwarg; defaults in body
//	arg                                   a=annotation
//	Lambda                                a=args b=body
//	BoolOp                                l1=values
//	NamedExpr                             a=target b=value
//	BinOp                                 a=left b=right
//	UnaryOp                               a=operand
//	IfExp                                 a=test b=body c=orelse
//	Dict                                  l1=keys (nil for **) l2=values
//	Set, List, Tuple                      l1=elts
//	ListComp, SetComp, GeneratorExp       a=elt l1=generators
//	DictComp                              a=key b=value l1=generators
//	comprehension                         a=target b=iter l1=ifs
//	Compare                               a=left l1=comparators
//	Call                                  a=func l1=args l2=keywords
//	keyword                               a=value
//	FormattedValue                        a=value b=format_spec
//	JoinedStr                             l1=values
//	Attribute                             a=value
//	Subscript                             a=value b=slice
//	Slice                                 a=lower b=upper c=step
//	Yield, YieldFrom                      a=value

// children returns the node's child nodes in _fields order, skipping None
// and the operator and context singletons (which hold no names).
func (n *node) children() []*node {
	var out []*node
	add := func(ns ...*node) {
		for _, c := range ns {
			if c != nil {
				out = append(out, c)
			}
		}
	}
	switch n.kind {
	case "Module":
		add(n.body...)
	case "Expr", "Return", "Starred", "Await", "Yield", "YieldFrom", "UnaryOp", "Attribute", "keyword", "arg":
		add(n.a)
	case "Assign":
		add(n.l1...)
		add(n.a)
	case "AugAssign", "NamedExpr", "withitem", "Raise", "Assert", "Subscript", "BinOp", "FormattedValue":
		add(n.a, n.b)
	case "AnnAssign", "Slice":
		add(n.a, n.b, n.c)
	case "Delete", "Set", "List", "Tuple", "BoolOp", "JoinedStr", "Import", "ImportFrom", "Global", "Nonlocal":
		add(n.l1...)
	case "If", "While":
		add(n.a)
		add(n.body...)
		add(n.orelse...)
	case "For":
		add(n.a, n.b)
		add(n.body...)
		add(n.orelse...)
	case "With":
		add(n.l1...)
		add(n.body...)
	case "Try":
		add(n.body...)
		add(n.l1...)
		add(n.orelse...)
		add(n.finalbody...)
	case "ExceptHandler":
		add(n.a)
		add(n.body...)
	case "FunctionDef":
		add(n.a)
		add(n.body...)
		add(n.l1...)
		add(n.b)
	case "ClassDef":
		add(n.l1...)
		add(n.l2...)
		add(n.body...)
		add(n.l3...)
	case "arguments":
		add(n.l1...)
		add(n.l2...)
		add(n.a)
		add(n.l3...)
		add(n.l4...)
		add(n.b)
		add(n.body...)
	case "Lambda":
		add(n.a, n.b)
	case "IfExp":
		add(n.a, n.b, n.c)
	case "Dict":
		add(n.l1...)
		add(n.l2...)
	case "ListComp", "SetComp", "GeneratorExp":
		add(n.a)
		add(n.l1...)
	case "DictComp":
		add(n.a, n.b)
		add(n.l1...)
	case "comprehension":
		add(n.a, n.b)
		add(n.l1...)
	case "Compare":
		add(n.a)
		add(n.l1...)
	case "Call":
		add(n.a)
		add(n.l1...)
		add(n.l2...)
	}
	return out
}

// startOf is where the tokens of an expression start, parentheses included.
func startOf(n *node) (int, int) {
	if n.outer != nil {
		return n.outer[0], n.outer[1]
	}
	return n.lineno, n.col
}

// endOf is where the tokens of an expression end, parentheses included.
func endOf(n *node) (int, int) {
	if n.outer != nil {
		return n.outer[2], n.outer[3]
	}
	return n.endLineno, n.endCol
}

// span gives n the span from the start of from's tokens to the end of to's.
func (n *node) span(from, to *node) *node {
	n.lineno, n.col = startOf(from)
	n.endLineno, n.endCol = endOf(to)
	n.hasPos = true
	return n
}

// isStmt reports whether the node is a statement.
func (n *node) isStmt() bool {
	switch n.kind {
	case "FunctionDef", "AsyncFunctionDef", "ClassDef", "Return", "Delete", "Assign", "AugAssign", "AnnAssign",
		"For", "AsyncFor", "While", "If", "With", "AsyncWith", "Match", "Raise", "Try", "TryStar", "Assert",
		"Import", "ImportFrom", "Global", "Nonlocal", "Expr", "Pass", "Break", "Continue":
		return true
	}
	return false
}
