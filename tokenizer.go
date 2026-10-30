// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT
//
// Go port of asteval. asteval hands source text to CPython's ast.parse; this
// file is the tokenizer half of that parser, for the Python 3.12 grammar the
// interpreter accepts. Positions follow CPython: lines count from 1 and
// columns are UTF-8 byte offsets within the line.

package asteval

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type tokKind int

const (
	tkEOF tokKind = iota
	tkName
	tkNumber
	tkString
	tkOp
	tkNewline
	tkIndent
	tkDedent
	// tkError ends the stream where the tokenizer failed; the parser raises
	// the tokenizer's error only if it reads that far, as CPython's lazy
	// tokenizer does.
	tkError
)

// token is one token of the source. For a string token, text is the whole
// literal including its prefix and quotes.
type token struct {
	kind    tokKind
	text    string
	line    int
	col     int
	endLine int
	endCol  int
}

// syntaxErr is a parse failure: CPython's message and the line it reports.
type syntaxErr struct {
	msg    string
	line   int
	indent bool // an IndentationError rather than a plain SyntaxError
}

func (e *syntaxErr) Error() string { return e.msg }

type tokenizer struct {
	src     string
	lines   []string // each line with its terminating newline, if any
	toks    []token
	indents []int
	parens  []parenOpen
}

type parenOpen struct {
	ch   byte
	line int
	col  int
}

var threeCharOps = []string{"**=", "//=", ">>=", "<<=", "...", "!="}
var twoCharOps = []string{"**", "//", ">>", "<<", "<=", ">=", "==", "!=", "->", "+=", "-=", "*=", "/=", "%=",
	"&=", "|=", "^=", "@=", ":="}

const oneCharOps = "+-*/%@&|^~<>()[]{},:;.=!"

func splitLines(src string) []string {
	var out []string
	start := 0
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\n':
			out = append(out, src[start:i+1])
			start = i + 1
		case '\r':
			end := i + 1
			if end < len(src) && src[end] == '\n' {
				end++
			}
			out = append(out, src[start:end])
			start = end
			i = end - 1
		}
	}
	if start < len(src) {
		out = append(out, src[start:])
	}
	return out
}

// tokenize splits src into tokens. Where the tokenizer itself fails, the
// stream ends with a tkError token and the error is returned with it.
func tokenize(src string) ([]token, *syntaxErr) {
	t := &tokenizer{src: src, lines: splitLines(src), indents: []int{0}}
	if err := t.run(); err != nil {
		t.toks = append(t.toks, token{kind: tkError, line: err.line})
		return t.toks, err
	}
	return t.toks, nil
}

func (t *tokenizer) emit(k tokKind, text string, line, col, eline, ecol int) {
	t.toks = append(t.toks, token{kind: k, text: text, line: line, col: col, endLine: eline, endCol: ecol})
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || unicode.Is(unicode.Other_ID_Start, r)
}

func isIdentContinue(r rune) bool {
	return isIdentStart(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) ||
		unicode.Is(unicode.Nd, r) || unicode.Is(unicode.Pc, r) || unicode.Is(unicode.Other_ID_Continue, r)
}

func (t *tokenizer) lastSignificant() *token {
	if len(t.toks) == 0 {
		return nil
	}
	return &t.toks[len(t.toks)-1]
}

func (t *tokenizer) run() *syntaxErr {
	lineno := 0
	contLine := false // the previous physical line ended with a backslash
	for li := 0; li < len(t.lines); li++ {
		raw := t.lines[li]
		lineno = li + 1
		line := strings.TrimRight(raw, "\r\n")
		pos := 0
		if len(t.parens) == 0 && !contLine {
			// Measure indentation at the start of a logical line.
			col := 0
			for pos < len(line) {
				c := line[pos]
				if c == ' ' {
					col++
				} else if c == '\t' {
					col = (col/8 + 1) * 8
				} else if c == '\f' {
					col = 0
				} else {
					break
				}
				pos++
			}
			if pos == len(line) || line[pos] == '#' {
				continue // blank or comment-only line
			}
			if line[pos] == '\\' && pos == len(line)-1 {
				// a lone continuation; handled below as part of the line
			}
			cur := t.indents[len(t.indents)-1]
			if col > cur {
				t.indents = append(t.indents, col)
				t.emit(tkIndent, "", lineno, 0, lineno, pos)
			} else if col < cur {
				for len(t.indents) > 1 && t.indents[len(t.indents)-1] > col {
					t.indents = t.indents[:len(t.indents)-1]
					t.emit(tkDedent, "", lineno, pos, lineno, pos)
				}
				if t.indents[len(t.indents)-1] != col {
					return &syntaxErr{msg: "unindent does not match any outer indentation level", line: lineno, indent: true}
				}
			}
		}
		contLine = false
		for pos < len(line) {
			c := line[pos]
			switch {
			case c == ' ' || c == '\t' || c == '\f':
				pos++
			case c == '#':
				pos = len(line)
			case c == '\\':
				if pos != len(line)-1 {
					return &syntaxErr{msg: "unexpected character after line continuation character", line: lineno}
				}
				if li == len(t.lines)-1 {
					return &syntaxErr{msg: "unexpected EOF while parsing", line: lineno}
				}
				contLine = true
				pos++
			default:
				np, li2, err := t.token(li, pos)
				if err != nil {
					return err
				}
				if li2 != li {
					// a triple-quoted string spanning lines
					li = li2
					raw = t.lines[li]
					line = strings.TrimRight(raw, "\r\n")
					lineno = li + 1
				}
				pos = np
			}
		}
		if !contLine && len(t.parens) == 0 {
			if last := t.lastSignificant(); last != nil && last.kind != tkNewline && last.kind != tkIndent &&
				last.kind != tkDedent {
				t.emit(tkNewline, "", lineno, len(line), lineno, len(line)+1)
			}
		}
	}
	if len(t.parens) > 0 {
		p := t.parens[len(t.parens)-1]
		return &syntaxErr{msg: "'" + string(p.ch) + "' was never closed", line: p.line}
	}
	if last := t.lastSignificant(); last != nil && last.kind != tkNewline && last.kind != tkDedent {
		t.emit(tkNewline, "", lineno, 0, lineno, 1)
	}
	for len(t.indents) > 1 {
		t.indents = t.indents[:len(t.indents)-1]
		t.emit(tkDedent, "", lineno+1, 0, lineno+1, 0)
	}
	t.emit(tkEOF, "", lineno+1, 0, lineno+1, 0)
	return nil
}

// expectIndent reports whether the last logical line ended with ':' (a block
// opener whose body must be indented). The parser makes the final decision;
// this only catches the indentation-level errors CPython's tokenizer reports.
func (t *tokenizer) expectIndent() bool {
	n := len(t.toks)
	if n < 2 || t.toks[n-1].kind != tkNewline {
		return false
	}
	prev := t.toks[n-2]
	return prev.kind == tkOp && prev.text == ":" && t.blockOpener(n-2)
}

// blockOpener reports whether the ':' at index i ends a compound statement
// header (rather than, say, a lambda or a dict on a line of its own).
func (t *tokenizer) blockOpener(i int) bool {
	j := i
	for j > 0 && t.toks[j-1].kind != tkNewline && t.toks[j-1].kind != tkIndent && t.toks[j-1].kind != tkDedent {
		j--
	}
	first := t.toks[j]
	if first.kind != tkName {
		return false
	}
	switch first.text {
	case "if", "elif", "else", "while", "for", "try", "except", "finally", "with", "def", "class", "async":
		return true
	}
	return false
}

// token scans one token at (li, pos) and returns the position after it and
// the line it ends on.
func (t *tokenizer) token(li, pos int) (int, int, *syntaxErr) {
	line := strings.TrimRight(t.lines[li], "\r\n")
	lineno := li + 1
	c := line[pos]
	r, size := utf8.DecodeRuneInString(line[pos:])
	// string literals, possibly prefixed
	if c == '"' || c == '\'' {
		return t.str(li, pos, pos)
	}
	if isIdentStart(r) {
		end := pos + size
		for end < len(line) {
			r2, s2 := utf8.DecodeRuneInString(line[end:])
			if !isIdentContinue(r2) {
				break
			}
			end += s2
		}
		word := line[pos:end]
		if end < len(line) && (line[end] == '"' || line[end] == '\'') && isStrPrefix(word) {
			return t.str(li, pos, end)
		}
		t.emit(tkName, word, lineno, pos, lineno, end)
		return end, li, nil
	}
	if c >= '0' && c <= '9' || c == '.' && pos+1 < len(line) && line[pos+1] >= '0' && line[pos+1] <= '9' {
		end, err := scanNumber(line, pos)
		if err != "" {
			return 0, 0, &syntaxErr{msg: err, line: lineno}
		}
		t.emit(tkNumber, line[pos:end], lineno, pos, lineno, end)
		return end, li, nil
	}
	for _, op := range threeCharOps {
		if strings.HasPrefix(line[pos:], op) {
			t.emit(tkOp, op, lineno, pos, lineno, pos+3)
			return pos + 3, li, nil
		}
	}
	for _, op := range twoCharOps {
		if strings.HasPrefix(line[pos:], op) {
			t.emit(tkOp, op, lineno, pos, lineno, pos+2)
			return pos + 2, li, nil
		}
	}
	if strings.IndexByte(oneCharOps, c) >= 0 {
		switch c {
		case '(', '[', '{':
			t.parens = append(t.parens, parenOpen{c, lineno, pos})
		case ')', ']', '}':
			want := map[byte]byte{')': '(', ']': '[', '}': '{'}[c]
			if len(t.parens) == 0 {
				return 0, 0, &syntaxErr{msg: "unmatched '" + string(c) + "'", line: lineno}
			}
			open := t.parens[len(t.parens)-1]
			if open.ch != want {
				msg := "closing parenthesis '" + string(c) + "' does not match opening parenthesis '" + string(open.ch) + "'"
				if open.line != lineno {
					msg += " on line " + itoa(open.line)
				}
				return 0, 0, &syntaxErr{msg: msg, line: lineno}
			}
			t.parens = t.parens[:len(t.parens)-1]
		case '!':
			return 0, 0, &syntaxErr{msg: "invalid syntax", line: lineno}
		}
		t.emit(tkOp, string(c), lineno, pos, lineno, pos+1)
		return pos + 1, li, nil
	}
	if c == '$' || c == '?' || c == '`' {
		return 0, 0, &syntaxErr{msg: "invalid syntax", line: lineno}
	}
	return 0, 0, &syntaxErr{msg: "invalid character '" + string(r) + "' (U+" + hexUpper(int(r), 4) + ")", line: lineno}
}

func isStrPrefix(w string) bool {
	switch strings.ToLower(w) {
	case "r", "u", "b", "f", "br", "rb", "fr", "rf":
		return true
	}
	return false
}

// str scans a string literal whose prefix starts at start and whose quote is
// at q. f-strings are scanned with their replacement fields, so quotes inside
// a field do not end the literal (PEP 701).
func (t *tokenizer) str(li, start, q int) (int, int, *syntaxErr) {
	line := t.lines[li]
	prefix := strings.ToLower(line[start:q])
	raw := strings.Contains(prefix, "r")
	isF := strings.Contains(prefix, "f")
	quote := line[q]
	triple := strings.HasPrefix(line[q:], strings.Repeat(string(quote), 3))
	qlen := 1
	if triple {
		qlen = 3
	}
	startLine := li
	var b strings.Builder
	b.WriteString(line[start : q+qlen])
	i := q + qlen
	depth := 0 // brace depth inside an f-string
	for {
		if i >= len(line) {
			if !triple && depth == 0 {
				if isF {
					return 0, 0, &syntaxErr{msg: "unterminated f-string literal (detected at line " + itoa(li+1) + ")", line: startLine + 1}
				}
				return 0, 0, &syntaxErr{msg: "unterminated string literal (detected at line " + itoa(li+1) + ")", line: startLine + 1}
			}
			li++
			if li >= len(t.lines) {
				if isF {
					return 0, 0, &syntaxErr{msg: "unterminated triple-quoted f-string literal (detected at line " + itoa(li) + ")", line: startLine + 1}
				}
				return 0, 0, &syntaxErr{msg: "unterminated triple-quoted string literal (detected at line " + itoa(li) + ")", line: startLine + 1}
			}
			line = t.lines[li]
			i = 0
			continue
		}
		c := line[i]
		if depth == 0 {
			if c == '\\' && (!raw || i+1 < len(line)) {
				if i+1 < len(line) && (line[i+1] == '\n' || line[i+1] == '\r') && !triple {
					// backslash-newline inside a single-quoted string continues it
				}
				b.WriteByte(c)
				if i+1 < len(line) {
					b.WriteByte(line[i+1])
				}
				i += 2
				continue
			}
			if c == quote && (!triple || strings.HasPrefix(line[i:], strings.Repeat(string(quote), 3))) {
				b.WriteString(line[i : i+qlen])
				i += qlen
				break
			}
			if (c == '\n' || c == '\r') && !triple {
				if isF {
					return 0, 0, &syntaxErr{msg: "unterminated f-string literal (detected at line " + itoa(li+1) + ")", line: startLine + 1}
				}
				return 0, 0, &syntaxErr{msg: "unterminated string literal (detected at line " + itoa(li+1) + ")", line: startLine + 1}
			}
			if isF && c == '{' {
				if i+1 < len(line) && line[i+1] == '{' {
					b.WriteString("{{")
					i += 2
					continue
				}
				depth = 1
			}
			b.WriteByte(c)
			i++
			continue
		}
		// inside a replacement field: track nesting and skip nested strings
		switch c {
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			depth--
		case '\'', '"':
			// a nested string literal inside the field
			end := i + 1
			trip := strings.HasPrefix(line[i:], strings.Repeat(string(c), 3))
			if trip {
				end = i + 3
			}
			for end < len(line) {
				if line[end] == '\\' {
					end += 2
					continue
				}
				if trip && strings.HasPrefix(line[end:], strings.Repeat(string(c), 3)) {
					end += 3
					break
				}
				if !trip && line[end] == c {
					end++
					break
				}
				end++
			}
			b.WriteString(line[i:min(end, len(line))])
			i = end
			continue
		}
		b.WriteByte(c)
		i++
	}
	text := b.String()
	t.emit(tkString, text, startLine+1, start, li+1, i)
	return i, li, nil
}

// scanNumber returns the end of the numeric literal at pos, or CPython's
// message for a malformed one.
func scanNumber(line string, pos int) (int, string) {
	i := pos
	isDigit := func(c byte, base int) bool {
		switch base {
		case 2:
			return c == '0' || c == '1'
		case 8:
			return c >= '0' && c <= '7'
		case 16:
			return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
		}
		return c >= '0' && c <= '9'
	}
	digits := func(base int) string {
		start := i
		for i < len(line) {
			if isDigit(line[i], base) {
				i++
			} else if line[i] == '_' && i > start && i+1 < len(line) && isDigit(line[i+1], base) {
				i++
			} else if line[i] == '_' {
				return "invalid decimal literal"
			} else {
				break
			}
		}
		return ""
	}
	if line[i] == '0' && i+1 < len(line) && strings.IndexByte("xXoObB", line[i+1]) >= 0 {
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[line[i+1]]
		i += 2
		if i < len(line) && line[i] == '_' {
			i++
		}
		start := i
		if e := digits(base); e != "" {
			return 0, e
		}
		if i == start {
			return 0, "invalid " + map[int]string{16: "hexadecimal", 8: "octal", 2: "binary"}[base] + " literal"
		}
		if i < len(line) && (line[i] >= '0' && line[i] <= '9' || isIdentStart(rune(line[i]))) {
			return 0, "invalid " + map[int]string{16: "hexadecimal", 8: "octal", 2: "binary"}[base] + " literal"
		}
		return i, ""
	}
	if e := digits(10); e != "" {
		return 0, e
	}
	isFloat := false
	if i < len(line) && line[i] == '.' {
		isFloat = true
		i++
		if i < len(line) && line[i] >= '0' && line[i] <= '9' {
			if e := digits(10); e != "" {
				return 0, e
			}
		}
	}
	if i < len(line) && (line[i] == 'e' || line[i] == 'E') {
		j := i + 1
		if j < len(line) && (line[j] == '+' || line[j] == '-') {
			j++
		}
		if j < len(line) && line[j] >= '0' && line[j] <= '9' {
			i = j
			isFloat = true
			if e := digits(10); e != "" {
				return 0, e
			}
		} else {
			return 0, "invalid decimal literal"
		}
	}
	if i < len(line) && (line[i] == 'j' || line[i] == 'J') {
		i++
	} else if !isFloat && i-pos > 1 && line[pos] == '0' && strings.Trim(line[pos:i], "0_") != "" {
		return 0, "leading zeros in decimal integer literals are not permitted; use an 0o prefix for octal integers"
	}
	if i < len(line) && isIdentStart(rune(line[i])) {
		return 0, "invalid decimal literal"
	}
	return i, ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func hexUpper(n, width int) string {
	const digits = "0123456789ABCDEF"
	var b []byte
	for n > 0 || len(b) < width {
		b = append([]byte{digits[n%16]}, b...)
		n /= 16
	}
	return string(b)
}
