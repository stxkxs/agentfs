package metrics

import (
	"testing"
	"time"
)

func TestPercentileNearestRank(t *testing.T) {
	t.Parallel()
	ms := func(n ...int) []time.Duration {
		out := make([]time.Duration, 0, len(n))
		for _, v := range n {
			out = append(out, time.Duration(v)*time.Millisecond)
		}
		return out
	}
	ten := ms(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	cases := []struct {
		name   string
		sorted []time.Duration
		p      int
		want   time.Duration
	}{
		{"empty has no percentile", nil, 50, 0},
		{"one value answers every rank", ms(7), 50, 7 * time.Millisecond},
		{"one value at the top rank", ms(7), 99, 7 * time.Millisecond},
		{"two values, median is the lower", ms(2, 8), 50, 2 * time.Millisecond},
		{"two values, upper rank is the higher", ms(2, 8), 90, 8 * time.Millisecond},
		{"median of ten", ten, 50, 5 * time.Millisecond},
		{"ninetieth of ten", ten, 90, 9 * time.Millisecond},
		{"ninety-ninth of ten is the largest", ten, 99, 10 * time.Millisecond},
		{"rank zero is the smallest", ten, 0, 1 * time.Millisecond},
		{"rank one hundred is the largest", ten, 100, 10 * time.Millisecond},
		{"a rank past the top clamps", ten, 101, 10 * time.Millisecond},
		{"a rank below zero clamps", ten, -1, 1 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := percentile(tc.sorted, tc.p); got != tc.want {
				t.Errorf("percentile(%v, %d) = %v, want %v", tc.sorted, tc.p, got, tc.want)
			}
		})
	}
}

// TestPercentileReturnsAnObservedValue is the property the nearest-rank method
// is chosen for: a reported percentile is a duration something actually took,
// so a reader can go looking for the span that produced it.
func TestPercentileReturnsAnObservedValue(t *testing.T) {
	t.Parallel()
	sorted := make([]time.Duration, 0, 64)
	for i := range 64 {
		sorted = append(sorted, time.Duration(i*i)*time.Microsecond)
	}
	for p := -5; p <= 105; p++ {
		got := percentile(sorted, p)
		found := false
		for _, d := range sorted {
			if d == got {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("percentile(_, %d) = %v, which no observation held", p, got)
		}
	}
}
