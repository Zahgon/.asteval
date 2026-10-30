// Copyright (c) 2026 Matthew Newville, The University of Chicago
// SPDX-License-Identifier: MIT

package asteval

// pyRuneIsDecimal is what Python's re \d matches.
func pyRuneIsDecimal(r rune) bool { return pyRuneIsDecimalDigit(r) }

// pyDecimalValueOr is unicodedata.decimal(r, -1).
func pyDecimalValueOr(r rune) int {
	if !pyRuneIsDecimalDigit(r) {
		return -1
	}
	return pyDecimalValue(r)
}

// pyDecimalValue is unicodedata.decimal for a decimal digit. Decimal digits
// come in runs of ten from 0 to 9, some runs adjacent (the mathematical
// digits), so the value counts from the start of the contiguous run.
func pyDecimalValue(r rune) int {
	z := r
	for z > 0 && pyRuneIsDecimalDigit(z-1) {
		z--
	}
	return int(r-z) % 10
}
