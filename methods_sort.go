// Copyright 2001 Python Software Foundation (listobject.c)
// SPDX-License-Identifier: PSF-2.0
//
// CPython 3.12's list.sort: timsort with powersort merging, ported from
// Objects/listobject.c so that it makes the same comparisons in the same
// order. That matters whenever < is not a total order (NaN, mixed types):
// the result, the TypeError raised (and its operand order) and the order a
// failed sort leaves the list in all match CPython.

package asteval

// sortItem is an item with its sort key (the item itself without key=).
type sortItem struct{ k, v Value }

const (
	sortMinGallop = 7
)

type sortRun struct{ base, len, power int }

type sortState struct {
	less      func(a, b Value) (bool, error)
	a         []sortItem
	minGallop int
	pending   []sortRun
	tmp       []sortItem
}

func sortReverse(a []sortItem) {
	for i, j := 0, len(a)-1; i < j; i, j = i+1, j-1 {
		a[i], a[j] = a[j], a[i]
	}
}

// methodsSort sorts a in place by key with Python's <, stably.
func methodsSort(a []sortItem) error { return sortBy(a, pyLess) }

// sortBy is CPython's list.sort over the items' keys with the comparison
// less: the same sequence of comparisons, so the first one that fails is the
// one Python reports.
func sortBy(a []sortItem, less func(a, b Value) (bool, error)) error {
	n := len(a)
	if n < 2 {
		return nil
	}
	ms := &sortState{less: less, a: a, minGallop: sortMinGallop}
	minrun := sortMinrun(n)
	lo, rem := 0, n
	for rem > 0 {
		run, desc, err := ms.countRun(lo, lo+rem)
		if err != nil {
			return err
		}
		if desc {
			sortReverse(a[lo : lo+run])
		}
		if run < minrun {
			force := min(rem, minrun)
			if err := ms.binarySort(lo, lo+force, lo+run); err != nil {
				return err
			}
			run = force
		}
		if err := ms.foundNewRun(run, n); err != nil {
			return err
		}
		ms.pending = append(ms.pending, sortRun{base: lo, len: run})
		lo += run
		rem -= run
	}
	for len(ms.pending) > 1 {
		i := len(ms.pending) - 2
		if i > 0 && ms.pending[i-1].len < ms.pending[i+1].len {
			i--
		}
		if err := ms.mergeAt(i); err != nil {
			return err
		}
	}
	return nil
}

// sortMinrun is merge_compute_minrun.
func sortMinrun(n int) int {
	r := 0
	for n >= 64 {
		r |= n & 1
		n >>= 1
	}
	return n + r
}

// countRun is count_run (3.12): the length of the run starting at lo, and
// whether it is strictly descending.
func (ms *sortState) countRun(lo, hi int) (int, bool, error) {
	a := ms.a
	if lo+1 == hi {
		return 1, false, nil
	}
	n := 2
	desc, err := ms.less(a[lo+1].k, a[lo].k)
	if err != nil {
		return 0, false, err
	}
	for i := lo + 2; i < hi; i, n = i+1, n+1 {
		lt, err := ms.less(a[i].k, a[i-1].k)
		if err != nil {
			return 0, false, err
		}
		if lt != desc {
			break
		}
	}
	return n, desc, nil
}

// binarySort is binarysort: a[lo:start] is sorted; insert a[start:hi].
func (ms *sortState) binarySort(lo, hi, start int) error {
	a := ms.a
	if lo == start {
		start++
	}
	for ; start < hi; start++ {
		l, r := lo, start
		pivot := a[r]
		for l < r {
			p := l + (r-l)>>1
			lt, err := ms.less(pivot.k, a[p].k)
			if err != nil {
				return err
			}
			if lt {
				r = p
			} else {
				l = p + 1
			}
		}
		copy(a[l+1:start+1], a[l:start])
		a[l] = pivot
	}
	return nil
}

// sortPowerloop is powerloop: the power of the boundary between the run
// a[s1:s1+n1] and the run of n2 items after it, in a list of n items.
func sortPowerloop(s1, n1, n2, n int) int {
	result := 0
	a := 2*s1 + n1
	b := a + n1 + n2
	for {
		result++
		if a >= n {
			a -= n
			b -= n
		} else if b >= n {
			break
		}
		a <<= 1
		b <<= 1
	}
	return result
}

// foundNewRun is found_new_run: merge pending runs as powersort says
// before the run of n2 items is pushed.
func (ms *sortState) foundNewRun(n2, listlen int) error {
	if len(ms.pending) == 0 {
		return nil
	}
	top := ms.pending[len(ms.pending)-1]
	power := sortPowerloop(top.base, top.len, n2, listlen)
	for len(ms.pending) > 1 && ms.pending[len(ms.pending)-2].power > power {
		if err := ms.mergeAt(len(ms.pending) - 2); err != nil {
			return err
		}
	}
	ms.pending[len(ms.pending)-1].power = power
	return nil
}

// sortGallopLeft is gallop_left: the position k in the sorted a where key
// belongs, leftmost among equals (a[k-1] < key <= a[k]), searching from
// hint.
func sortGallopLeft(less func(a, b Value) (bool, error), key Value, a []sortItem, hint int) (int, error) {
	n := len(a)
	lastofs, ofs := 0, 1
	lt, err := less(a[hint].k, key)
	if err != nil {
		return 0, err
	}
	if lt {
		maxofs := n - hint
		for ofs < maxofs {
			lt, err := less(a[hint+ofs].k, key)
			if err != nil {
				return 0, err
			}
			if !lt {
				break
			}
			lastofs = ofs
			ofs = ofs<<1 + 1
		}
		ofs = min(ofs, maxofs)
		lastofs += hint
		ofs += hint
	} else {
		maxofs := hint + 1
		for ofs < maxofs {
			lt, err := less(a[hint-ofs].k, key)
			if err != nil {
				return 0, err
			}
			if lt {
				break
			}
			lastofs = ofs
			ofs = ofs<<1 + 1
		}
		ofs = min(ofs, maxofs)
		lastofs, ofs = hint-ofs, hint-lastofs
	}
	lastofs++
	for lastofs < ofs {
		m := lastofs + (ofs-lastofs)>>1
		lt, err := less(a[m].k, key)
		if err != nil {
			return 0, err
		}
		if lt {
			lastofs = m + 1
		} else {
			ofs = m
		}
	}
	return ofs, nil
}

// sortGallopRight is gallop_right: like sortGallopLeft but rightmost among equals
// (a[k-1] <= key < a[k]).
func sortGallopRight(less func(a, b Value) (bool, error), key Value, a []sortItem, hint int) (int, error) {
	n := len(a)
	lastofs, ofs := 0, 1
	lt, err := less(key, a[hint].k)
	if err != nil {
		return 0, err
	}
	if lt {
		maxofs := hint + 1
		for ofs < maxofs {
			lt, err := less(key, a[hint-ofs].k)
			if err != nil {
				return 0, err
			}
			if !lt {
				break
			}
			lastofs = ofs
			ofs = ofs<<1 + 1
		}
		ofs = min(ofs, maxofs)
		lastofs, ofs = hint-ofs, hint-lastofs
	} else {
		maxofs := n - hint
		for ofs < maxofs {
			lt, err := less(key, a[hint+ofs].k)
			if err != nil {
				return 0, err
			}
			if lt {
				break
			}
			lastofs = ofs
			ofs = ofs<<1 + 1
		}
		ofs = min(ofs, maxofs)
		lastofs += hint
		ofs += hint
	}
	lastofs++
	for lastofs < ofs {
		m := lastofs + (ofs-lastofs)>>1
		lt, err := less(key, a[m].k)
		if err != nil {
			return 0, err
		}
		if lt {
			ofs = m
		} else {
			lastofs = m + 1
		}
	}
	return ofs, nil
}

// mergeAt is merge_at: merge the pending runs i and i+1.
func (ms *sortState) mergeAt(i int) error {
	p := ms.pending
	pa, na := p[i].base, p[i].len
	pb, nb := p[i+1].base, p[i+1].len
	p[i].len = na + nb
	if i == len(p)-3 {
		p[i+1] = p[i+2]
	}
	ms.pending = p[:len(p)-1]

	// Items of a before where b's first item goes are already in place.
	k, err := sortGallopRight(ms.less, ms.a[pb].k, ms.a[pa:pa+na], 0)
	if err != nil {
		return err
	}
	pa += k
	na -= k
	if na == 0 {
		return nil
	}
	// Items of b after where a's last item goes are already in place.
	nb, err = sortGallopLeft(ms.less, ms.a[pa+na-1].k, ms.a[pb:pb+nb], nb-1)
	if err != nil || nb <= 0 {
		return err
	}
	if na <= nb {
		return ms.mergeLo(pa, na, pb, nb)
	}
	return ms.mergeHi(pa, na, pb, nb)
}

// sortOutcome says how the main loop of a merge ended.
type sortOutcome int

const (
	sortDone sortOutcome = iota // succeeded, or failed with an error
	sortCopyTail
)

// mergeLo is merge_lo: merge the na items at pa with the nb items at
// pb = pa+na, na <= nb, copying a to temporary storage.
func (ms *sortState) mergeLo(pa, na, pb, nb int) error {
	arr := ms.a
	ms.tmp = append(ms.tmp[:0], arr[pa:pa+na]...)
	tmp := ms.tmp
	dest, a, b := pa, 0, pb
	run := func() (sortOutcome, error) {
		arr[dest] = arr[b]
		dest++
		b++
		nb--
		if nb == 0 {
			return sortDone, nil
		}
		if na == 1 {
			return sortCopyTail, nil
		}
		minGallop := ms.minGallop
		for {
			acount, bcount := 0, 0
			for {
				lt, err := ms.less(arr[b].k, tmp[a].k)
				if err != nil {
					return sortDone, err
				}
				if lt {
					arr[dest] = arr[b]
					dest++
					b++
					bcount++
					acount = 0
					nb--
					if nb == 0 {
						return sortDone, nil
					}
					if bcount >= minGallop {
						break
					}
				} else {
					arr[dest] = tmp[a]
					dest++
					a++
					acount++
					bcount = 0
					na--
					if na == 1 {
						return sortCopyTail, nil
					}
					if acount >= minGallop {
						break
					}
				}
			}
			minGallop++
			for {
				if minGallop > 1 {
					minGallop--
				}
				ms.minGallop = minGallop
				k, err := sortGallopRight(ms.less, arr[b].k, tmp[a:a+na], 0)
				if err != nil {
					return sortDone, err
				}
				acount = k
				if k > 0 {
					copy(arr[dest:dest+k], tmp[a:a+k])
					dest += k
					a += k
					na -= k
					if na == 1 {
						return sortCopyTail, nil
					}
					if na == 0 {
						return sortDone, nil
					}
				}
				arr[dest] = arr[b]
				dest++
				b++
				nb--
				if nb == 0 {
					return sortDone, nil
				}
				k, err = sortGallopLeft(ms.less, tmp[a].k, arr[b:b+nb], 0)
				if err != nil {
					return sortDone, err
				}
				bcount = k
				if k > 0 {
					copy(arr[dest:dest+k], arr[b:b+k])
					dest += k
					b += k
					nb -= k
					if nb == 0 {
						return sortDone, nil
					}
				}
				arr[dest] = tmp[a]
				dest++
				a++
				na--
				if na == 1 {
					return sortCopyTail, nil
				}
				if acount < sortMinGallop && bcount < sortMinGallop {
					break
				}
			}
			minGallop++
			ms.minGallop = minGallop
		}
	}
	outcome, err := run()
	if outcome == sortCopyTail {
		// The last item of a belongs at the end of the merge.
		copy(arr[dest:dest+nb], arr[b:b+nb])
		arr[dest+nb] = tmp[a]
		return nil
	}
	if na > 0 {
		copy(arr[dest:dest+na], tmp[a:a+na])
	}
	return err
}

// mergeHi is merge_hi: like mergeLo for na > nb, merging from the right
// with b in temporary storage.
func (ms *sortState) mergeHi(pa, na, pb, nb int) error {
	arr := ms.a
	ms.tmp = append(ms.tmp[:0], arr[pb:pb+nb]...)
	tmp := ms.tmp
	basea := pa
	// a and b index the last remaining item of each run: a == basea+na-1
	// and b == nb-1 throughout.
	dest, a, b := pb+nb-1, pa+na-1, nb-1
	run := func() (sortOutcome, error) {
		arr[dest] = arr[a]
		dest--
		a--
		na--
		if na == 0 {
			return sortDone, nil
		}
		if nb == 1 {
			return sortCopyTail, nil
		}
		minGallop := ms.minGallop
		for {
			acount, bcount := 0, 0
			for {
				lt, err := ms.less(tmp[b].k, arr[a].k)
				if err != nil {
					return sortDone, err
				}
				if lt {
					arr[dest] = arr[a]
					dest--
					a--
					acount++
					bcount = 0
					na--
					if na == 0 {
						return sortDone, nil
					}
					if acount >= minGallop {
						break
					}
				} else {
					arr[dest] = tmp[b]
					dest--
					b--
					bcount++
					acount = 0
					nb--
					if nb == 1 {
						return sortCopyTail, nil
					}
					if bcount >= minGallop {
						break
					}
				}
			}
			minGallop++
			for {
				if minGallop > 1 {
					minGallop--
				}
				ms.minGallop = minGallop
				k, err := sortGallopRight(ms.less, tmp[b].k, arr[basea:basea+na], na-1)
				if err != nil {
					return sortDone, err
				}
				k = na - k
				acount = k
				if k > 0 {
					dest -= k
					a -= k
					copy(arr[dest+1:dest+1+k], arr[a+1:a+1+k])
					na -= k
					if na == 0 {
						return sortDone, nil
					}
				}
				arr[dest] = tmp[b]
				dest--
				b--
				nb--
				if nb == 1 {
					return sortCopyTail, nil
				}
				k, err = sortGallopLeft(ms.less, arr[a].k, tmp[:nb], nb-1)
				if err != nil {
					return sortDone, err
				}
				k = nb - k
				bcount = k
				if k > 0 {
					dest -= k
					b -= k
					copy(arr[dest+1:dest+1+k], tmp[b+1:b+1+k])
					nb -= k
					if nb == 1 {
						return sortCopyTail, nil
					}
					if nb == 0 {
						return sortDone, nil
					}
				}
				arr[dest] = arr[a]
				dest--
				a--
				na--
				if na == 0 {
					return sortDone, nil
				}
				if acount < sortMinGallop && bcount < sortMinGallop {
					break
				}
			}
			minGallop++
			ms.minGallop = minGallop
		}
	}
	outcome, err := run()
	if outcome == sortCopyTail {
		// The first item of b belongs at the front of the merge.
		copy(arr[dest+1-na:dest+1], arr[a+1-na:a+1])
		dest -= na
		arr[dest] = tmp[b]
		return nil
	}
	if nb > 0 {
		copy(arr[dest-(nb-1):dest+1], tmp[:nb])
	}
	return err
}

// dictItems is list(d.items()) as sort items.
func dictItems(d *dictV) []sortItem {
	items := make([]sortItem, len(d.keys))
	for i := range d.keys {
		items[i] = sortItem{d.keys[i], d.vals[i]}
	}
	return items
}
