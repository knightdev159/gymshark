package packing

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

var defaultSizes = []int{250, 500, 1000, 2000, 5000}

func TestCalculate_ChallengeExamples(t *testing.T) {
	tests := []struct {
		order int64
		want  []Pack
	}{
		{1, []Pack{{250, 1}}},
		{250, []Pack{{250, 1}}},
		{251, []Pack{{500, 1}}},
		{501, []Pack{{500, 1}, {250, 1}}},
		{12001, []Pack{{5000, 2}, {2000, 1}, {250, 1}}},
	}
	for _, tc := range tests {
		got, err := Calculate(tc.order, defaultSizes)
		if err != nil {
			t.Fatalf("Calculate(%d): unexpected error: %v", tc.order, err)
		}
		if !reflect.DeepEqual(got.Packs, tc.want) {
			t.Errorf("Calculate(%d) = %v, want %v", tc.order, got.Packs, tc.want)
		}
	}
}

func TestCalculate_MoreDefaultCases(t *testing.T) {
	tests := []struct {
		order      int64
		want       []Pack
		totalItems int64
	}{
		{500, []Pack{{500, 1}}, 500},
		{750, []Pack{{500, 1}, {250, 1}}, 750},
		{751, []Pack{{1000, 1}}, 1000},
		{1001, []Pack{{1000, 1}, {250, 1}}, 1250},
		{4750, []Pack{{2000, 2}, {500, 1}, {250, 1}}, 4750},
		{5000, []Pack{{5000, 1}}, 5000},
		{9999, []Pack{{5000, 2}}, 10000},
		{10001, []Pack{{5000, 2}, {250, 1}}, 10250},
	}
	for _, tc := range tests {
		got, err := Calculate(tc.order, defaultSizes)
		if err != nil {
			t.Fatalf("Calculate(%d): unexpected error: %v", tc.order, err)
		}
		if !reflect.DeepEqual(got.Packs, tc.want) || got.TotalItems != tc.totalItems {
			t.Errorf("Calculate(%d) = %v (total %d), want %v (total %d)",
				tc.order, got.Packs, got.TotalItems, tc.want, tc.totalItems)
		}
	}
}

// The well-known edge case where greedy algorithms fail.
func TestCalculate_EdgeCase_23_31_53(t *testing.T) {
	got, err := Calculate(500_000, []int{23, 31, 53})
	if err != nil {
		t.Fatal(err)
	}
	want := []Pack{{53, 9429}, {31, 7}, {23, 2}}
	if !reflect.DeepEqual(got.Packs, want) {
		t.Fatalf("got %v, want %v", got.Packs, want)
	}
	if got.TotalItems != 500_000 || got.Surplus != 0 || got.TotalPacks != 9438 {
		t.Fatalf("unexpected totals: %+v", got)
	}
}

func TestCalculate_ResultFields(t *testing.T) {
	got, err := Calculate(12001, []int{5000, 250, 2000, 500, 1000, 250})
	if err != nil {
		t.Fatal(err)
	}
	want := Result{
		ItemsOrdered: 12001,
		TotalItems:   12250,
		TotalPacks:   4,
		Surplus:      249,
		PackSizes:    defaultSizes,
		Packs:        []Pack{{5000, 2}, {2000, 1}, {250, 1}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCalculate_ZeroOrder(t *testing.T) {
	got, err := Calculate(0, defaultSizes)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalItems != 0 || got.TotalPacks != 0 || len(got.Packs) != 0 {
		t.Fatalf("expected empty result, got %+v", got)
	}
}

func TestCalculate_SinglePackSize(t *testing.T) {
	got, err := Calculate(1001, []int{100})
	if err != nil {
		t.Fatal(err)
	}
	if want := []Pack{{100, 11}}; !reflect.DeepEqual(got.Packs, want) {
		t.Fatalf("got %v, want %v", got.Packs, want)
	}
}

func TestCalculate_HugeOrderIsFast(t *testing.T) {
	got, err := Calculate(MaxOrder, defaultSizes)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Pack{{5000, MaxOrder / 5000}}; !reflect.DeepEqual(got.Packs, want) {
		t.Fatalf("got %v, want %v", got.Packs, want)
	}

	got, err = Calculate(MaxOrder-1, []int{23, 31, 53})
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalItems < MaxOrder-1 {
		t.Fatalf("shipped fewer items than ordered: %+v", got)
	}
}

func TestCalculate_Errors(t *testing.T) {
	tests := []struct {
		name  string
		order int64
		sizes []int
		want  error
	}{
		{"no sizes", 10, nil, ErrNoPackSizes},
		{"zero size", 10, []int{0, 250}, ErrInvalidPackSize},
		{"negative size", 10, []int{-5}, ErrInvalidPackSize},
		{"size too big", 10, []int{MaxPackSize + 1}, ErrInvalidPackSize},
		{"negative order", -1, defaultSizes, ErrInvalidOrder},
		{"order too big", MaxOrder + 1, defaultSizes, ErrInvalidOrder},
		{"too complex", 1_000_000_000, []int{999_983, 999_979}, ErrTooComplex},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Calculate(tc.order, tc.sizes)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got error %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNormalizePackSizes(t *testing.T) {
	in := []int{500, 250, 500, 1000}
	got, err := NormalizePackSizes(in)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{250, 500, 1000}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if !reflect.DeepEqual(in, []int{500, 250, 500, 1000}) {
		t.Fatalf("input was modified: %v", in)
	}

	many := make([]int, MaxPackSizes+1)
	for i := range many {
		many[i] = i + 1
	}
	if _, err := NormalizePackSizes(many); !errors.Is(err, ErrTooManyPackSizes) {
		t.Fatalf("got %v, want ErrTooManyPackSizes", err)
	}
}

// TestCalculate_MatchesBruteForce checks the optimised solver against a
// straightforward reference implementation on thousands of random inputs.
func TestCalculate_MatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 3000; i++ {
		n := 1 + rng.Intn(4)
		sizes := make([]int, n)
		for j := range sizes {
			sizes[j] = 1 + rng.Intn(60)
		}
		order := int64(rng.Intn(4000))

		got, err := Calculate(order, sizes)
		if err != nil {
			t.Fatalf("Calculate(%d, %v): %v", order, sizes, err)
		}
		wantItems, wantPacks := bruteForce(order, sizes)
		if got.TotalItems != wantItems || got.TotalPacks != wantPacks {
			t.Fatalf("Calculate(%d, %v) = %d items in %d packs, want %d items in %d packs",
				order, sizes, got.TotalItems, got.TotalPacks, wantItems, wantPacks)
		}

		var items, packs int64
		for _, p := range got.Packs {
			items += int64(p.Size) * p.Quantity
			packs += p.Quantity
		}
		if items != got.TotalItems || packs != got.TotalPacks || got.Surplus != got.TotalItems-order {
			t.Fatalf("inconsistent result for (%d, %v): %+v", order, sizes, got)
		}
	}
}

// bruteForce is a direct, unoptimised implementation of the three rules.
func bruteForce(order int64, sizes []int) (items, packs int64) {
	if order == 0 {
		return 0, 0
	}
	maxSize := 0
	for _, s := range sizes {
		maxSize = max(maxSize, s)
	}
	limit := int(order) + maxSize
	best := make([]int64, limit+1)
	for t := 1; t <= limit; t++ {
		best[t] = -1
		for _, s := range sizes {
			if s <= t && best[t-s] >= 0 && (best[t] < 0 || best[t-s]+1 < best[t]) {
				best[t] = best[t-s] + 1
			}
		}
	}
	for t := int(order); t <= limit; t++ {
		if best[t] >= 0 {
			return int64(t), best[t]
		}
	}
	panic("unreachable")
}

func BenchmarkCalculate_Default(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = Calculate(123_456_789, defaultSizes)
	}
}

func BenchmarkCalculate_EdgeCase(b *testing.B) {
	sizes := []int{23, 31, 53}
	for i := 0; i < b.N; i++ {
		_, _ = Calculate(500_000, sizes)
	}
}
