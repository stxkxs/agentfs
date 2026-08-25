package layout_test

import (
	"testing"

	"github.com/stxkxs/agentfs/internal/ui/layout"
)

func TestRectEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		r    layout.Rect
		want bool
	}{
		{"zero value", layout.Rect{}, true},
		{"no width", layout.Rect{X: 3, Y: 4, W: 0, H: 9}, true},
		{"no height", layout.Rect{X: 3, Y: 4, W: 9, H: 0}, true},
		{"negative width", layout.Rect{W: -1, H: 5}, true},
		{"negative height", layout.Rect{W: 5, H: -1}, true},
		{"single cell", layout.Rect{W: 1, H: 1}, false},
		{"pane", layout.Rect{X: 10, Y: 2, W: 40, H: 12}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.r.Empty(); got != tc.want {
				t.Errorf("%+v.Empty() = %v, want %v", tc.r, got, tc.want)
			}
		})
	}
}

func TestRectOverlaps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b layout.Rect
		want bool
	}{
		{"identical", layout.Rect{W: 4, H: 4}, layout.Rect{W: 4, H: 4}, true},
		{"one shared cell", layout.Rect{W: 4, H: 4}, layout.Rect{X: 3, Y: 3, W: 4, H: 4}, true},
		{"touching on the right edge", layout.Rect{W: 4, H: 4}, layout.Rect{X: 4, W: 4, H: 4}, false},
		{"touching on the bottom edge", layout.Rect{W: 4, H: 4}, layout.Rect{Y: 4, W: 4, H: 4}, false},
		{"column split", layout.Rect{W: 28, H: 20}, layout.Rect{X: 28, W: 52, H: 20}, false},
		{"nested", layout.Rect{W: 10, H: 10}, layout.Rect{X: 2, Y: 2, W: 3, H: 3}, true},
		{"disjoint", layout.Rect{W: 4, H: 4}, layout.Rect{X: 40, Y: 40, W: 4, H: 4}, false},
		{"empty against a pane", layout.Rect{}, layout.Rect{W: 4, H: 4}, false},
		{"empty against empty", layout.Rect{}, layout.Rect{}, false},
		{"zero width inside a pane", layout.Rect{X: 1, Y: 1, W: 0, H: 4}, layout.Rect{W: 8, H: 8}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.a.Overlaps(tc.b); got != tc.want {
				t.Errorf("%+v.Overlaps(%+v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			if got := tc.b.Overlaps(tc.a); got != tc.want {
				t.Errorf("%+v.Overlaps(%+v) = %v, want %v (asymmetric)", tc.b, tc.a, got, tc.want)
			}
		})
	}
}

func TestRectContains(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		outer layout.Rect
		inner layout.Rect
		want  bool
	}{
		{"self", layout.Rect{X: 2, Y: 3, W: 8, H: 9}, layout.Rect{X: 2, Y: 3, W: 8, H: 9}, true},
		{"strictly inside", layout.Rect{W: 10, H: 10}, layout.Rect{X: 1, Y: 1, W: 8, H: 8}, true},
		{"flush against every edge", layout.Rect{W: 10, H: 10}, layout.Rect{W: 10, H: 10}, true},
		{"one column past the right edge", layout.Rect{W: 10, H: 10}, layout.Rect{X: 1, W: 10, H: 4}, false},
		{"one row past the bottom edge", layout.Rect{W: 10, H: 10}, layout.Rect{Y: 1, W: 4, H: 10}, false},
		{"starts left of the outer", layout.Rect{X: 4, W: 10, H: 10}, layout.Rect{X: 3, W: 4, H: 4}, false},
		{"starts above the outer", layout.Rect{Y: 4, W: 10, H: 10}, layout.Rect{Y: 3, W: 4, H: 4}, false},
		{"empty inner", layout.Rect{W: 10, H: 10}, layout.Rect{X: 900, Y: 900}, true},
		{"empty inner in an empty outer", layout.Rect{}, layout.Rect{}, true},
		{"pane in an empty outer", layout.Rect{}, layout.Rect{W: 1, H: 1}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.outer.Contains(tc.inner); got != tc.want {
				t.Errorf("%+v.Contains(%+v) = %v, want %v", tc.outer, tc.inner, got, tc.want)
			}
		})
	}
}

// Contains and Overlaps are used together to prove a pane is placed inside the
// terminal and clear of its neighbours, so they must not disagree: a non-empty
// rect held by another shares cells with it.
func TestContainsImpliesOverlaps(t *testing.T) {
	t.Parallel()
	coords := []int{-2, 0, 5}
	sizes := []int{0, 1, 3, 7}
	for _, ax := range coords {
		for _, aw := range sizes {
			for _, bx := range coords {
				for _, bw := range sizes {
					a := layout.Rect{X: ax, Y: ax, W: aw, H: aw}
					b := layout.Rect{X: bx, Y: bx, W: bw, H: bw}
					if a.Contains(b) && !b.Empty() && !a.Overlaps(b) {
						t.Fatalf("%+v contains %+v but does not overlap it", a, b)
					}
				}
			}
		}
	}
}
