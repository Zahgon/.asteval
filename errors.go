// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT
//
// Go port of asteval. Python exceptions: the classes asteval exposes, their
// hierarchy, and exception instances as Python values.

package asteval

import (
	"errors"
	"fmt"
)

// excV is a Python exception instance. It is both a Value (it can be bound
// by `except ... as e`, printed and inspected) and a Go error (it is what a
// Python operation raises).
type excV struct {
	Class string
	// Message is str() of the exception.
	Message string
	args    []Value
	// attrs holds attributes set on the instance.
	attrs map[string]Value
}

func (e *excV) Error() string { return e.Class + ": " + e.Message }

// newPyErr returns an exception raised with a single message argument.
func newPyErr(class, msg string) *excV {
	return &excV{Class: class, Message: msg, args: []Value{strV(msg)}}
}

// newExc returns an exception built as Class(*args) is in Python.
func newExc(class string, args []Value) *excV {
	e := &excV{Class: class, args: args}
	e.Message = excStr(class, args)
	return e
}

// excStr is BaseException.__str__ (KeyError's included).
func excStr(class string, args []Value) string {
	switch len(args) {
	case 0:
		return ""
	case 1:
		if subclass(class, "KeyError") {
			return reprOf(args[0])
		}
		s, err := pyStr(args[0])
		if err != nil {
			return ""
		}
		return s
	}
	s, _ := pyRepr(newTuple(args))
	return s
}

// excParents is the class hierarchy of the exceptions asteval puts in its
// symbol table (FROM_PY), plus those Python raises inside it.
var excParents = map[string]string{
	"BaseException":         "",
	"Exception":             "BaseException",
	"KeyboardInterrupt":     "BaseException",
	"SystemExit":            "BaseException",
	"GeneratorExit":         "BaseException",
	"ArithmeticError":       "Exception",
	"FloatingPointError":    "ArithmeticError",
	"OverflowError":         "ArithmeticError",
	"ZeroDivisionError":     "ArithmeticError",
	"AssertionError":        "Exception",
	"AttributeError":        "Exception",
	"BufferError":           "Exception",
	"EOFError":              "Exception",
	"ImportError":           "Exception",
	"ModuleNotFoundError":   "ImportError",
	"LookupError":           "Exception",
	"IndexError":            "LookupError",
	"KeyError":              "LookupError",
	"MemoryError":           "Exception",
	"NameError":             "Exception",
	"UnboundLocalError":     "NameError",
	"OSError":               "Exception",
	"ReferenceError":        "Exception",
	"RuntimeError":          "Exception",
	"NotImplementedError":   "RuntimeError",
	"RecursionError":        "RuntimeError",
	"StopIteration":         "Exception",
	"SyntaxError":           "Exception",
	"IndentationError":      "SyntaxError",
	"TabError":              "IndentationError",
	"SystemError":           "Exception",
	"TypeError":             "Exception",
	"ValueError":            "Exception",
	"UnicodeError":          "ValueError",
	"UnicodeDecodeError":    "UnicodeError",
	"UnicodeEncodeError":    "UnicodeError",
	"UnicodeTranslateError": "UnicodeError",
	"Warning":               "Exception",
	"BytesWarning":          "Warning",
	"DeprecationWarning":    "Warning",
	"ImportWarning":         "Warning",
	"RuntimeWarning":        "Warning",
	"SyntaxWarning":         "Warning",
	"UnicodeWarning":        "Warning",
}

// excAliases are the names FROM_PY binds to another class's object.
var excAliases = map[string]string{"EnvironmentError": "OSError", "IOError": "OSError"}

func subclass(class, of string) bool {
	for c := class; c != ""; c = excParents[c] {
		if c == of {
			return true
		}
	}
	return false
}

// isExc reports whether err is a Python exception of one of the classes.
func isExc(err error, classes ...string) bool {
	var e *excV
	if !errors.As(err, &e) {
		return false
	}
	for _, c := range classes {
		if subclass(e.Class, c) {
			return true
		}
	}
	return false
}

// asExc returns err as an exception, wrapping a foreign Go error as
// SystemError (it would be a bug in this package).
func asExc(err error) *excV {
	var e *excV
	if errors.As(err, &e) {
		return e
	}
	return newPyErr("SystemError", err.Error())
}

func typeErr(format string, args ...any) *excV {
	return newPyErr("TypeError", fmt.Sprintf(format, args...))
}

func valueErr(format string, args ...any) *excV {
	return newPyErr("ValueError", fmt.Sprintf(format, args...))
}
