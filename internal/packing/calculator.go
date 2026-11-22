// Package packing works out which packs to ship for an order.
//
// The rules, in priority order, are:
//
//  1. Only whole packs can be sent.
//  2. Within rule 1, send out no more items than necessary.
//  3. Within rules 1 and 2, send out as few packs as possible.
//
// Greedy "largest pack first" approaches break these rules for many inputs
// (for example pack sizes 23, 31, 53), so the calculator uses dynamic
// programming: it finds the smallest reachable total that is >= the order
// (rule 2) and, for that total, the combination with the fewest packs (rule 3).
//
// Two reductions keep this fast and memory-bounded for very large orders:
//
//   - GCD scaling. Every reachable total is a multiple of g = gcd(pack sizes),
//     so all sizes and the order are divided by g before solving.
//     (250/500/1000/2000/5000 becomes 1/2/4/8/20.)
//
//   - Largest-pack shifting. Let L be the largest pack. In any fewest-packs
//     solution, a smaller pack d appears fewer than L/gcd(d,L) times; otherwise
//     L/gcd(d,L) copies of d could be swapped for d/gcd(d,L) copies of L, which
//     is the same number of items in strictly fewer packs. So the items carried
//     by packs other than L are bounded by a constant B. For an order above B we
//     can therefore remove whole multiples of L, solve the small remainder, and
//     add those L packs back, without changing the answer. This makes the work
//     independent of the order size (an order of 10^12 items is instant).
package packing

import (
	"errors"
	"fmt"
	"slices"
)

const (
	// MaxPackSizes is the maximum number of distinct pack sizes accepted.
	MaxPackSizes = 50
	// MaxPackSize is the largest single pack size accepted.
	MaxPackSize = 1_000_000
	// MaxOrder is the largest order (in items) accepted.
	MaxOrder int64 = 1_000_000_000_000

	// maxStates caps the dynamic-programming table (about 5 bytes per entry).
	// With the reductions above, only adversarial pack sets get near it.
	maxStates = 5_000_000

	unreachable = ^uint32(0)
)

var (
	// ErrNoPackSizes is returned when no pack sizes are configured.
	ErrNoPackSizes = errors.New("at least one pack size is required")
	// ErrInvalidPackSize is returned for a pack size outside 1..MaxPackSize.
	ErrInvalidPackSize = errors.New("invalid pack size")
	// ErrTooManyPackSizes is returned when more than MaxPackSizes sizes are given.
	ErrTooManyPackSizes = errors.New("too many pack sizes")
	// ErrInvalidOrder is returned for an order outside 0..MaxOrder.
	ErrInvalidOrder = errors.New("invalid order quantity")
	// ErrTooComplex is returned when the pack sizes would need an
	// unreasonably large search space (e.g. huge, pairwise-coprime sizes).
	ErrTooComplex = errors.New("pack sizes are too large or irregular to calculate this order")
)

// Pack is a number of packs of one size.
type Pack struct {
	Size     int   `json:"size"`
	Quantity int64 `json:"quantity"`
}

// Result describes the packs to ship for one order.
type Result struct {
	ItemsOrdered int64  `json:"itemsOrdered"`
	TotalItems   int64  `json:"totalItems"`
	TotalPacks   int64  `json:"totalPacks"`
	Surplus      int64  `json:"surplus"`
	PackSizes    []int  `json:"packSizes"`
	Packs        []Pack `json:"packs"`
}

// NormalizePackSizes validates pack sizes and returns them de-duplicated and
// sorted in ascending order. The input slice is not modified.
func NormalizePackSizes(sizes []int) ([]int, error) {
	if len(sizes) == 0 {
		return nil, ErrNoPackSizes
	}
	out := slices.Clone(sizes)
	for _, s := range out {
		if s < 1 || s > MaxPackSize {
			return nil, fmt.Errorf("%w: %d (must be between 1 and %d)", ErrInvalidPackSize, s, MaxPackSize)
		}
	}
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) > MaxPackSizes {
		return nil, fmt.Errorf("%w: %d (maximum is %d)", ErrTooManyPackSizes, len(out), MaxPackSizes)
	}
	return out, nil
}

// Calculate returns the packs to ship for an order of the given number of
// items, following the three rules described in the package documentation.
// Packs in the result are ordered from largest to smallest size.
func Calculate(order int64, packSizes []int) (Result, error) {
	sizes, err := NormalizePackSizes(packSizes)
	if err != nil {
		return Result{}, err
	}
	if order < 0 || order > MaxOrder {
		return Result{}, fmt.Errorf("%w: %d (must be between 0 and %d)", ErrInvalidOrder, order, MaxOrder)
	}

	res := Result{ItemsOrdered: order, PackSizes: sizes, Packs: []Pack{}}
	if order == 0 {
		return res, nil
	}

	// GCD scaling: work in units of g items.
	g := sizes[0]
	for _, s := range sizes[1:] {
		g = gcd(g, s)
	}
	units := make([]int, len(sizes))
	for i, s := range sizes {
		units[i] = s / g
	}
	smallest, largest := units[0], units[len(units)-1]
	top := len(units) - 1

	// Upper bound on the units carried by packs other than the largest in any
	// fewest-packs solution (see package docs).
	var bound int64
	for _, u := range units[:top] {
		bound += int64(u) * int64(largest/gcd(u, largest)-1)
	}

	target := ceilDiv(order, int64(g))

	// Largest-pack shifting: strip whole largest packs while the remaining
	// target stays above the bound.
	var shift int64
	if target > bound+1 {
		shift = (target - bound - 1) / int64(largest)
	}
	reduced := target - shift*int64(largest)

	// The smallest reachable total >= reduced is at most reduced+smallest-1
	// (just use the smallest pack), so that is as far as we need to look.
	limit := reduced + int64(smallest) - 1
	if limit > maxStates {
		return Result{}, ErrTooComplex
	}

	counts, last := solve(int(limit), units)

	for t := int(reduced); t <= int(limit); t++ {
		if counts[t] == unreachable {
			continue
		}
		qty := make([]int64, len(units))
		qty[top] = shift
		for x := t; x > 0; x -= units[last[x]] {
			qty[last[x]]++
		}
		for i := top; i >= 0; i-- {
			if qty[i] == 0 {
				continue
			}
			res.Packs = append(res.Packs, Pack{Size: sizes[i], Quantity: qty[i]})
			res.TotalPacks += qty[i]
			res.TotalItems += qty[i] * int64(sizes[i])
		}
		res.Surplus = res.TotalItems - order
		return res, nil
	}

	// Unreachable: ceil(reduced/smallest)*smallest always lies in the range.
	return Result{}, errors.New("packing: no solution found")
}

// solve fills, for every total 0..limit (in units), the fewest packs that make
// exactly that total and the index of the last pack used. Larger packs win
// ties, which keeps results deterministic.
func solve(limit int, units []int) ([]uint32, []uint8) {
	counts := make([]uint32, limit+1)
	last := make([]uint8, limit+1)
	for t := 1; t <= limit; t++ {
		best := unreachable
		for i := len(units) - 1; i >= 0; i-- {
			u := units[i]
			if u > t {
				continue
			}
			if c := counts[t-u]; c != unreachable && c+1 < best {
				best = c + 1
				last[t] = uint8(i)
			}
		}
		counts[t] = best
	}
	return counts, last
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func ceilDiv(a, b int64) int64 {
	return (a + b - 1) / b
}
