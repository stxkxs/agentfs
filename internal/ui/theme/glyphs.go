package theme

// Glyphs are the non-colour carriers of meaning. Every distinction the palette
// draws in colour is drawn here as well, so a monochrome terminal and a golden
// file keep every distinction the colour makes.
//
// Each glyph occupies exactly one terminal cell. A pane's column arithmetic
// budgets one cell for a mark, so a two-cell glyph shifts every column to its
// right by one.
type Glyphs struct {
	// Status markers, one per [StatusRole].
	Running, Idle, Blocked, Error, Done, Unknown string
	// Severity markers, one per [SeverityRole].
	Info, Warning, Severe string
	// Tree markers: a node with hidden children, a node with shown children,
	// and a node with none.
	Collapsed, Expanded, Leaf string
	// Row markers: the selected row, a row touched inside the recency window,
	// a row whose text was clipped, and a row whose agent missed its heartbeat.
	Cursor, Recent, Truncated, Stale string
	// Change markers, one per filesystem event kind.
	Created, Modified, Removed, Renamed string
	// VerticalBar separates columns within a row.
	VerticalBar string
}

// UnicodeGlyphs returns the default marks, drawn from the geometric shapes and
// dingbats blocks.
func UnicodeGlyphs() Glyphs {
	return Glyphs{
		Running: "●", Idle: "○", Blocked: "◐", Error: "✗", Done: "✓", Unknown: "◌",
		Info: "▪", Warning: "▲", Severe: "◆",
		Collapsed: "▸", Expanded: "▾", Leaf: "·",
		Cursor: "❯", Recent: "•", Truncated: "…", Stale: "◇",
		Created: "✚", Modified: "✎", Removed: "✖", Renamed: "→",
		VerticalBar: "│",
	}
}

// ASCIIGlyphs returns marks confined to printable ASCII, for a terminal or font
// that draws a replacement box in place of U+25CF. Every distinction
// [UnicodeGlyphs] draws within a column survives; across columns the shapes
// repeat, because printable ASCII does not hold a legible mark per state and
// position already tells a status column from a change column.
func ASCIIGlyphs() Glyphs {
	return Glyphs{
		Running: ">", Idle: "o", Blocked: "!", Error: "X", Done: "*", Unknown: "?",
		Info: "i", Warning: "!", Severe: "X",
		Collapsed: "+", Expanded: "-", Leaf: ".",
		Cursor: ">", Recent: "*", Truncated: "~", Stale: "!",
		Created: "+", Modified: "~", Removed: "-", Renamed: ">",
		VerticalBar: "|",
	}
}
