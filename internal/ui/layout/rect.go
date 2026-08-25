package layout

// Rect is a screen rectangle measured in terminal cells. X and Y are
// zero-based, and the rect covers the cells from X through X+W-1 and from Y
// through Y+H-1. A rect with a width or height of zero covers nothing.
type Rect struct{ X, Y, W, H int }

// Empty reports whether the rect covers no cells.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Overlaps reports whether r and o share at least one cell. An empty rect
// covers no cells, so it overlaps nothing, including itself.
func (r Rect) Overlaps(o Rect) bool {
	if r.Empty() || o.Empty() {
		return false
	}
	return r.X < o.X+o.W && o.X < r.X+r.W &&
		r.Y < o.Y+o.H && o.Y < r.Y+r.H
}

// Contains reports whether every cell of o lies within r. An empty o covers no
// cells and so is contained by any rect; an empty r contains nothing but empty
// rects. Containment is the check a caller applies to prove a pane cannot write
// outside the terminal.
func (r Rect) Contains(o Rect) bool {
	if o.Empty() {
		return true
	}
	if r.Empty() {
		return false
	}
	return o.X >= r.X && o.Y >= r.Y &&
		o.X+o.W <= r.X+r.W && o.Y+o.H <= r.Y+r.H
}
