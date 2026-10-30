ASTEVAL
=======

A Go port of `asteval <https://github.com/lmfit/asteval>`_, the safe(ish)
evaluator of a subset of Python. ``asteval`` parses Python 3 source text into a
syntax tree and runs that tree with its own interpreter, against a symbol
table of Python values. It never hands the code to a Python interpreter, so
names such as ``eval``, ``exec`` and ``__import__`` simply do not exist, and
dunder attributes, ``str.format`` and imports are refused.

This module keeps the behaviour of the Python package at version 1.0.10+
(commit ``57f3fbb2``) under CPython 3.12, including what it inherits from
CPython: the Python 3.12 grammar and node positions, integers of any size,
``repr()`` and ``str()`` of every value, ``%``-formatting, ``format()`` and
f-strings, the ``str`` methods with Unicode 15.0 data, the builtins and
``math`` functions asteval exposes, and CPython's exception messages. NumPy
support and file access are not part of the port.

Installation
------------

::

    go get github.com/lmfit/asteval

The module has no dependencies outside the Go standard library.

Usage
-----

.. code-block:: go

    package main

    import (
        "fmt"
        "os"

        asteval "github.com/lmfit/asteval"
    )

    func main() {
        opts := asteval.DefaultOptions()
        opts.Writer = os.Stdout
        aeval := asteval.New(opts)
        aeval.Eval("x = 3.0 * 4", 0, true, false)
        v, _ := aeval.Eval("sqrt(x)", 0, true, false)
        s, _ := asteval.Repr(v)
        fmt.Println(s) // 3.4641016151377544

        // Errors are recorded as asteval records them.
        aeval.Eval("1 / 0", 0, true, false)
        for _, e := range aeval.Errors() {
            _, text := e.GetError()
            fmt.Println(text)
        }
    }

``Eval`` is ``Interpreter.eval(expr, lineno, show_errors, raise_errors)``;
``Parse`` and ``Run`` are ``Interpreter.parse`` and ``Interpreter.run``;
``Errors`` is the interpreter's error list, each entry an ``ExceptionHolder``
with its ``get_error()`` text. ``Options`` holds the constructor arguments:
``nested_symtable``, ``user_symbols``, ``writer``, ``err_writer``,
``max_statement_length``, ``minimal``, ``readonly_symbols``,
``builtins_readonly``, ``config`` and the ``no_<node>`` / ``with_<node>``
flags.

Testing
-------

::

    go vet ./...
    go test ./...

``testdata/recorded.jsonl`` holds cases recorded from the Python original;
``recorded_test.go`` replays them through the public API.

License
-------

MIT, see ``LICENSE``. Parts of the value model follow CPython's own
algorithms (number formatting, the set table, list sorting) and carry the
Python Software Foundation's notice where they do.
