// Copyright (c) 2026 Matthew Newville, The University of Chicago
// Portions follow CPython 3.12, Copyright 2001 Python Software Foundation.
// SPDX-License-Identifier: MIT AND PSF-2.0
//
// CPython 3.12's str methods. Every function works on a Go string holding
// UTF-8 with code-point semantics: lengths, widths, indices and positions
// count code points, as Python does. A string is assumed to be valid UTF-8;
// an invalid byte reads as U+FFFD. Arguments arrive already converted from
// Values (that is the engine's job); each doc comment says what Python
// accepts for them, so the engine can raise Python's errors first.
//
// Unicode properties follow CPython 3.12's database (Unicode 15.0.0): Go's
// unicode package (17.0.0) is the baseline and pyunicode_tables.go lists the
// code points where Python's answer differs, including every code point
// assigned after 15.0, which Python still treats as unassigned (Cn).

package asteval

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ---------------------------------------------------------------- per rune

// pyRange is an inclusive range of code points.
type pyRange struct{ lo, hi rune }

// pyInRanges reports whether r lies in one of the sorted, disjoint ranges.
func pyInRanges(r rune, t []pyRange) bool {
	i := sort.Search(len(t), func(i int) bool { return t[i].hi >= r })
	return i < len(t) && t[i].lo <= r
}

// The Go baselines the generated flip tables correct: each table lists the
// code points where CPython 3.12 answers differently, so changing a baseline
// means regenerating the tables.

func pyBaseSpace(r rune) bool   { return unicode.IsSpace(r) }
func pyBaseAlpha(r rune) bool   { return unicode.IsLetter(r) }
func pyBaseDecimal(r rune) bool { return unicode.Is(unicode.Nd, r) }
func pyBaseNumeric(r rune) bool { return unicode.IsNumber(r) }
func pyBaseTitle(r rune) bool   { return unicode.Is(unicode.Lt, r) }
func pyBasePrintable(r rune) bool {
	return r == ' ' || unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S)
}
func pyBaseUpper(r rune) bool { return unicode.In(r, unicode.Lu, unicode.Other_Uppercase) }
func pyBaseLower(r rune) bool { return unicode.In(r, unicode.Ll, unicode.Other_Lowercase) }
func pyBaseCased(r rune) bool { return pyBaseUpper(r) || pyBaseLower(r) || pyBaseTitle(r) }
func pyBaseCaseIgnorable(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}
func pyBaseXIDStart(r rune) bool {
	return unicode.In(r, unicode.L, unicode.Nl, unicode.Other_ID_Start) &&
		!unicode.In(r, unicode.Pattern_Syntax, unicode.Pattern_White_Space)
}
func pyBaseXIDContinue(r rune) bool {
	return pyBaseXIDStart(r) || (unicode.In(r, unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc, unicode.Other_ID_Continue) &&
		!unicode.In(r, unicode.Pattern_Syntax, unicode.Pattern_White_Space))
}

// pyRuneIsSpace is str.isspace() for one character (Zs, or bidirectional
// class WS, B or S), which is also what re's \s matches in a str pattern.
func pyRuneIsSpace(r rune) bool {
	if r < utf8.RuneSelf {
		return r == ' ' || r >= '\t' && r <= '\r' || r >= 0x1c && r <= 0x1f
	}
	return pyBaseSpace(r) != pyInRanges(r, pyFlipSpace)
}

// pyRuneIsAlpha is str.isalpha() for one character (Lu, Ll, Lt, Lm, Lo).
func pyRuneIsAlpha(r rune) bool {
	if r < utf8.RuneSelf {
		return r|0x20 >= 'a' && r|0x20 <= 'z'
	}
	return pyBaseAlpha(r) != pyInRanges(r, pyFlipAlpha)
}

// pyRuneIsDecimalDigit is str.isdecimal() for one character, which is also
// what re's \d matches in a str pattern.
func pyRuneIsDecimalDigit(r rune) bool {
	if r < utf8.RuneSelf {
		return r >= '0' && r <= '9'
	}
	return pyBaseDecimal(r) != pyInRanges(r, pyFlipDecimal)
}

// pyRuneIsDigit is str.isdigit() for one character: a decimal digit or a
// character with a digit value, such as '²' or '①'.
func pyRuneIsDigit(r rune) bool {
	if r < utf8.RuneSelf {
		return r >= '0' && r <= '9'
	}
	return pyBaseDecimal(r) != pyInRanges(r, pyFlipDigit)
}

// pyRuneIsNumeric is str.isnumeric() for one character: any character with
// a numeric value, CJK numerals such as '一' included.
func pyRuneIsNumeric(r rune) bool {
	if r < utf8.RuneSelf {
		return r >= '0' && r <= '9'
	}
	return pyBaseNumeric(r) != pyInRanges(r, pyFlipNumeric)
}

// pyRuneIsAlnum is str.isalnum() for one character.
func pyRuneIsAlnum(r rune) bool {
	return pyRuneIsAlpha(r) || pyRuneIsDecimalDigit(r) || pyRuneIsDigit(r) || pyRuneIsNumeric(r)
}

// pyRuneIsWord is what re's \w matches in a str pattern: alnum or '_'.
func pyRuneIsWord(r rune) bool { return r == '_' || pyRuneIsAlnum(r) }

// pyRuneIsPrintable is str.isprintable() for one character, the test repr()
// uses to decide whether to escape it: false for Cc, Cf, Cs, Co, Cn, Zl, Zp
// and Zs other than the space.
func pyRuneIsPrintable(r rune) bool {
	if r < utf8.RuneSelf {
		return r >= ' ' && r < 0x7f
	}
	return pyBasePrintable(r) != pyInRanges(r, pyFlipPrintable)
}

// pyRuneIsUpper is Py_UNICODE_ISUPPER: the Uppercase property.
func pyRuneIsUpper(r rune) bool {
	if r < utf8.RuneSelf {
		return r >= 'A' && r <= 'Z'
	}
	return pyBaseUpper(r) != pyInRanges(r, pyFlipUpper)
}

// pyRuneIsLower is Py_UNICODE_ISLOWER: the Lowercase property.
func pyRuneIsLower(r rune) bool {
	if r < utf8.RuneSelf {
		return r >= 'a' && r <= 'z'
	}
	return pyBaseLower(r) != pyInRanges(r, pyFlipLower)
}

// pyRuneIsTitle is Py_UNICODE_ISTITLE: general category Lt, such as 'ǅ'.
func pyRuneIsTitle(r rune) bool { return pyBaseTitle(r) != pyInRanges(r, pyFlipTitle) }

// pyRuneIsCased is the Cased property, which drives str.title() and the
// final sigma rule.
func pyRuneIsCased(r rune) bool {
	if r < utf8.RuneSelf {
		return r|0x20 >= 'a' && r|0x20 <= 'z'
	}
	return pyBaseCased(r) != pyInRanges(r, pyFlipCased)
}

// pyRuneIsCaseIgnorable is the Case_Ignorable property used by the final
// sigma rule.
func pyRuneIsCaseIgnorable(r rune) bool {
	return pyBaseCaseIgnorable(r) != pyInRanges(r, pyFlipCaseIgnorable)
}

// pyRuneIsXIDStart is XID_Start; str.isidentifier() also accepts '_' first.
func pyRuneIsXIDStart(r rune) bool {
	if r < utf8.RuneSelf {
		return r|0x20 >= 'a' && r|0x20 <= 'z'
	}
	return pyBaseXIDStart(r) != pyInRanges(r, pyFlipXIDStart)
}

// pyRuneIsXIDContinue is XID_Continue, for every character of an
// identifier after the first.
func pyRuneIsXIDContinue(r rune) bool {
	if r < utf8.RuneSelf {
		return r|0x20 >= 'a' && r|0x20 <= 'z' || r >= '0' && r <= '9' || r == '_'
	}
	return pyBaseXIDContinue(r) != pyInRanges(r, pyFlipXIDContinue)
}

// pyRuneIsLinebreak reports whether str.splitlines() breaks at r:
// \n \v \f \r \x1c \x1d \x1e \x85 U+2028 U+2029.
func pyRuneIsLinebreak(r rune) bool {
	switch r {
	case '\n', '\v', '\f', '\r', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}

// ---------------------------------------------------------------- case mapping

// pyWriteMapped writes a full case mapping of r: the generated exception if
// there is one, else Go's simple mapping.
func pyWriteMapped(b *strings.Builder, r rune, exc map[rune]string, simple func(rune) rune) {
	if m, ok := exc[r]; ok {
		b.WriteString(m)
		return
	}
	b.WriteRune(simple(r))
}

func pyWriteUpper(b *strings.Builder, r rune) { pyWriteMapped(b, r, pyUpperExc, unicode.ToUpper) }
func pyWriteLower(b *strings.Builder, r rune) { pyWriteMapped(b, r, pyLowerExc, unicode.ToLower) }
func pyWriteTitle(b *strings.Builder, r rune) { pyWriteMapped(b, r, pyTitleExc, unicode.ToTitle) }

// pyWriteFold writes the full case folding of r, which is its full lower
// mapping except where the generated table says otherwise.
func pyWriteFold(b *strings.Builder, r rune) {
	if m, ok := pyFoldExc[r]; ok {
		b.WriteString(m)
		return
	}
	pyWriteLower(b, r)
}

// pyWriteLowerAt writes the lower mapping of rs[i], applying the final sigma
// rule to U+03A3 as CPython's lower_ucs4 does.
func pyWriteLowerAt(b *strings.Builder, rs []rune, i int) {
	if rs[i] != 'Σ' {
		pyWriteLower(b, rs[i])
	} else if pyFinalSigma(rs, i) {
		b.WriteRune('ς')
	} else {
		b.WriteRune('σ')
	}
}

// pyFinalSigma reports whether rs[i] is in the Final_Sigma context:
// cased case-ignorable* Σ not followed by case-ignorable* cased.
func pyFinalSigma(rs []rune, i int) bool {
	j := i - 1
	for j >= 0 && pyRuneIsCaseIgnorable(rs[j]) {
		j--
	}
	if j < 0 || !pyRuneIsCased(rs[j]) {
		return false
	}
	j = i + 1
	for j < len(rs) && pyRuneIsCaseIgnorable(rs[j]) {
		j++
	}
	return j == len(rs) || !pyRuneIsCased(rs[j])
}

// pyMapRunes builds a string by writing each rune of s through f.
func pyMapRunes(s string, f func(*strings.Builder, rune)) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		f(&b, r)
	}
	return b.String()
}

// pyUpper is str.upper(): the full uppercase mapping, so 'ß' becomes "SS".
func pyUpper(s string) string {
	if pyIsASCII(s) {
		return strings.ToUpper(s)
	}
	return pyMapRunes(s, pyWriteUpper)
}

// pyLower is str.lower(): the full lowercase mapping, so 'İ' becomes "i̇",
// with Σ lowered to ς at the end of a word.
func pyLower(s string) string {
	if pyIsASCII(s) {
		return strings.ToLower(s)
	}
	if !strings.ContainsRune(s, 'Σ') {
		return pyMapRunes(s, pyWriteLower)
	}
	rs := []rune(s)
	var b strings.Builder
	for i := range rs {
		pyWriteLowerAt(&b, rs, i)
	}
	return b.String()
}

// pyCasefold is str.casefold(): the full case folding, without the final
// sigma rule.
func pyCasefold(s string) string {
	if pyIsASCII(s) {
		return strings.ToLower(s)
	}
	return pyMapRunes(s, pyWriteFold)
}

// pyTitle is str.title(): a character is title-cased after a character
// that is not cased and lower-cased after one that is, so "they're" becomes
// "They'Re".
func pyTitle(s string) string {
	rs := []rune(s)
	var b strings.Builder
	prevCased := false
	for i, r := range rs {
		if prevCased {
			pyWriteLowerAt(&b, rs, i)
		} else {
			pyWriteTitle(&b, r)
		}
		prevCased = pyRuneIsCased(r)
	}
	return b.String()
}

// pyCapitalize is str.capitalize() as of Python 3.8: the first character
// title-cased (so 'ǆ' becomes 'ǅ', 'ß' "Ss"), the rest lower-cased.
func pyCapitalize(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		if i == 0 {
			pyWriteTitle(&b, r)
		} else {
			pyWriteLowerAt(&b, rs, i)
		}
	}
	return b.String()
}

// pySwapcase is str.swapcase(): uppercase characters lowered (with the final
// sigma rule), lowercase ones uppered, the rest (titlecase 'ǅ' included)
// left alone.
func pySwapcase(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		switch {
		case pyRuneIsUpper(r):
			pyWriteLowerAt(&b, rs, i)
		case pyRuneIsLower(r):
			pyWriteUpper(&b, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---------------------------------------------------------------- predicates

// pyAll reports whether s is not empty and every character satisfies f.
func pyAll(s string, f func(rune) bool) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !f(r) {
			return false
		}
	}
	return true
}

// pyIsUpper is str.isupper(): no lowercase or titlecase character and at
// least one uppercase one.
func pyIsUpper(s string) bool {
	if r, n := utf8.DecodeRuneInString(s); n == len(s) && n > 0 {
		return pyRuneIsUpper(r)
	}
	cased := false
	for _, r := range s {
		if pyRuneIsLower(r) || pyRuneIsTitle(r) {
			return false
		}
		cased = cased || pyRuneIsUpper(r)
	}
	return cased
}

// pyIsLower is str.islower(): no uppercase or titlecase character and at
// least one lowercase one.
func pyIsLower(s string) bool {
	if r, n := utf8.DecodeRuneInString(s); n == len(s) && n > 0 {
		return pyRuneIsLower(r)
	}
	cased := false
	for _, r := range s {
		if pyRuneIsUpper(r) || pyRuneIsTitle(r) {
			return false
		}
		cased = cased || pyRuneIsLower(r)
	}
	return cased
}

// pyIsTitle is str.istitle(): uppercase and titlecase characters only after
// uncased ones, lowercase ones only after cased ones, and at least one cased.
func pyIsTitle(s string) bool {
	if r, n := utf8.DecodeRuneInString(s); n == len(s) && n > 0 {
		return pyRuneIsTitle(r) || pyRuneIsUpper(r)
	}
	cased, prevCased := false, false
	for _, r := range s {
		switch {
		case pyRuneIsUpper(r) || pyRuneIsTitle(r):
			if prevCased {
				return false
			}
			prevCased, cased = true, true
		case pyRuneIsLower(r):
			if !prevCased {
				return false
			}
			prevCased, cased = true, true
		default:
			prevCased = false
		}
	}
	return cased
}

// pyIsAlpha is str.isalpha(); false for "".
func pyIsAlpha(s string) bool { return pyAll(s, pyRuneIsAlpha) }

// pyIsAlnum is str.isalnum(); false for "".
func pyIsAlnum(s string) bool { return pyAll(s, pyRuneIsAlnum) }

// pyIsDigit is str.isdigit(); false for "".
func pyIsDigit(s string) bool { return pyAll(s, pyRuneIsDigit) }

// pyIsDecimal is str.isdecimal(); false for "".
func pyIsDecimal(s string) bool { return pyAll(s, pyRuneIsDecimalDigit) }

// pyIsNumeric is str.isnumeric(); false for "".
func pyIsNumeric(s string) bool { return pyAll(s, pyRuneIsNumeric) }

// pyIsSpace is str.isspace(); false for "".
func pyIsSpace(s string) bool { return pyAll(s, pyRuneIsSpace) }

// pyIsPrintable is str.isprintable(); true for "".
func pyIsPrintable(s string) bool { return s == "" || pyAll(s, pyRuneIsPrintable) }

// pyIsASCII is str.isascii(); true for "".
func pyIsASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// pyIsIdentifier is str.isidentifier(): XID_Start or '_' first, XID_Continue
// after, with no normalisation and no keyword check.
func pyIsIdentifier(s string) bool {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 || r != '_' && !pyRuneIsXIDStart(r) {
		return false
	}
	for _, r := range s[n:] {
		if !pyRuneIsXIDContinue(r) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- whitespace, splitting

// pyStrip is str.strip(chars): chars nil (Python's None) strips whitespace
// as str.isspace() defines it, otherwise every code point in *chars. Python
// takes chars as a str or None.
func pyStrip(s string, chars *string) string {
	if chars == nil {
		return strings.TrimFunc(s, pyRuneIsSpace)
	}
	return strings.Trim(s, *chars)
}

// pyLStrip is str.lstrip(chars); see pyStrip.
func pyLStrip(s string, chars *string) string {
	if chars == nil {
		return strings.TrimLeftFunc(s, pyRuneIsSpace)
	}
	return strings.TrimLeft(s, *chars)
}

// pyRStrip is str.rstrip(chars); see pyStrip.
func pyRStrip(s string, chars *string) string {
	if chars == nil {
		return strings.TrimRightFunc(s, pyRuneIsSpace)
	}
	return strings.TrimRight(s, *chars)
}

// pySplit is str.split(sep, maxsplit). sep nil (None) splits on runs of
// whitespace and drops empty strings; otherwise an empty *sep raises
// ValueError "empty separator". maxsplit < 0 means no limit. Python takes
// sep as a str or None and maxsplit as an int that fits a C ssize_t
// (OverflowError "Python int too large to convert to C ssize_t" otherwise).
func pySplit(s string, sep *string, maxsplit int) ([]string, error) {
	if sep == nil {
		return pySplitWhitespace(s, maxsplit), nil
	}
	if *sep == "" {
		return nil, valueErr("empty separator")
	}
	if maxsplit < 0 || maxsplit >= len(s) {
		return strings.Split(s, *sep), nil
	}
	return strings.SplitN(s, *sep, maxsplit+1), nil
}

// pyRSplit is str.rsplit(sep, maxsplit): pySplit working from the right.
func pyRSplit(s string, sep *string, maxsplit int) ([]string, error) {
	if sep == nil {
		return pyRSplitWhitespace(s, maxsplit), nil
	}
	if *sep == "" {
		return nil, valueErr("empty separator")
	}
	var out []string
	for ; maxsplit != 0; maxsplit-- {
		i := strings.LastIndex(s, *sep)
		if i < 0 {
			break
		}
		out = append(out, s[i+len(*sep):])
		s = s[:i]
	}
	out = append(out, s)
	pyReverse(out)
	return out, nil
}

func pyReverse(a []string) {
	for i, j := 0, len(a)-1; i < j; i, j = i+1, j-1 {
		a[i], a[j] = a[j], a[i]
	}
}

// pySkipSpace returns the offset of the first non-space rune at or after i
// (want false) or of the first space rune (want true).
func pySkipSpace(s string, i int, want bool) int {
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		if pyRuneIsSpace(r) == want {
			break
		}
		i += n
	}
	return i
}

// pySkipSpaceBack is pySkipSpace from the right: it returns the end of the
// last rune before end whose isspace() equals want, or 0.
func pySkipSpaceBack(s string, end int, want bool) int {
	for end > 0 {
		r, n := utf8.DecodeLastRuneInString(s[:end])
		if pyRuneIsSpace(r) == want {
			break
		}
		end -= n
	}
	return end
}

// pySplitWhitespace is CPython's split_whitespace: once maxsplit splits are
// made, the rest (with leading whitespace skipped) is the last item.
func pySplitWhitespace(s string, maxsplit int) []string {
	out := []string{}
	i := 0
	for ; maxsplit != 0; maxsplit-- {
		i = pySkipSpace(s, i, false)
		if i == len(s) {
			break
		}
		j := i
		i = pySkipSpace(s, i, true)
		out = append(out, s[j:i])
	}
	if i < len(s) {
		if i = pySkipSpace(s, i, false); i < len(s) {
			out = append(out, s[i:])
		}
	}
	return out
}

// pyRSplitWhitespace is CPython's rsplit_whitespace.
func pyRSplitWhitespace(s string, maxsplit int) []string {
	out := []string{}
	i := len(s)
	for ; maxsplit != 0; maxsplit-- {
		i = pySkipSpaceBack(s, i, false)
		if i == 0 {
			break
		}
		j := i
		i = pySkipSpaceBack(s, i, true)
		out = append(out, s[i:j])
	}
	if i > 0 {
		if i = pySkipSpaceBack(s, i, false); i > 0 {
			out = append(out, s[:i])
		}
	}
	pyReverse(out)
	return out
}

// pySplitlines is str.splitlines(keepends): it breaks at every character
// pyRuneIsLinebreak accepts, reading "\r\n" as one break, and yields no
// empty last line for a trailing break.
func pySplitlines(s string, keepends bool) []string {
	out := []string{}
	for i, j := 0, 0; i < len(s); j = i {
		var r rune
		n := 0
		for i < len(s) {
			r, n = utf8.DecodeRuneInString(s[i:])
			if pyRuneIsLinebreak(r) {
				break
			}
			i += n
		}
		eol := i
		if i < len(s) {
			if r == '\r' && i+1 < len(s) && s[i+1] == '\n' {
				i += 2
			} else {
				i += n
			}
			if keepends {
				eol = i
			}
		}
		out = append(out, s[j:eol])
	}
	return out
}

// pyExpandtabs is str.expandtabs(tabsize): each tab becomes spaces up to the
// next multiple of tabsize, the column restarting after "\n" and "\r" only;
// tabsize <= 0 removes tabs. Python takes tabsize as an int that fits a C
// int (OverflowError "Python int too large to convert to C int" otherwise).
func pyExpandtabs(s string, tabsize int) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch {
		case r == '\t':
			if tabsize > 0 {
				n := tabsize - col%tabsize
				col += n
				b.WriteString(strings.Repeat(" ", n))
			}
		default:
			b.WriteRune(r)
			col++
			if r == '\n' || r == '\r' {
				col = 0
			}
		}
	}
	return b.String()
}

// ---------------------------------------------------------------- search

// pyAdjustIndices is CPython's ADJUST_INDICES for a string of n code points:
// a nil bound is the default, a negative one counts from the end, end is
// clamped to [0, n] and start only below at 0. The engine converts a bound
// as a slice index (None, or an int or bool with big ints clamped to the Go
// int range); anything else is TypeError "slice indices must be integers or
// None or have an __index__ method".
func pyAdjustIndices(start, end *int, n int) (int, int) {
	st, en := 0, n
	if end != nil {
		en = *end
		if en > n {
			en = n
		} else if en < 0 {
			en = max(en+n, 0)
		}
	}
	if start != nil {
		st = *start
		if st < 0 {
			st = max(st+n, 0)
		}
	}
	return st, en
}

// pyByteOffset returns the byte offset of code point i of s (len(s) past
// the end).
func pyByteOffset(s string, i int) int {
	off := 0
	for ; i > 0 && off < len(s); i-- {
		_, n := utf8.DecodeRuneInString(s[off:])
		off += n
	}
	return off
}

// pyWindow returns s[st:en] in code points with the byte offset it starts
// at, for 0 <= st <= en <= len(s) in code points.
func pyWindow(s string, st, en int) (string, int) {
	bs := pyByteOffset(s, st)
	be := bs + pyByteOffset(s[bs:], en-st)
	return s[bs:be], bs
}

// pySearch runs find (last false) or rfind (last true) on s[start:end].
func pySearch(s, sub string, start, end *int, last bool) int {
	st, en := pyAdjustIndices(start, end, utf8.RuneCountInString(s))
	if en-st < utf8.RuneCountInString(sub) {
		return -1
	}
	w, _ := pyWindow(s, st, en)
	var i int
	if last {
		i = strings.LastIndex(w, sub)
	} else {
		i = strings.Index(w, sub)
	}
	if i < 0 {
		return -1
	}
	return st + utf8.RuneCountInString(w[:i])
}

// pyFind is str.find(sub, start, end): the lowest index of sub within
// s[start:end], or -1. An empty sub is found at start unless start is past
// the clamped end.
func pyFind(s, sub string, start, end *int) int { return pySearch(s, sub, start, end, false) }

// pyRFind is str.rfind(sub, start, end): the highest index, or -1.
func pyRFind(s, sub string, start, end *int) int { return pySearch(s, sub, start, end, true) }

// pyIndex is str.index(): pyFind raising ValueError "substring not found".
func pyIndex(s, sub string, start, end *int) (int, error) {
	if i := pyFind(s, sub, start, end); i >= 0 {
		return i, nil
	}
	return 0, valueErr("substring not found")
}

// pyRIndex is str.rindex(): pyRFind raising ValueError "substring not found".
func pyRIndex(s, sub string, start, end *int) (int, error) {
	if i := pyRFind(s, sub, start, end); i >= 0 {
		return i, nil
	}
	return 0, valueErr("substring not found")
}

// pyCount is str.count(sub, start, end): non-overlapping occurrences in
// s[start:end]; an empty sub counts end-start+1 positions.
func pyCount(s, sub string, start, end *int) int {
	st, en := pyAdjustIndices(start, end, utf8.RuneCountInString(s))
	if en-st < utf8.RuneCountInString(sub) {
		return 0
	}
	w, _ := pyWindow(s, st, en)
	return strings.Count(w, sub)
}

// pyTailMatch is CPython's tailmatch: whether s[start:end] starts (suffix
// false) or ends (suffix true) with sub.
func pyTailMatch(s, sub string, start, end *int, suffix bool) bool {
	st, en := pyAdjustIndices(start, end, utf8.RuneCountInString(s))
	m := utf8.RuneCountInString(sub)
	en -= m
	if en < st {
		return false
	}
	if m == 0 {
		return true
	}
	at := st
	if suffix {
		at = en
	}
	return strings.HasPrefix(s[pyByteOffset(s, at):], sub)
}

// pyStartsWith is str.startswith(prefix, start, end) for one prefix; for a
// tuple the engine calls it per item. Python takes prefix as a str or a
// tuple of str (TypeError "startswith first arg must be str or a tuple of
// str, not X" otherwise).
func pyStartsWith(s, prefix string, start, end *int) bool {
	return pyTailMatch(s, prefix, start, end, false)
}

// pyEndsWith is str.endswith(suffix, start, end); see pyStartsWith.
func pyEndsWith(s, suffix string, start, end *int) bool {
	return pyTailMatch(s, suffix, start, end, true)
}

// pyReplace is str.replace(old, new, count): count < 0 replaces every
// occurrence; an empty old inserts new before every character and at the
// end, count times at most. Python takes count as an int that fits a C
// ssize_t.
func pyReplace(s, old, new string, count int) string {
	return strings.Replace(s, old, new, count)
}

// pyPartition is str.partition(sep): (before, sep, after) at the first sep,
// or (s, "", "") when it is absent. An empty sep is ValueError "empty
// separator".
func pyPartition(s, sep string) ([3]string, error) {
	if sep == "" {
		return [3]string{}, valueErr("empty separator")
	}
	if i := strings.Index(s, sep); i >= 0 {
		return [3]string{s[:i], sep, s[i+len(sep):]}, nil
	}
	return [3]string{s, "", ""}, nil
}

// pyRPartition is str.rpartition(sep): split at the last sep, or
// ("", "", s) when it is absent.
func pyRPartition(s, sep string) ([3]string, error) {
	if sep == "" {
		return [3]string{}, valueErr("empty separator")
	}
	if i := strings.LastIndex(s, sep); i >= 0 {
		return [3]string{s[:i], sep, s[i+len(sep):]}, nil
	}
	return [3]string{"", "", s}, nil
}

// pyRemovePrefix is str.removeprefix(prefix).
func pyRemovePrefix(s, prefix string) string { return strings.TrimPrefix(s, prefix) }

// pyRemoveSuffix is str.removesuffix(suffix).
func pyRemoveSuffix(s, suffix string) string { return strings.TrimSuffix(s, suffix) }

// ---------------------------------------------------------------- padding

// pyPad surrounds s with left and right copies of fill.
func pyPad(s string, left, right int, fill rune) string {
	f := string(fill)
	return strings.Repeat(f, left) + s + strings.Repeat(f, right)
}

// pyCenter is str.center(width, fillchar). When the margin is odd the extra
// fill goes on the left only if width is odd too: 'ab'.center(5) is
// "  ab " but 'abc'.center(6) is " abc  ". Python takes width as an int
// that fits a C ssize_t and fillchar as a str of exactly one character
// (TypeError "The fill character must be exactly one character long").
func pyCenter(s string, width int, fill rune) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	marg := width - n
	left := marg/2 + (marg & width & 1)
	return pyPad(s, left, marg-left, fill)
}

// pyLJust is str.ljust(width, fillchar); see pyCenter for the arguments.
func pyLJust(s string, width int, fill rune) string {
	if n := utf8.RuneCountInString(s); n < width {
		return pyPad(s, 0, width-n, fill)
	}
	return s
}

// pyRJust is str.rjust(width, fillchar); see pyCenter for the arguments.
func pyRJust(s string, width int, fill rune) string {
	if n := utf8.RuneCountInString(s); n < width {
		return pyPad(s, width-n, 0, fill)
	}
	return s
}

// pyZfill is str.zfill(width): zeros on the left, after a leading '+' or '-'.
func pyZfill(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	zeros := strings.Repeat("0", width-n)
	if s != "" && (s[0] == '+' || s[0] == '-') {
		return s[:1] + zeros + s[1:]
	}
	return zeros + s
}
