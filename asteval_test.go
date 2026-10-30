// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT

package asteval_test

import (
	"bytes"
	"strings"
	"testing"

	asteval "github.com/lmfit/asteval"
)

func newInterp(t *testing.T, mod func(*asteval.Options)) (*asteval.Interpreter, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var w, ew bytes.Buffer
	o := asteval.DefaultOptions()
	o.Writer, o.ErrWriter = &w, &ew
	if mod != nil {
		mod(&o)
	}
	return asteval.New(o), &w, &ew
}

func repr(t *testing.T, v asteval.Value) string {
	t.Helper()
	s, err := asteval.Repr(v)
	if err != nil {
		t.Fatalf("repr failed: %v", err)
	}
	return s
}

// TestResults checks the value eval returns for programs whose results the
// original interpreter was observed to return.
func TestResults(t *testing.T) {
	cases := []struct{ src, want string }{
		{"1 + 2 * 3", "7"},
		{"-2 ** 2", "-4"},
		{"7 // -2, 7 % -2, divmod(-7.5, 2)", "(-4, -1, (-4.0, 0.5))"},
		{"0.1 + 0.2", "0.30000000000000004"},
		{"2 ** 100", "1267650600228229401496703205376"},
		{"1 / 3", "0.3333333333333333"},
		{"(-8) ** (1 / 3)", "(1.0000000000000002+1.7320508075688772j)"},
		{"round(2.675, 2), round(2.5), round(1250, -2)", "(2.67, 2, 1200)"},
		{"pow(2, 3)", "8.0"},
		{"enumerate([5, 6])", "[(0, 5), (1, 6)]"},
		{"{**{'a': 1}}", "{None: {'a': 1}}"},
		{"{1: 'a', 1.0: 'b', True: 'c'}", "{1: 'c'}"},
		{"'ß'.upper(), 'İ'.lower(), 'ǅ'.title()", "('SS', 'i̇', 'ǅ')"},
		{"'%5.2f|%-4d|%r' % (3.14159, 7, 'x')", "\" 3.14|7   |'x'\""},
		{"f'{255:#x} {1234567:,} {0.5:.1%}'", "'0xff 1,234,567 50.0%'"},
		{"format(1+2j, '.2e')", "'1.00e+00+2.00e+00j'"},
		{"{5, 13, 21, 29}", "{29, 13, 21, 5}"},
		{"x = [1]\nb = x\nx += [2]\n(x, b)", "([1, 2], [1])"},
		{"dir()", "['args', 'func', 'keywords', 'kwargs', 'node', 'self', 'starargs']"},
		{"def g(*args, **kw):\n    return args, kw\ng(1, 2, k=3)", "((1, 2), {'k': 3})"},
		{"def g(*args, **kw):\n    return args, kw\ng", "<Procedure g(*args)>"},
		{"x = 'outer'\n[x for x in range(3)]\nx", "'outer'"},
	}
	for _, c := range cases {
		in, _, _ := newInterp(t, nil)
		v, err := in.Eval(c.src, 0, true, false)
		if err != nil {
			t.Errorf("%q: unexpected exception %v", c.src, err)
			continue
		}
		if errs := in.Errors(); len(errs) > 0 {
			_, text := errs[0].GetError()
			t.Errorf("%q: unexpected error record %q", c.src, text)
			continue
		}
		if got := repr(t, v); got != c.want {
			t.Errorf("%q:\n got  %s\n want %s", c.src, got, c.want)
		}
	}
}

// TestErrorRecords checks the error list after an eval: one get_error()
// text per entry.
func TestErrorRecords(t *testing.T) {
	cases := []struct {
		src  string
		want []string
	}{
		{"1/0", []string{"1/0\nZeroDivisionError: division by zero"}},
		{"x = [1,2]\ny = x[5]", []string{"y = x[5]\n    ^^^^\nIndexError: list index out of range"}},
		{"foo", []string{"foo\nNameError: name 'foo' is not defined"}},
		{"d = {}\nd[\"a\"]", []string{"d[\"a\"]\nKeyError: 'a'", "d[\"a\"]\nKeyError: ''"}},
		{"def f(a, b=2):\n    c = a + b\n    return c / 0\nf(1)", []string{
			"    return c / 0\n           ^^^^\nZeroDivisionError: division by zero",
			"f(1)\nZeroDivisionError: Error running function 'f' with args '[1]' and kwargs {}: ",
		}},
		{"raise ValueError(\"bad\", 1)", []string{"\nValueError:  ", "raise ValueError(\"bad\", 1)\nValueError:  "}},
		{"try:\n    raise ValueError('x')\nexcept ValueError as e:\n    pass", []string{
			"\nValueError: x",
			"try:\n    raise ValueError('x')\nexcept ValueError as e:\n    pass\nTypeError: 'NoneType' object is not callable",
		}},
		{"obj = [1]\nobj.x += 1", []string{"obj.x += 1\nRuntimeError: attribute for storage: shouldn't be here!"}},
		{"import os", []string{"import os\nNotImplementedError: Import not supported", "import os\nNotImplementedError: Import not supported"}},
		{"'{}'.format(1)", []string{"'{}'.format(1)\nAttributeError: no safe attribute 'format' for '{}'"}},
		{"(1).foo", []string{"(1).foo\nAttributeError: 'int' object has no attribute 'foo'"}},
		{"def f(**k):\n    return k\nf(a=1, a=2)", []string{"f(a=1, a=2)\nSyntaxError: keyword argument repeated: a"}},
	}
	for _, c := range cases {
		in, _, ew := newInterp(t, nil)
		if _, err := in.Eval(c.src, 0, true, false); err != nil {
			t.Errorf("%q: unexpected exception %v", c.src, err)
		}
		if ew.Len() != 0 {
			t.Errorf("%q: a runtime error printed %q to err_writer", c.src, ew.String())
		}
		var got []string
		for _, e := range in.Errors() {
			_, text := e.GetError()
			got = append(got, text)
		}
		if strings.Join(got, "\n--\n") != strings.Join(c.want, "\n--\n") {
			t.Errorf("%q:\n got  %q\n want %q", c.src, got, c.want)
		}
	}
}

// TestParseErrorIsPrinted checks that a syntax error is written to
// err_writer and raised when raise_errors is set.
func TestParseErrorIsPrinted(t *testing.T) {
	in, _, ew := newInterp(t, nil)
	if _, err := in.Eval("x = (1 +", 0, true, false); err != nil {
		t.Fatalf("unexpected exception %v", err)
	}
	want := "x = (1 +\nSyntaxError: '(' was never closed (<unknown>, line 1)\n"
	if ew.String() != want {
		t.Errorf("err_writer got %q, want %q", ew.String(), want)
	}
	_, err := in.Eval("1 +", 0, true, true)
	exc, ok := err.(*asteval.Exception)
	if !ok || exc.Class != "SyntaxError" {
		t.Errorf("raise_errors: got %v, want a SyntaxError", err)
	}
}

// TestPrinter checks print's sep and end and its writer.
func TestPrinter(t *testing.T) {
	in, w, _ := newInterp(t, nil)
	if _, err := in.Eval("print(1, 2, sep='-')\nprint('a', end='')", 0, true, false); err != nil {
		t.Fatal(err)
	}
	if w.String() != "1-2\na" {
		t.Errorf("writer got %q", w.String())
	}
	in, w, _ = newInterp(t, func(o *asteval.Options) { o.Minimal = true })
	if _, err := in.Eval("print('hidden')", 0, true, false); err != nil {
		t.Fatal(err)
	}
	if w.Len() != 0 {
		t.Errorf("minimal interpreter printed %q", w.String())
	}
}

// TestSymtable checks user symbols, read-only names and the names a run
// adds.
func TestSymtable(t *testing.T) {
	in, _, _ := newInterp(t, func(o *asteval.Options) {
		o.UserSymbols = []asteval.Symbol{{Name: "k", Value: asteval.Int(4)}}
		o.ReadonlySymbols = []string{"k"}
	})
	if _, err := in.Eval("x = k * 2\nk = 1", 0, true, false); err != nil {
		t.Fatal(err)
	}
	if v, ok := in.Lookup("x"); !ok || repr(t, v) != "8" {
		t.Errorf("x = %v, %v", v, ok)
	}
	errs := in.Errors()
	if len(errs) == 0 || errs[0].Class() != "NameError" || errs[0].Msg() != "invalid symbol name (reserved word?) k" {
		t.Errorf("assigning a read-only name: got %v", errs)
	}
	if got := strings.Join(in.UserDefinedSymbols(), ","); !strings.Contains(got, "x") {
		t.Errorf("user defined symbols %q lack x", got)
	}
}

// TestNames checks valid_symbol_name and get_ast_names.
func TestNames(t *testing.T) {
	for name, want := range map[string]bool{"a": true, "_b1": true, "1a": false, "eval": false, "if": false, "a b": false} {
		if got := asteval.ValidSymbolName(name); got != want {
			t.Errorf("ValidSymbolName(%q) = %v, want %v", name, got, want)
		}
	}
	in, _, _ := newInterp(t, nil)
	tree, err := in.Parse("[q for q in w if q > m]")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(asteval.GetASTNames(tree), ","); got != "q,w,m" {
		t.Errorf("GetASTNames = %q, want q,w,m", got)
	}
}
