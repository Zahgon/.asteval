// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT
//
// Go port of asteval. The parser half of ast.parse: a recursive-descent
// parser for the Python 3.12 grammar, building the tree CPython builds, with
// CPython's node positions. Only acceptance and the reported line of a
// syntax error follow CPython; its error wording is not reproduced.

package asteval

import (
	"math/big"
	"strconv"
	"strings"
)

type parser struct {
	toks []token
	p    int
	// tokErr is the tokenizer's error at the tkError token ending toks.
	tokErr *syntaxErr
	// lineOff and colOff place a parse of an f-string replacement field at
	// its position in the enclosing source: tokens on the field's first line
	// are shifted by colOff, every token by lineOff lines.
	lineOff, colOff int
	src             string
}

func (p *parser) fail(msg string, t token) *syntaxErr {
	if t.kind == tkError && p.tokErr != nil {
		return p.tokErr
	}
	return &syntaxErr{msg: msg, line: t.line}
}

func (p *parser) peek() token { return p.toks[p.p] }
func (p *parser) peekN(n int) token {
	if p.p+n < len(p.toks) {
		return p.toks[p.p+n]
	}
	return p.toks[len(p.toks)-1]
}
func (p *parser) advance() token {
	t := p.toks[p.p]
	if p.p < len(p.toks)-1 {
		p.p++
	}
	return t
}

func (p *parser) isOp(s string) bool {
	t := p.peek()
	return t.kind == tkOp && t.text == s
}

func (p *parser) isKw(s string) bool {
	t := p.peek()
	return t.kind == tkName && t.text == s
}

func (p *parser) acceptOp(s string) (token, bool) {
	if p.isOp(s) {
		return p.advance(), true
	}
	return token{}, false
}

func (p *parser) expectOp(s string) (token, *syntaxErr) {
	if p.isOp(s) {
		return p.advance(), nil
	}
	t := p.peek()
	if s == ":" {
		return t, p.fail("expected ':'", t)
	}
	return t, p.fail("invalid syntax", t)
}

var keywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true, "assert": true, "async": true,
	"await": true, "break": true, "class": true, "continue": true, "def": true, "del": true, "elif": true,
	"else": true, "except": true, "finally": true, "for": true, "from": true, "global": true, "if": true,
	"import": true, "in": true, "is": true, "lambda": true, "nonlocal": true, "not": true, "or": true,
	"pass": true, "raise": true, "return": true, "try": true, "while": true, "with": true, "yield": true,
}

func (p *parser) mk(kind string, t token) *node {
	return &node{kind: kind, lineno: t.line, col: t.col, endLineno: t.endLine, endCol: t.endCol, hasPos: true}
}

func endAt(n *node, t token) *node {
	n.endLineno, n.endCol = t.endLine, t.endCol
	return n
}

// parseModule is ast.parse(text): the module, or the syntax error.
func parseModule(src string) (*node, *syntaxErr) {
	toks, tokErr := tokenize(src)
	p := &parser{toks: toks, src: src, tokErr: tokErr}
	mod := &node{kind: "Module"}
	for p.peek().kind != tkEOF {
		stmts, err := p.statement()
		if err != nil {
			return nil, err
		}
		mod.body = append(mod.body, stmts...)
	}
	return mod, nil
}

// ---------------------------------------------------------------- statements

func (p *parser) statement() ([]*node, *syntaxErr) {
	t := p.peek()
	if t.kind == tkIndent {
		return nil, &syntaxErr{msg: "unexpected indent", line: t.line, indent: true}
	}
	if t.kind == tkName {
		switch t.text {
		case "if":
			s, err := p.ifStmt()
			return []*node{s}, err
		case "while":
			s, err := p.whileStmt()
			return []*node{s}, err
		case "for":
			s, err := p.forStmt()
			return []*node{s}, err
		case "try":
			s, err := p.tryStmt()
			return []*node{s}, err
		case "with":
			s, err := p.withStmt()
			return []*node{s}, err
		case "def":
			s, err := p.funcDef(nil)
			return []*node{s}, err
		case "async":
			if nt := p.peekN(1); nt.kind == tkName && nt.text == "def" {
				p.advance()
				s, err := p.funcDef(nil)
				if s != nil {
					s.kind = "AsyncFunctionDef"
					s.col = t.col
				}
				return []*node{s}, err
			}
			return nil, p.fail("invalid syntax", t)
		case "class":
			s, err := p.classDef(nil)
			return []*node{s}, err
		case "elif", "else", "except", "finally":
			return nil, p.fail("invalid syntax", t)
		}
	}
	if t.kind == tkOp && t.text == "@" {
		return p.decorated()
	}
	return p.simpleStmts()
}

func (p *parser) simpleStmts() ([]*node, *syntaxErr) {
	var out []*node
	for {
		s, err := p.simpleStmt()
		if err != nil {
			return nil, err
		}
		out = append(out, s)
		if _, ok := p.acceptOp(";"); !ok {
			break
		}
		if p.peek().kind == tkNewline {
			break
		}
	}
	t := p.peek()
	if t.kind != tkNewline {
		return nil, p.fail("invalid syntax", t)
	}
	p.advance()
	return out, nil
}

func (p *parser) simpleStmt() (*node, *syntaxErr) {
	t := p.peek()
	if t.kind == tkName {
		switch t.text {
		case "pass", "break", "continue":
			p.advance()
			return p.mk(strings.ToUpper(t.text[:1])+t.text[1:], t), nil
		case "return":
			p.advance()
			n := p.mk("Return", t)
			if !p.atStmtEnd() {
				v, err := p.starExpressions()
				if err != nil {
					return nil, err
				}
				n.a = v
				n.endLineno, n.endCol = endOf(v)
			}
			return n, nil
		case "del":
			return p.delStmt()
		case "assert":
			p.advance()
			n := p.mk("Assert", t)
			test, err := p.expression()
			if err != nil {
				return nil, err
			}
			n.a = test
			last := test
			if _, ok := p.acceptOp(","); ok {
				msg, err := p.expression()
				if err != nil {
					return nil, err
				}
				n.b, last = msg, msg
			}
			n.endLineno, n.endCol = endOf(last)
			return n, nil
		case "raise":
			p.advance()
			n := p.mk("Raise", t)
			if !p.atStmtEnd() {
				exc, err := p.expression()
				if err != nil {
					return nil, err
				}
				n.a = exc
				n.endLineno, n.endCol = endOf(exc)
				if p.isKw("from") {
					p.advance()
					cause, err := p.expression()
					if err != nil {
						return nil, err
					}
					n.b = cause
					n.endLineno, n.endCol = endOf(cause)
				}
			}
			return n, nil
		case "global", "nonlocal":
			p.advance()
			n := p.mk(strings.ToUpper(t.text[:1])+t.text[1:], t)
			for {
				nt := p.peek()
				if nt.kind != tkName || keywords[nt.text] {
					return nil, p.fail("invalid syntax", nt)
				}
				p.advance()
				n.l1 = append(n.l1, &node{kind: "alias", id: nt.text})
				endAt(n, nt)
				if _, ok := p.acceptOp(","); !ok {
					break
				}
			}
			return n, nil
		case "import":
			return p.importStmt()
		case "from":
			return p.fromImport()
		}
	}
	return p.exprStmt()
}

func (p *parser) atStmtEnd() bool {
	t := p.peek()
	return t.kind == tkNewline || t.kind == tkEOF || t.kind == tkOp && t.text == ";"
}

func (p *parser) delStmt() (*node, *syntaxErr) {
	t := p.advance()
	n := p.mk("Delete", t)
	for {
		e, err := p.bitwiseOr()
		if err != nil {
			return nil, err
		}
		if err := p.setCtx(e, "Del"); err != nil {
			return nil, err
		}
		n.l1 = append(n.l1, e)
		n.endLineno, n.endCol = endOf(e)
		if _, ok := p.acceptOp(","); !ok || p.atStmtEnd() {
			break
		}
	}
	return n, nil
}

func (p *parser) dottedName() (string, token, *syntaxErr) {
	t := p.peek()
	if t.kind != tkName || keywords[t.text] {
		return "", t, p.fail("invalid syntax", t)
	}
	p.advance()
	name, last := t.text, t
	for p.isOp(".") {
		p.advance()
		nt := p.peek()
		if nt.kind != tkName || keywords[nt.text] {
			return "", nt, p.fail("invalid syntax", nt)
		}
		p.advance()
		name += "." + nt.text
		last = nt
	}
	return name, last, nil
}

func (p *parser) importStmt() (*node, *syntaxErr) {
	t := p.advance()
	n := p.mk("Import", t)
	for {
		start := p.peek()
		name, last, err := p.dottedName()
		if err != nil {
			return nil, err
		}
		a := p.mk("alias", start)
		a.id, a.noAsname = name, true
		endAt(a, last)
		if p.isKw("as") {
			p.advance()
			at := p.peek()
			if at.kind != tkName || keywords[at.text] {
				return nil, p.fail("invalid syntax", at)
			}
			p.advance()
			a.asname, a.noAsname = at.text, false
			endAt(a, at)
		}
		n.l1 = append(n.l1, a)
		n.endLineno, n.endCol = endOf(a)
		if _, ok := p.acceptOp(","); !ok {
			break
		}
	}
	return n, nil
}

func (p *parser) fromImport() (*node, *syntaxErr) {
	t := p.advance()
	n := p.mk("ImportFrom", t)
	for p.isOp(".") || p.isOp("...") {
		dt := p.advance()
		n.level += len(dt.text)
	}
	if p.isKw("import") {
		n.noID = true
	} else {
		name, _, err := p.dottedName()
		if err != nil {
			return nil, err
		}
		n.id = name
	}
	if !p.isKw("import") {
		return nil, p.fail("invalid syntax", p.peek())
	}
	p.advance()
	if st, ok := p.acceptOp("*"); ok {
		a := p.mk("alias", st)
		a.id, a.noAsname = "*", true
		n.l1 = []*node{a}
		endAt(n, st)
		return n, nil
	}
	paren := false
	if _, ok := p.acceptOp("("); ok {
		paren = true
	}
	for {
		at := p.peek()
		if at.kind != tkName || keywords[at.text] {
			return nil, p.fail("invalid syntax", at)
		}
		p.advance()
		a := p.mk("alias", at)
		a.id, a.noAsname = at.text, true
		if p.isKw("as") {
			p.advance()
			as := p.peek()
			if as.kind != tkName || keywords[as.text] {
				return nil, p.fail("invalid syntax", as)
			}
			p.advance()
			a.asname, a.noAsname = as.text, false
			endAt(a, as)
		}
		n.l1 = append(n.l1, a)
		n.endLineno, n.endCol = endOf(a)
		if _, ok := p.acceptOp(","); !ok {
			break
		}
		if paren && p.isOp(")") {
			break
		}
		if !paren && p.atStmtEnd() {
			return nil, p.fail("trailing comma not allowed without surrounding parentheses", p.peek())
		}
	}
	if paren {
		ct, err := p.expectOp(")")
		if err != nil {
			return nil, err
		}
		endAt(n, ct)
	}
	return n, nil
}

var augOps = map[string]string{"+=": "Add", "-=": "Sub", "*=": "Mult", "/=": "Div", "//=": "FloorDiv", "%=": "Mod",
	"**=": "Pow", "<<=": "LShift", ">>=": "RShift", "&=": "BitAnd", "|=": "BitOr", "^=": "BitXor", "@=": "MatMult"}

func (p *parser) exprStmt() (*node, *syntaxErr) {
	first := p.peek()
	e, err := p.starExpressionsOrYield()
	if err != nil {
		return nil, err
	}
	t := p.peek()
	if t.kind == tkOp {
		if op, ok := augOps[t.text]; ok {
			if err := p.checkAugTarget(e); err != nil {
				return nil, err
			}
			p.advance()
			if err := p.setCtx(e, "Store"); err != nil {
				return nil, err
			}
			v, err := p.starExpressionsOrYield()
			if err != nil {
				return nil, err
			}
			n := (&node{kind: "AugAssign", op: op, a: e, b: v}).span(e, v)
			return n, nil
		}
		if t.text == ":" {
			p.advance()
			if err := p.checkAnnTarget(e); err != nil {
				return nil, err
			}
			ann, err := p.expression()
			if err != nil {
				return nil, err
			}
			n := (&node{kind: "AnnAssign", a: e, b: ann}).span(e, ann)
			if _, ok := p.acceptOp("="); ok {
				v, err := p.starExpressionsOrYield()
				if err != nil {
					return nil, err
				}
				n.c = v
				n.endLineno, n.endCol = endOf(v)
			}
			return n, nil
		}
		if t.text == "=" {
			targets := []*node{e}
			var value *node
			for p.isOp("=") {
				p.advance()
				v, err := p.starExpressionsOrYield()
				if err != nil {
					return nil, err
				}
				targets = append(targets, v)
			}
			value = targets[len(targets)-1]
			targets = targets[:len(targets)-1]
			for _, tg := range targets {
				if err := p.setCtx(tg, "Store"); err != nil {
					return nil, err
				}
			}
			n := (&node{kind: "Assign", l1: targets, a: value}).span(targets[0], value)
			return n, nil
		}
	}
	if e.kind == "NamedExpr" && !p.wasParenthesized(first, e) {
		return nil, p.fail("invalid syntax", first)
	}
	return (&node{kind: "Expr", a: e}).span(e, e), nil
}

func (p *parser) wasParenthesized(first token, e *node) bool {
	return first.kind == tkOp && first.text == "("
}

func (p *parser) checkAugTarget(e *node) *syntaxErr {
	switch e.kind {
	case "Name", "Attribute", "Subscript":
		return nil
	}
	return &syntaxErr{msg: "illegal expression for augmented assignment", line: e.lineno}
}

func (p *parser) checkAnnTarget(e *node) *syntaxErr {
	switch e.kind {
	case "Name", "Attribute", "Subscript":
		return p.setCtx(e, "Store")
	}
	return &syntaxErr{msg: "illegal target for annotation", line: e.lineno}
}

// setCtx marks an assignment or deletion target, refusing what cannot be one.
func (p *parser) setCtx(e *node, ctx string) *syntaxErr {
	switch e.kind {
	case "Name":
		e.ctx = ctx
		return nil
	case "Attribute", "Subscript":
		e.ctx = ctx
		return nil
	case "Tuple", "List":
		e.ctx = ctx
		for _, el := range e.l1 {
			if el.kind == "Starred" && ctx == "Del" {
				return &syntaxErr{msg: "cannot delete starred", line: el.lineno}
			}
			if err := p.setCtx(el, ctx); err != nil {
				return err
			}
		}
		return nil
	case "Starred":
		if ctx == "Del" {
			return &syntaxErr{msg: "cannot delete starred", line: e.lineno}
		}
		e.ctx = ctx
		return p.setCtx(e.a, ctx)
	}
	verb := "assign to"
	if ctx == "Del" {
		verb = "delete"
	}
	return &syntaxErr{msg: "cannot " + verb + " " + e.kind, line: e.lineno}
}

// block parses ':' then an indented block or simple statements on the line.
func (p *parser) block() ([]*node, *syntaxErr) {
	if _, err := p.expectOp(":"); err != nil {
		return nil, err
	}
	if p.peek().kind != tkNewline {
		return p.simpleStmts()
	}
	p.advance()
	t := p.peek()
	if t.kind != tkIndent {
		return nil, &syntaxErr{msg: "expected an indented block", line: t.line, indent: true}
	}
	p.advance()
	var out []*node
	for p.peek().kind != tkDedent && p.peek().kind != tkEOF {
		s, err := p.statement()
		if err != nil {
			return nil, err
		}
		out = append(out, s...)
	}
	if p.peek().kind == tkDedent {
		p.advance()
	}
	return out, nil
}

func lastEnd(n *node, stmts []*node) {
	if len(stmts) > 0 {
		l := stmts[len(stmts)-1]
		n.endLineno, n.endCol = endOf(l)
	}
}

func (p *parser) ifStmt() (*node, *syntaxErr) {
	t := p.advance()
	n := p.mk("If", t)
	test, err := p.namedExpression()
	if err != nil {
		return nil, err
	}
	n.a = test
	if n.body, err = p.block(); err != nil {
		return nil, err
	}
	lastEnd(n, n.body)
	if p.isKw("elif") {
		el, err := p.ifStmt()
		if err != nil {
			return nil, err
		}
		el.kind = "If"
		n.orelse = []*node{el}
		lastEnd(n, n.orelse)
	} else if p.isKw("else") {
		p.advance()
		if n.orelse, err = p.block(); err != nil {
			return nil, err
		}
		lastEnd(n, n.orelse)
	}
	return n, nil
}

func (p *parser) whileStmt() (*node, *syntaxErr) {
	t := p.advance()
	n := p.mk("While", t)
	test, err := p.namedExpression()
	if err != nil {
		return nil, err
	}
	n.a = test
	if n.body, err = p.block(); err != nil {
		return nil, err
	}
	lastEnd(n, n.body)
	if p.isKw("else") {
		p.advance()
		if n.orelse, err = p.block(); err != nil {
			return nil, err
		}
		lastEnd(n, n.orelse)
	}
	return n, nil
}

func (p *parser) forStmt() (*node, *syntaxErr) {
	t := p.advance()
	n := p.mk("For", t)
	target, err := p.targetList()
	if err != nil {
		return nil, err
	}
	if !p.isKw("in") {
		return nil, p.fail("invalid syntax", p.peek())
	}
	p.advance()
	iter, err := p.starExpressions()
	if err != nil {
		return nil, err
	}
	n.a, n.b = target, iter
	if n.body, err = p.block(); err != nil {
		return nil, err
	}
	lastEnd(n, n.body)
	if p.isKw("else") {
		p.advance()
		if n.orelse, err = p.block(); err != nil {
			return nil, err
		}
		lastEnd(n, n.orelse)
	}
	return n, nil
}

// targetList parses a for or comprehension target (star_targets).
func (p *parser) targetList() (*node, *syntaxErr) {
	first, err := p.starTarget()
	if err != nil {
		return nil, err
	}
	if !p.isOp(",") {
		if err := p.setCtx(first, "Store"); err != nil {
			return nil, err
		}
		return first, nil
	}
	tup := (&node{kind: "Tuple", l1: []*node{first}}).span(first, first)
	for p.isOp(",") {
		ct := p.advance()
		endAt(tup, ct)
		if p.isKw("in") || p.isOp("=") {
			break
		}
		el, err := p.starTarget()
		if err != nil {
			return nil, err
		}
		tup.l1 = append(tup.l1, el)
		tup.endLineno, tup.endCol = endOf(el)
	}
	if err := p.setCtx(tup, "Store"); err != nil {
		return nil, err
	}
	return tup, nil
}

func (p *parser) starTarget() (*node, *syntaxErr) {
	if st, ok := p.acceptOp("*"); ok {
		v, err := p.starTarget()
		if err != nil {
			return nil, err
		}
		n := p.mk("Starred", st)
		n.a = v
		n.endLineno, n.endCol = endOf(v)
		return n, nil
	}
	return p.bitwiseOr()
}

func (p *parser) tryStmt() (*node, *syntaxErr) {
	t := p.advance()
	n := p.mk("Try", t)
	var err *syntaxErr
	if n.body, err = p.block(); err != nil {
		return nil, err
	}
	lastEnd(n, n.body)
	for p.isKw("except") {
		et := p.advance()
		h := p.mk("ExceptHandler", et)
		h.noID = true
		if p.isOp("*") {
			return nil, p.fail("invalid syntax", p.peek())
		}
		if !p.isOp(":") {
			typ, err := p.expression()
			if err != nil {
				return nil, err
			}
			if p.isOp(",") {
				return nil, p.fail("multiple exception types must be parenthesized", p.peek())
			}
			h.a = typ
			if p.isKw("as") {
				p.advance()
				nt := p.peek()
				if nt.kind != tkName || keywords[nt.text] {
					return nil, p.fail("invalid syntax", nt)
				}
				p.advance()
				h.id, h.noID = nt.text, false
			}
		}
		if h.body, err = p.block(); err != nil {
			return nil, err
		}
		lastEnd(h, h.body)
		n.l1 = append(n.l1, h)
		lastEnd(n, h.body)
	}
	if len(n.l1) > 0 && p.isKw("else") {
		p.advance()
		if n.orelse, err = p.block(); err != nil {
			return nil, err
		}
		lastEnd(n, n.orelse)
	}
	if p.isKw("finally") {
		p.advance()
		if n.finalbody, err = p.block(); err != nil {
			return nil, err
		}
		lastEnd(n, n.finalbody)
	}
	if len(n.l1) == 0 && len(n.finalbody) == 0 {
		return nil, p.fail("expected 'except' or 'finally' block", p.peek())
	}
	return n, nil
}

func (p *parser) withStmt() (*node, *syntaxErr) {
	t := p.advance()
	n := p.mk("With", t)
	paren := false
	if p.isOp("(") && p.parenthesizedWithItems() {
		p.advance()
		paren = true
	}
	for {
		ce, err := p.expression()
		if err != nil {
			return nil, err
		}
		item := &node{kind: "withitem", a: ce}
		if p.isKw("as") {
			p.advance()
			tg, err := p.starTarget()
			if err != nil {
				return nil, err
			}
			if err := p.setCtx(tg, "Store"); err != nil {
				return nil, err
			}
			item.b = tg
		}
		n.l1 = append(n.l1, item)
		if _, ok := p.acceptOp(","); !ok {
			break
		}
		if paren && p.isOp(")") {
			break
		}
	}
	if paren {
		if _, err := p.expectOp(")"); err != nil {
			return nil, err
		}
	}
	var err *syntaxErr
	if n.body, err = p.block(); err != nil {
		return nil, err
	}
	lastEnd(n, n.body)
	return n, nil
}

// parenthesizedWithItems reports whether the '(' after `with` opens a
// parenthesized list of with-items (followed by ':' after its ')') rather
// than starting an expression.
func (p *parser) parenthesizedWithItems() bool {
	depth := 0
	for i := p.p; i < len(p.toks); i++ {
		t := p.toks[i]
		if t.kind == tkOp {
			switch t.text {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				depth--
				if depth == 0 {
					nt := p.toks[i+1]
					if !(nt.kind == tkOp && nt.text == ":") {
						return false
					}
					// `with (a, b):` is a tuple expression unless an item has `as`
					// or a trailing comma would make the items list ambiguous.
					for j := p.p + 1; j < i; j++ {
						if p.toks[j].kind == tkName && p.toks[j].text == "as" {
							return true
						}
					}
					return false
				}
			}
		}
		if t.kind == tkNewline || t.kind == tkEOF {
			return false
		}
	}
	return false
}

func (p *parser) decorated() ([]*node, *syntaxErr) {
	var decos []*node
	for p.isOp("@") {
		p.advance()
		d, err := p.namedExpression()
		if err != nil {
			return nil, err
		}
		decos = append(decos, d)
		if p.peek().kind != tkNewline {
			return nil, p.fail("invalid syntax", p.peek())
		}
		p.advance()
	}
	if p.isKw("def") {
		n, err := p.funcDef(decos)
		return []*node{n}, err
	}
	if p.isKw("class") {
		n, err := p.classDef(decos)
		return []*node{n}, err
	}
	return nil, p.fail("invalid syntax", p.peek())
}

func (p *parser) funcDef(decos []*node) (*node, *syntaxErr) {
	t := p.advance()
	n := p.mk("FunctionDef", t)
	n.src = &p.src
	nt := p.peek()
	if nt.kind != tkName || keywords[nt.text] {
		return nil, p.fail("invalid syntax", nt)
	}
	p.advance()
	n.id = nt.text
	n.l1 = decos
	if _, err := p.expectOp("("); err != nil {
		return nil, err
	}
	args, err := p.parameters(")", true)
	if err != nil {
		return nil, err
	}
	if _, err := p.expectOp(")"); err != nil {
		return nil, err
	}
	n.a = args
	if _, ok := p.acceptOp("->"); ok {
		r, err := p.expression()
		if err != nil {
			return nil, err
		}
		n.b = r
	}
	if n.body, err = p.block(); err != nil {
		return nil, err
	}
	lastEnd(n, n.body)
	return n, nil
}

func (p *parser) classDef(decos []*node) (*node, *syntaxErr) {
	t := p.advance()
	n := p.mk("ClassDef", t)
	nt := p.peek()
	if nt.kind != tkName || keywords[nt.text] {
		return nil, p.fail("invalid syntax", nt)
	}
	p.advance()
	n.id = nt.text
	n.l3 = decos
	if _, ok := p.acceptOp("("); ok {
		args, kws, _, err := p.callArgs()
		if err != nil {
			return nil, err
		}
		n.l1, n.l2 = args, kws
	}
	var err *syntaxErr
	if n.body, err = p.block(); err != nil {
		return nil, err
	}
	lastEnd(n, n.body)
	return n, nil
}

// parameters parses a parameter list up to close (")" for def, ":" for
// lambda); annotations are allowed only for def.
func (p *parser) parameters(close string, annotated bool) (*node, *syntaxErr) {
	args := &node{kind: "arguments"}
	var defaults []*node
	seenDefault, seenStar, seenSlash := false, false, false
	param := func() (*node, *syntaxErr) {
		t := p.peek()
		if t.kind != tkName || keywords[t.text] {
			return nil, p.fail("invalid syntax", t)
		}
		p.advance()
		a := p.mk("arg", t)
		a.id = t.text
		if annotated && p.isOp(":") {
			p.advance()
			ann, err := p.expression()
			if err != nil {
				return nil, err
			}
			a.a = ann
			a.endLineno, a.endCol = endOf(ann)
		}
		return a, nil
	}
	for !p.isOp(close) {
		t := p.peek()
		switch {
		case t.kind == tkOp && t.text == "/":
			if seenSlash || seenStar || len(args.l2) == 0 {
				return nil, p.fail("invalid syntax", t)
			}
			p.advance()
			seenSlash = true
			args.l1, args.l2 = args.l2, nil
		case t.kind == tkOp && t.text == "*":
			if seenStar {
				return nil, p.fail("* argument may appear only once", t)
			}
			p.advance()
			seenStar = true
			if !p.isOp(",") && !p.isOp(close) {
				a, err := param()
				if err != nil {
					return nil, err
				}
				args.a = a
			} else if p.isOp(close) || p.isOp(",") && (p.peekN(1).kind == tkOp && (p.peekN(1).text == close || p.peekN(1).text == "**")) {
				return nil, p.fail("named arguments must follow bare *", t)
			}
		case t.kind == tkOp && t.text == "**":
			p.advance()
			a, err := param()
			if err != nil {
				return nil, err
			}
			args.b = a
			p.acceptOp(",")
			if !p.isOp(close) {
				return nil, p.fail("arguments cannot follow var-keyword argument", p.peek())
			}
			continue
		default:
			a, err := param()
			if err != nil {
				return nil, err
			}
			var def *node
			if _, ok := p.acceptOp("="); ok {
				d, err := p.expression()
				if err != nil {
					return nil, err
				}
				def = d
			}
			if seenStar {
				args.l3 = append(args.l3, a)
				args.l4 = append(args.l4, def)
			} else {
				if def != nil {
					seenDefault = true
					defaults = append(defaults, def)
				} else if seenDefault {
					return nil, p.fail("parameter without a default follows parameter with a default", t)
				}
				args.l2 = append(args.l2, a)
			}
		}
		if _, ok := p.acceptOp(","); !ok {
			break
		}
	}
	if seenStar && args.a == nil && len(args.l3) == 0 {
		return nil, p.fail("named arguments must follow bare *", p.peek())
	}
	args.body = defaults
	return args, nil
}

// ---------------------------------------------------------------- expressions

func (p *parser) starExpressionsOrYield() (*node, *syntaxErr) {
	if p.isKw("yield") {
		return p.yieldExpr()
	}
	return p.starExpressions()
}

func (p *parser) yieldExpr() (*node, *syntaxErr) {
	t := p.advance()
	if p.isKw("from") {
		p.advance()
		v, err := p.expression()
		if err != nil {
			return nil, err
		}
		n := p.mk("YieldFrom", t)
		n.a = v
		n.endLineno, n.endCol = endOf(v)
		return n, nil
	}
	n := p.mk("Yield", t)
	if !p.atStmtEnd() && !p.isOp(")") && !p.isOp("=") {
		v, err := p.starExpressions()
		if err != nil {
			return nil, err
		}
		n.a = v
		n.endLineno, n.endCol = endOf(v)
	}
	return n, nil
}

// starExpressions: star_expression (',' star_expression)* [','] as a Tuple
// when there is a comma.
func (p *parser) starExpressions() (*node, *syntaxErr) {
	first, err := p.starExpression()
	if err != nil {
		return nil, err
	}
	if !p.isOp(",") {
		return first, nil
	}
	tup := (&node{kind: "Tuple", ctx: "Load", l1: []*node{first}}).span(first, first)
	for p.isOp(",") {
		ct := p.advance()
		endAt(tup, ct)
		if p.exprEnds() {
			break
		}
		el, err := p.starExpression()
		if err != nil {
			return nil, err
		}
		tup.l1 = append(tup.l1, el)
		tup.endLineno, tup.endCol = endOf(el)
	}
	return tup, nil
}

// exprEnds reports whether the next token cannot start an expression.
func (p *parser) exprEnds() bool {
	t := p.peek()
	switch t.kind {
	case tkNewline, tkEOF, tkIndent, tkDedent:
		return true
	case tkOp:
		switch t.text {
		case "(", "[", "{", "-", "+", "~", "*", "...":
			return false
		}
		return true
	case tkName:
		switch t.text {
		case "in", "for", "if", "else", "and", "or", "is", "as", "from":
			return true
		}
	}
	return false
}

func (p *parser) starExpression() (*node, *syntaxErr) {
	if st, ok := p.acceptOp("*"); ok {
		v, err := p.bitwiseOr()
		if err != nil {
			return nil, err
		}
		n := p.mk("Starred", st)
		n.ctx = "Load"
		n.a = v
		n.endLineno, n.endCol = endOf(v)
		return n, nil
	}
	return p.expression()
}

func (p *parser) namedExpression() (*node, *syntaxErr) {
	t := p.peek()
	if t.kind == tkName && !keywords[t.text] && p.peekN(1).kind == tkOp && p.peekN(1).text == ":=" {
		p.advance()
		p.advance()
		v, err := p.expression()
		if err != nil {
			return nil, err
		}
		target := p.mk("Name", t)
		target.id, target.ctx = t.text, "Store"
		return (&node{kind: "NamedExpr", a: target, b: v}).span(target, v), nil
	}
	e, err := p.expression()
	if err != nil {
		return nil, err
	}
	if p.isOp(":=") {
		return nil, p.fail("cannot use assignment expressions with "+e.kind, p.peek())
	}
	return e, nil
}

func (p *parser) expression() (*node, *syntaxErr) {
	if p.isKw("lambda") {
		return p.lambda()
	}
	body, err := p.disjunction()
	if err != nil {
		return nil, err
	}
	if p.isKw("if") {
		p.advance()
		test, err := p.disjunction()
		if err != nil {
			return nil, err
		}
		if !p.isKw("else") {
			return nil, p.fail("expected 'else' after 'if' expression", p.peek())
		}
		p.advance()
		orelse, err := p.expression()
		if err != nil {
			return nil, err
		}
		return (&node{kind: "IfExp", a: test, b: body, c: orelse}).span(body, orelse), nil
	}
	return body, nil
}

func (p *parser) lambda() (*node, *syntaxErr) {
	t := p.advance()
	n := p.mk("Lambda", t)
	n.src = &p.src
	args, err := p.parameters(":", false)
	if err != nil {
		return nil, err
	}
	if _, err := p.expectOp(":"); err != nil {
		return nil, err
	}
	body, err := p.expression()
	if err != nil {
		return nil, err
	}
	n.a, n.b = args, body
	n.endLineno, n.endCol = endOf(body)
	return n, nil
}

func (p *parser) disjunction() (*node, *syntaxErr) {
	first, err := p.conjunction()
	if err != nil {
		return nil, err
	}
	if !p.isKw("or") {
		return first, nil
	}
	n := &node{kind: "BoolOp", op: "Or", l1: []*node{first}}
	last := first
	for p.isKw("or") {
		p.advance()
		v, err := p.conjunction()
		if err != nil {
			return nil, err
		}
		n.l1 = append(n.l1, v)
		last = v
	}
	return n.span(first, last), nil
}

func (p *parser) conjunction() (*node, *syntaxErr) {
	first, err := p.inversion()
	if err != nil {
		return nil, err
	}
	if !p.isKw("and") {
		return first, nil
	}
	n := &node{kind: "BoolOp", op: "And", l1: []*node{first}}
	last := first
	for p.isKw("and") {
		p.advance()
		v, err := p.inversion()
		if err != nil {
			return nil, err
		}
		n.l1 = append(n.l1, v)
		last = v
	}
	return n.span(first, last), nil
}

func (p *parser) inversion() (*node, *syntaxErr) {
	if p.isKw("not") {
		t := p.advance()
		v, err := p.inversion()
		if err != nil {
			return nil, err
		}
		n := p.mk("UnaryOp", t)
		n.op, n.a = "Not", v
		n.endLineno, n.endCol = endOf(v)
		return n, nil
	}
	return p.comparison()
}

func (p *parser) compareOp() (string, bool) {
	t := p.peek()
	if t.kind == tkOp {
		switch t.text {
		case "==":
			return "Eq", true
		case "!=":
			return "NotEq", true
		case "<":
			return "Lt", true
		case "<=":
			return "LtE", true
		case ">":
			return "Gt", true
		case ">=":
			return "GtE", true
		}
		return "", false
	}
	if t.kind == tkName {
		switch t.text {
		case "in":
			return "In", true
		case "not":
			if n := p.peekN(1); n.kind == tkName && n.text == "in" {
				return "NotIn", true
			}
		case "is":
			if n := p.peekN(1); n.kind == tkName && n.text == "not" {
				return "IsNot", true
			}
			return "Is", true
		}
	}
	return "", false
}

func (p *parser) comparison() (*node, *syntaxErr) {
	left, err := p.bitwiseOr()
	if err != nil {
		return nil, err
	}
	op, ok := p.compareOp()
	if !ok {
		return left, nil
	}
	n := &node{kind: "Compare", a: left}
	last := left
	for ok {
		p.advance()
		if op == "NotIn" || op == "IsNot" {
			p.advance()
		}
		right, err := p.bitwiseOr()
		if err != nil {
			return nil, err
		}
		n.ops = append(n.ops, op)
		n.l1 = append(n.l1, right)
		last = right
		op, ok = p.compareOp()
	}
	return n.span(left, last), nil
}

func (p *parser) binLevel(next func() (*node, *syntaxErr), ops map[string]string) (*node, *syntaxErr) {
	left, err := next()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind != tkOp {
			return left, nil
		}
		op, ok := ops[t.text]
		if !ok {
			return left, nil
		}
		p.advance()
		right, err := next()
		if err != nil {
			return nil, err
		}
		left = (&node{kind: "BinOp", op: op, a: left, b: right}).span(left, right)
	}
}

var (
	orOps    = map[string]string{"|": "BitOr"}
	xorOps   = map[string]string{"^": "BitXor"}
	andOps   = map[string]string{"&": "BitAnd"}
	shiftOps = map[string]string{"<<": "LShift", ">>": "RShift"}
	sumOps   = map[string]string{"+": "Add", "-": "Sub"}
	termOps  = map[string]string{"*": "Mult", "/": "Div", "//": "FloorDiv", "%": "Mod", "@": "MatMult"}
)

func (p *parser) bitwiseOr() (*node, *syntaxErr)  { return p.binLevel(p.bitwiseXor, orOps) }
func (p *parser) bitwiseXor() (*node, *syntaxErr) { return p.binLevel(p.bitwiseAnd, xorOps) }
func (p *parser) bitwiseAnd() (*node, *syntaxErr) { return p.binLevel(p.shiftExpr, andOps) }
func (p *parser) shiftExpr() (*node, *syntaxErr)  { return p.binLevel(p.sum, shiftOps) }
func (p *parser) sum() (*node, *syntaxErr)        { return p.binLevel(p.term, sumOps) }
func (p *parser) term() (*node, *syntaxErr)       { return p.binLevel(p.factor, termOps) }

func (p *parser) factor() (*node, *syntaxErr) {
	t := p.peek()
	if t.kind == tkOp {
		op := map[string]string{"+": "UAdd", "-": "USub", "~": "Invert"}[t.text]
		if op != "" {
			p.advance()
			v, err := p.factor()
			if err != nil {
				return nil, err
			}
			n := p.mk("UnaryOp", t)
			n.op, n.a = op, v
			n.endLineno, n.endCol = endOf(v)
			return n, nil
		}
	}
	return p.power()
}

func (p *parser) power() (*node, *syntaxErr) {
	var base *node
	var err *syntaxErr
	if p.isKw("await") {
		t := p.advance()
		v, err := p.primary()
		if err != nil {
			return nil, err
		}
		base = p.mk("Await", t)
		base.a = v
		base.endLineno, base.endCol = endOf(v)
	} else if base, err = p.primary(); err != nil {
		return nil, err
	}
	if _, ok := p.acceptOp("**"); ok {
		exp, err := p.factor()
		if err != nil {
			return nil, err
		}
		return (&node{kind: "BinOp", op: "Pow", a: base, b: exp}).span(base, exp), nil
	}
	return base, nil
}

func (p *parser) primary() (*node, *syntaxErr) {
	e, err := p.atom()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind != tkOp {
			return e, nil
		}
		switch t.text {
		case ".":
			p.advance()
			nt := p.peek()
			if nt.kind != tkName || keywords[nt.text] {
				return nil, p.fail("invalid syntax", nt)
			}
			p.advance()
			a := &node{kind: "Attribute", a: e, id: nt.text, ctx: "Load"}
			a.lineno, a.col = startOf(e)
			a.hasPos = true
			endAt(a, nt)
			e = a
		case "(":
			open := p.advance()
			args, kws, ct, err := p.callArgs()
			if err != nil {
				return nil, err
			}
			if len(args) == 1 && len(kws) == 0 && args[0].kind == "GeneratorExp" && args[0].outer == nil {
				args[0].lineno, args[0].col = open.line, open.col
				args[0].endLineno, args[0].endCol = ct.endLine, ct.endCol
			}
			c := &node{kind: "Call", a: e, l1: args, l2: kws}
			c.lineno, c.col = startOf(e)
			c.hasPos = true
			endAt(c, ct)
			e = c
		case "[":
			p.advance()
			sl, err := p.slices()
			if err != nil {
				return nil, err
			}
			ct, err := p.expectOp("]")
			if err != nil {
				return nil, err
			}
			s := &node{kind: "Subscript", a: e, b: sl, ctx: "Load"}
			s.lineno, s.col = startOf(e)
			s.hasPos = true
			endAt(s, ct)
			e = s
		default:
			return e, nil
		}
	}
}

// callArgs parses call arguments after '(' through ')'.
func (p *parser) callArgs() ([]*node, []*node, token, *syntaxErr) {
	var args, kws []*node
	seenKw, seenKwUnpack := false, false
	for !p.isOp(")") {
		t := p.peek()
		switch {
		case t.kind == tkOp && t.text == "**":
			p.advance()
			v, err := p.expression()
			if err != nil {
				return nil, nil, t, err
			}
			k := p.mk("keyword", t)
			k.noID, k.a = true, v
			k.endLineno, k.endCol = endOf(v)
			kws = append(kws, k)
			seenKwUnpack = true
		case t.kind == tkOp && t.text == "*":
			if seenKwUnpack {
				return nil, nil, t, p.fail("iterable argument unpacking follows keyword argument unpacking", t)
			}
			v, err := p.starExpression()
			if err != nil {
				return nil, nil, t, err
			}
			args = append(args, v)
		case t.kind == tkName && !keywords[t.text] && p.peekN(1).kind == tkOp && p.peekN(1).text == "=":
			p.advance()
			p.advance()
			v, err := p.expression()
			if err != nil {
				return nil, nil, t, err
			}
			k := p.mk("keyword", t)
			k.id, k.a = t.text, v
			k.endLineno, k.endCol = endOf(v)
			kws = append(kws, k)
			seenKw = true
		default:
			v, err := p.namedExpression()
			if err != nil {
				return nil, nil, t, err
			}
			if p.isKw("for") {
				gen, err := p.comprehensionTail("GeneratorExp", v, t)
				if err != nil {
					return nil, nil, t, err
				}
				v = gen
			} else if p.isOp("=") {
				return nil, nil, t, p.fail("expression cannot contain assignment, perhaps you meant \"==\"?", t)
			}
			if seenKwUnpack {
				return nil, nil, t, p.fail("positional argument follows keyword argument unpacking", t)
			}
			if seenKw {
				return nil, nil, t, p.fail("positional argument follows keyword argument", t)
			}
			args = append(args, v)
		}
		if _, ok := p.acceptOp(","); !ok {
			break
		}
	}
	ct, err := p.expectOp(")")
	if err != nil {
		return nil, nil, ct, err
	}
	for _, a := range args {
		if a.kind == "GeneratorExp" && len(args)+len(kws) > 1 {
			return nil, nil, ct, &syntaxErr{msg: "Generator expression must be parenthesized", line: a.lineno}
		}
	}
	return args, kws, ct, nil
}

// slices parses the inside of a subscript.
func (p *parser) slices() (*node, *syntaxErr) {
	first, err := p.slice()
	if err != nil {
		return nil, err
	}
	if !p.isOp(",") {
		return first, nil
	}
	tup := (&node{kind: "Tuple", ctx: "Load", l1: []*node{first}}).span(first, first)
	for p.isOp(",") {
		ct := p.advance()
		endAt(tup, ct)
		if p.isOp("]") {
			break
		}
		el, err := p.slice()
		if err != nil {
			return nil, err
		}
		tup.l1 = append(tup.l1, el)
		tup.endLineno, tup.endCol = endOf(el)
	}
	return tup, nil
}

func (p *parser) slice() (*node, *syntaxErr) {
	start := p.peek()
	if start.kind == tkOp && start.text == "*" {
		return p.starExpression()
	}
	var lower *node
	if !p.isOp(":") {
		e, err := p.namedExpression()
		if err != nil {
			return nil, err
		}
		if !p.isOp(":") {
			return e, nil
		}
		lower = e
	}
	ct := p.advance() // ':'
	n := p.mk("Slice", start)
	endAt(n, ct)
	n.a = lower
	if !p.isOp(":") && !p.isOp("]") && !p.isOp(",") {
		u, err := p.expression()
		if err != nil {
			return nil, err
		}
		n.b = u
		n.endLineno, n.endCol = endOf(u)
	}
	if st, ok := p.acceptOp(":"); ok {
		endAt(n, st)
		if !p.isOp("]") && !p.isOp(",") {
			s, err := p.expression()
			if err != nil {
				return nil, err
			}
			n.c = s
			n.endLineno, n.endCol = endOf(s)
		}
	}
	return n, nil
}

func (p *parser) atom() (*node, *syntaxErr) {
	t := p.peek()
	switch t.kind {
	case tkName:
		switch t.text {
		case "True", "False", "None":
			p.advance()
			n := p.mk("Constant", t)
			n.value = map[string]Value{"True": trueV, "False": falseV, "None": none}[t.text]
			return n, nil
		}
		if keywords[t.text] {
			return nil, p.fail("invalid syntax", t)
		}
		p.advance()
		n := p.mk("Name", t)
		n.id, n.ctx = nfkcName(t.text), "Load"
		return n, nil
	case tkNumber:
		p.advance()
		v, err := parseNumber(t.text)
		if err != nil {
			return nil, p.fail(err.Error(), t)
		}
		n := p.mk("Constant", t)
		n.value = v
		return n, nil
	case tkString:
		return p.strings()
	case tkOp:
		switch t.text {
		case "(":
			return p.parenAtom()
		case "[":
			return p.listAtom()
		case "{":
			return p.braceAtom()
		case "...":
			p.advance()
			n := p.mk("Constant", t)
			n.value = ellipsisV{}
			return n, nil
		}
	}
	if t.kind == tkEOF || t.kind == tkNewline {
		return nil, p.fail("invalid syntax", t)
	}
	return nil, p.fail("invalid syntax", t)
}

func (p *parser) parenAtom() (*node, *syntaxErr) {
	open := p.advance()
	if ct, ok := p.acceptOp(")"); ok {
		n := p.mk("Tuple", open)
		n.ctx = "Load"
		return endAt(n, ct), nil
	}
	if p.isKw("yield") {
		y, err := p.yieldExpr()
		if err != nil {
			return nil, err
		}
		ct, err := p.expectOp(")")
		if err != nil {
			return nil, err
		}
		y.outer = &[4]int{open.line, open.col, ct.endLine, ct.endCol}
		return y, nil
	}
	first, err := p.starOrNamed()
	if err != nil {
		return nil, err
	}
	if p.isKw("for") {
		gen, err := p.comprehensionTail("GeneratorExp", first, open)
		if err != nil {
			return nil, err
		}
		ct, err := p.expectOp(")")
		if err != nil {
			return nil, err
		}
		return endAt(gen, ct), nil
	}
	if ct, ok := p.acceptOp(")"); ok {
		if first.kind == "Starred" {
			return nil, &syntaxErr{msg: "cannot use starred expression here", line: first.lineno}
		}
		first.outer = &[4]int{open.line, open.col, ct.endLine, ct.endCol}
		return first, nil
	}
	n := p.mk("Tuple", open)
	n.ctx = "Load"
	n.l1 = []*node{first}
	for p.isOp(",") {
		p.advance()
		if p.isOp(")") {
			break
		}
		el, err := p.starOrNamed()
		if err != nil {
			return nil, err
		}
		n.l1 = append(n.l1, el)
	}
	ct, err := p.expectOp(")")
	if err != nil {
		return nil, err
	}
	return endAt(n, ct), nil
}

func (p *parser) starOrNamed() (*node, *syntaxErr) {
	if p.isOp("*") {
		return p.starExpression()
	}
	return p.namedExpression()
}

func (p *parser) listAtom() (*node, *syntaxErr) {
	open := p.advance()
	n := p.mk("List", open)
	n.ctx = "Load"
	if ct, ok := p.acceptOp("]"); ok {
		return endAt(n, ct), nil
	}
	first, err := p.starOrNamed()
	if err != nil {
		return nil, err
	}
	if p.isKw("for") {
		comp, err := p.comprehensionTail("ListComp", first, open)
		if err != nil {
			return nil, err
		}
		ct, err := p.expectOp("]")
		if err != nil {
			return nil, err
		}
		return endAt(comp, ct), nil
	}
	n.l1 = []*node{first}
	for p.isOp(",") {
		p.advance()
		if p.isOp("]") {
			break
		}
		el, err := p.starOrNamed()
		if err != nil {
			return nil, err
		}
		n.l1 = append(n.l1, el)
	}
	ct, err := p.expectOp("]")
	if err != nil {
		return nil, err
	}
	return endAt(n, ct), nil
}

func (p *parser) braceAtom() (*node, *syntaxErr) {
	open := p.advance()
	if ct, ok := p.acceptOp("}"); ok {
		n := p.mk("Dict", open)
		return endAt(n, ct), nil
	}
	// dict display or comprehension
	if p.isOp("**") {
		return p.dictRest(open, nil, nil)
	}
	first, err := p.starOrNamed()
	if err != nil {
		return nil, err
	}
	if _, ok := p.acceptOp(":"); ok {
		if first.kind == "Starred" {
			return nil, p.fail("invalid syntax", p.peek())
		}
		val, err := p.expression()
		if err != nil {
			return nil, err
		}
		if p.isKw("for") {
			comp := p.mk("DictComp", open)
			comp.a, comp.b = first, val
			gens, err := p.comprehensionFor()
			if err != nil {
				return nil, err
			}
			comp.l1 = gens
			ct, err := p.expectOp("}")
			if err != nil {
				return nil, err
			}
			return endAt(comp, ct), nil
		}
		return p.dictRest(open, first, val)
	}
	if p.isKw("for") {
		comp, err := p.comprehensionTail("SetComp", first, open)
		if err != nil {
			return nil, err
		}
		ct, err := p.expectOp("}")
		if err != nil {
			return nil, err
		}
		return endAt(comp, ct), nil
	}
	n := p.mk("Set", open)
	n.l1 = []*node{first}
	for p.isOp(",") {
		p.advance()
		if p.isOp("}") {
			break
		}
		el, err := p.starOrNamed()
		if err != nil {
			return nil, err
		}
		n.l1 = append(n.l1, el)
	}
	ct, err := p.expectOp("}")
	if err != nil {
		return nil, err
	}
	return endAt(n, ct), nil
}

// dictRest parses a dict display after its first item (key nil for none
// parsed yet, or for a ** item).
func (p *parser) dictRest(open token, key, val *node) (*node, *syntaxErr) {
	n := p.mk("Dict", open)
	if val != nil {
		n.l1 = append(n.l1, key)
		n.l2 = append(n.l2, val)
		if _, ok := p.acceptOp(","); !ok {
			ct, err := p.expectOp("}")
			if err != nil {
				return nil, err
			}
			return endAt(n, ct), nil
		}
	}
	for !p.isOp("}") {
		if _, ok := p.acceptOp("**"); ok {
			v, err := p.bitwiseOr()
			if err != nil {
				return nil, err
			}
			n.l1 = append(n.l1, nil)
			n.l2 = append(n.l2, v)
		} else {
			k, err := p.expression()
			if err != nil {
				return nil, err
			}
			if _, err := p.expectOp(":"); err != nil {
				return nil, err
			}
			v, err := p.expression()
			if err != nil {
				return nil, err
			}
			n.l1 = append(n.l1, k)
			n.l2 = append(n.l2, v)
		}
		if _, ok := p.acceptOp(","); !ok {
			break
		}
	}
	ct, err := p.expectOp("}")
	if err != nil {
		return nil, err
	}
	return endAt(n, ct), nil
}

func (p *parser) comprehensionTail(kind string, elt *node, open token) (*node, *syntaxErr) {
	if elt.kind == "Starred" {
		return nil, &syntaxErr{msg: "iterable unpacking cannot be used in comprehension", line: elt.lineno}
	}
	n := p.mk(kind, open)
	n.a = elt
	gens, err := p.comprehensionFor()
	if err != nil {
		return nil, err
	}
	n.l1 = gens
	if kind == "GeneratorExp" && open.kind == tkOp && open.text != "(" {
		n.lineno, n.col = elt.lineno, elt.col
	}
	last := gens[len(gens)-1]
	n.endLineno, n.endCol = endOf(last)
	return n, nil
}

func (p *parser) comprehensionFor() ([]*node, *syntaxErr) {
	var gens []*node
	for p.isKw("for") || p.isKw("async") {
		if p.isKw("async") {
			return nil, p.fail("invalid syntax", p.peek())
		}
		p.advance()
		target, err := p.targetList()
		if err != nil {
			return nil, err
		}
		if !p.isKw("in") {
			return nil, p.fail("invalid syntax", p.peek())
		}
		p.advance()
		iter, err := p.disjunction()
		if err != nil {
			return nil, err
		}
		g := &node{kind: "comprehension", a: target, b: iter}
		g.endLineno, g.endCol = endOf(iter)
		for p.isKw("if") {
			p.advance()
			cond, err := p.disjunction()
			if err != nil {
				return nil, err
			}
			g.l1 = append(g.l1, cond)
			g.endLineno, g.endCol = endOf(cond)
		}
		gens = append(gens, g)
	}
	return gens, nil
}

// ---------------------------------------------------------------- literals

// parseNumber converts a NUMBER token to its value.
func parseNumber(text string) (Value, error) {
	s := strings.ReplaceAll(text, "_", "")
	last := s[len(s)-1]
	if last == 'j' || last == 'J' {
		f, err := strconv.ParseFloat(s[:len(s)-1], 64)
		if err != nil && !isRangeErr(err) {
			return nil, err
		}
		return complexV(complex(0, f)), nil
	}
	if len(s) > 1 && s[0] == '0' && strings.IndexByte("xXoObB", s[1]) >= 0 {
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[s[1]]
		n, _ := new(big.Int).SetString(s[2:], base)
		return intV{n}, nil
	}
	if strings.ContainsAny(s, ".eE") {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil && !isRangeErr(err) {
			return nil, err
		}
		return floatV(f), nil
	}
	if len(s) > maxStrDigits {
		return nil, &syntaxErr{msg: "Exceeds the limit (4300 digits) for integer string conversion: value has " +
			itoa(len(s)) + " digits; use sys.set_int_max_str_digits() to increase the limit - Consider hexadecimal for huge integer literals to avoid decimal conversion limits."}
	}
	n, _ := new(big.Int).SetString(s, 10)
	return intV{n}, nil
}

func isRangeErr(err error) bool {
	ne, ok := err.(*strconv.NumError)
	return ok && ne.Err == strconv.ErrRange
}

// strings parses one or more adjacent string literals into a Constant (str
// or bytes) or a JoinedStr. Inside an f-string every part has its own
// position, as in CPython 3.12: a literal part covers its text (a plain
// string token, its whole token), a replacement field its braces.
func (p *parser) strings() (*node, *syntaxErr) {
	first := p.peek()
	var parts []*node // Constant and FormattedValue pieces
	isBytes, isFstr := false, false
	last := first
	count := 0
	addLit := func(c *node) {
		if n := len(parts); n > 0 && parts[n-1].kind == "Constant" {
			prev := parts[n-1]
			s1, _ := prev.value.(strV)
			s2, _ := c.value.(strV)
			prev.value = s1 + s2
			prev.endLineno, prev.endCol = c.endLineno, c.endCol
			return
		}
		parts = append(parts, c)
	}
	var plain strings.Builder
	for p.peek().kind == tkString {
		t := p.advance()
		last = t
		prefix, body, raw := splitStrToken(t.text)
		b := strings.ContainsAny(prefix, "bB")
		f := strings.ContainsAny(prefix, "fF")
		if count > 0 && b != isBytes {
			return nil, p.fail("cannot mix bytes and nonbytes literals", t)
		}
		isBytes = b
		count++
		if f {
			isFstr = true
			vals, err := p.fstringBody(body, raw, t, 0, t.line, p.bodyOffset(t))
			if err != nil {
				return nil, err
			}
			for _, v := range vals {
				if v.kind == "Constant" {
					addLit(v)
				} else {
					parts = append(parts, v)
				}
			}
			continue
		}
		s, err := decodeStrBody(body, raw, b)
		if err != nil {
			return nil, &syntaxErr{msg: err.Error(), line: t.line}
		}
		plain.WriteString(s)
		c := p.mk("Constant", t)
		c.value = strV(s)
		if s != "" || true {
			addLit(c)
		}
	}
	if !isFstr {
		n := p.mk("Constant", first)
		endAt(n, last)
		if isBytes {
			n.value = bytesV(plain.String())
		} else {
			n.value = strV(plain.String())
		}
		if strings.HasPrefix(strings.ToLower(first.text), "u") {
			n.id = "u"
		}
		return n, nil
	}
	var kept []*node
	for _, part := range parts {
		if part.kind == "Constant" {
			if s, _ := part.value.(strV); s == "" {
				continue
			}
		}
		kept = append(kept, part)
	}
	n := p.mk("JoinedStr", first)
	endAt(n, last)
	n.l1 = kept
	return n, nil
}

// splitStrToken returns a string token's prefix, its body without the
// quotes, and whether it is raw.
func splitStrToken(text string) (string, string, bool) {
	i := 0
	for i < len(text) && text[i] != '\'' && text[i] != '"' {
		i++
	}
	prefix := text[:i]
	q := text[i:]
	n := 1
	if len(q) >= 6 && (strings.HasPrefix(q, `"""`) || strings.HasPrefix(q, `'''`)) {
		n = 3
	}
	return prefix, q[n : len(q)-n], strings.ContainsAny(prefix, "rR")
}

// bodyOffset is the column of the first body character of a string token.
func (p *parser) bodyOffset(tok token) int {
	i := 0
	for i < len(tok.text) && tok.text[i] != '\'' && tok.text[i] != '"' {
		i++
	}
	q := tok.text[i:]
	if strings.HasPrefix(q, `"""`) || strings.HasPrefix(q, `'''`) {
		return tok.col + i + 3
	}
	return tok.col + i + 1
}

// fbody is the text of an f-string body (or of a format spec within one)
// with the source position of its first character.
type fbody struct {
	text      string
	line, col int
}

// pos is the source position of text[i].
func (b fbody) pos(i int) (int, int) {
	nl := strings.Count(b.text[:i], "\n")
	if nl == 0 {
		return b.line, b.col + i
	}
	return b.line + nl, i - strings.LastIndexByte(b.text[:i], '\n') - 1
}

func (b fbody) place(n *node, from, to int) *node {
	n.lineno, n.col = b.pos(from)
	n.endLineno, n.endCol = b.pos(to)
	n.hasPos = true
	return n
}

// fstringBody parses the body of an f-string (or of a format spec, at
// depth 1) into Constant and FormattedValue nodes.
func (p *parser) fstringBody(text string, raw bool, tok token, depth int, line, col int) ([]*node, *syntaxErr) {
	body := fbody{text: text, line: line, col: col}
	var out []*node
	var lit strings.Builder
	litStart := 0
	flush := func(end int) *syntaxErr {
		if lit.Len() == 0 {
			return nil
		}
		s, err := decodeStrBody(lit.String(), raw, false)
		if err != nil {
			return &syntaxErr{msg: err.Error(), line: tok.line}
		}
		out = append(out, body.place(&node{kind: "Constant", value: strV(s)}, litStart, end))
		lit.Reset()
		return nil
	}
	i := 0
	for i < len(text) {
		if lit.Len() == 0 {
			litStart = i
		}
		c := text[i]
		if c == '\\' && !raw && i+1 < len(text) {
			if text[i+1] == 'N' && i+2 < len(text) && text[i+2] == '{' {
				end := strings.IndexByte(text[i:], '}')
				if end < 0 {
					end = len(text) - i - 1
				}
				lit.WriteString(text[i : i+end+1])
				i += end + 1
				continue
			}
			lit.WriteString(text[i : i+2])
			i += 2
			continue
		}
		if c == '{' {
			if depth == 0 && i+1 < len(text) && text[i+1] == '{' {
				lit.WriteByte('{')
				i += 2
				continue
			}
			if err := flush(i); err != nil {
				return nil, err
			}
			fv, next, err := p.fstringField(body, i, raw, tok, depth)
			if err != nil {
				return nil, err
			}
			out = append(out, fv...)
			i = next
			continue
		}
		if c == '}' {
			if depth == 0 && i+1 < len(text) && text[i+1] == '}' {
				lit.WriteByte('}')
				i += 2
				continue
			}
			return nil, p.fail("f-string: single '}' is not allowed", tok)
		}
		lit.WriteByte(c)
		i++
	}
	if err := flush(len(text)); err != nil {
		return nil, err
	}
	return out, nil
}

// fstringField parses the replacement field starting at body.text[start]
// ('{'). It returns the debug text Constant (for `=`) and the
// FormattedValue, and the index after the closing '}'.
func (p *parser) fstringField(b fbody, start int, raw bool, tok token, depth int) ([]*node, int, *syntaxErr) {
	body := b.text
	i := start + 1
	exprStart := i
	nest := 0
	exprEnd := -1
	for i < len(body) {
		c := body[i]
		switch {
		case c == '\'' || c == '"':
			q := string(c)
			if strings.HasPrefix(body[i:], strings.Repeat(q, 3)) {
				q = strings.Repeat(q, 3)
			}
			j := strings.Index(body[i+len(q):], q)
			if j < 0 {
				return nil, 0, p.fail("f-string: unterminated string", tok)
			}
			i += len(q) + j + len(q)
			continue
		case c == '(' || c == '[' || c == '{':
			nest++
		case c == ')' || c == ']':
			nest--
		case c == '}':
			if nest == 0 {
				exprEnd = i
			} else {
				nest--
			}
		case nest == 0 && c == '!' && (i+1 >= len(body) || body[i+1] != '='):
			exprEnd = i
		case nest == 0 && c == ':':
			exprEnd = i
		case nest == 0 && c == '=':
			prev := byte(0)
			if i > exprStart {
				prev = body[i-1]
			}
			if (i+1 >= len(body) || body[i+1] != '=') && !strings.ContainsRune("=!<>:", rune(prev)) {
				exprEnd = i
			} else if i+1 < len(body) && body[i+1] == '=' {
				i++
			}
		}
		if exprEnd >= 0 {
			break
		}
		i++
	}
	if exprEnd < 0 {
		return nil, 0, p.fail("f-string: expecting '}'", tok)
	}
	exprText := body[exprStart:exprEnd]
	if strings.TrimSpace(exprText) == "" {
		return nil, 0, p.fail("f-string: valid expression required before '"+string(body[exprEnd])+"'", tok)
	}
	eline, ecol := b.pos(exprStart)
	expr, err := p.parseFieldExpr(exprText, eline, ecol)
	if err != nil {
		return nil, 0, err
	}
	fv := &node{kind: "FormattedValue", a: expr, conversion: -1}
	var out []*node
	i = exprEnd
	debug := false
	if body[i] == '=' {
		debug = true
		i++
		for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n') {
			i++
		}
		out = append(out, b.place(&node{kind: "Constant", value: strV(body[exprStart:i])}, exprStart, i))
	}
	if i < len(body) && body[i] == '!' {
		if i+1 >= len(body) {
			return nil, 0, p.fail("f-string: missing conversion character", tok)
		}
		switch body[i+1] {
		case 's':
			fv.conversion = 115
		case 'r':
			fv.conversion = 114
		case 'a':
			fv.conversion = 97
		default:
			return nil, 0, p.fail("f-string: invalid conversion character", tok)
		}
		i += 2
	}
	if i < len(body) && body[i] == ':' {
		colon := i
		i++
		specStart := i
		nest := 0
		for i < len(body) {
			if body[i] == '{' {
				nest++
			} else if body[i] == '}' {
				if nest == 0 {
					break
				}
				nest--
			}
			i++
		}
		sl, sc := b.pos(specStart)
		spec, err := p.fstringBody(body[specStart:i], raw, tok, depth+1, sl, sc)
		if err != nil {
			return nil, 0, err
		}
		fv.b = b.place(&node{kind: "JoinedStr", l1: spec}, colon, i)
	} else if debug && fv.conversion == -1 {
		fv.conversion = 114
	}
	if i >= len(body) || body[i] != '}' {
		return nil, 0, p.fail("f-string: expecting '}'", tok)
	}
	out = append(out, b.place(fv, start, i+1))
	return out, i + 1, nil
}

// parseFieldExpr parses the expression of a replacement field whose first
// character is at (line, col), placing its nodes at their source positions.
func (p *parser) parseFieldExpr(text string, line, col int) (*node, *syntaxErr) {
	toks, err := tokenize("(" + text + ")")
	if err != nil {
		return nil, &syntaxErr{msg: "f-string: " + err.msg, line: line + err.line - 1}
	}
	// drop the parentheses and the trailing NEWLINE/EOF, shift positions
	inner := toks[1:]
	for k := range inner {
		t := &inner[k]
		if t.line == 1 {
			t.col += col - 1
		}
		if t.endLine == 1 {
			t.endCol += col - 1
		}
		t.line += line - 1
		t.endLine += line - 1
	}
	sub := &parser{toks: inner, src: p.src}
	e, serr := sub.starExpressionsOrYield()
	if serr != nil {
		return nil, serr
	}
	if !sub.isOp(")") {
		return nil, sub.fail("f-string: expecting '}'", sub.peek())
	}
	return e, nil
}
