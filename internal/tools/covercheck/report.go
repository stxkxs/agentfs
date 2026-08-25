package main

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

// tolerance is the gap below which two percentages are the same measurement.
// A recorded value is stored to one decimal place, so a gap narrower than half
// of that place is the file's precision rather than a package that moved.
const tolerance = 0.05

// totalKey labels the repository-wide row, the one the global floor gates.
const totalKey = "total"

// row is one package's line of the coverage table.
type row struct {
	// key is the package key, or [totalKey] for the repository row.
	key string
	// floor is the percentage the package must meet, when one is declared.
	floor    float64
	hasFloor bool
	// actual is the percentage the profile measured, when it covers the
	// package at all.
	actual  float64
	hasData bool
	// recorded is the percentage the floors file carries, when it carries one.
	recorded  float64
	hasRecord bool
	// below and regressed are the two ways a row fails: under its floor, and
	// under its own recorded high-water mark.
	below     bool
	regressed bool
}

// failed reports whether this row alone fails the gate.
func (r row) failed() bool { return r.below || r.regressed }

// verdict is the word the status column carries.
func (r row) verdict() string {
	switch {
	case r.below:
		return "below floor"
	case r.regressed:
		return "regressed"
	case !r.hasData:
		return "no data"
	default:
		return "ok"
	}
}

// report is an evaluated gate: one row per package, the repository total, and
// the recorded values a ratchet raises.
type report struct {
	rows    []row
	total   row
	updates map[string]float64
	ratchet bool
}

// evaluate scores every package the profile covers together with every
// package the floors file names in either section, so a package that stopped
// being tested is a visible row rather than an absent one. A recorded value
// gates its package whether or not that package also carries a floor, which
// is what makes the ratchet catch a deleted test file.
func evaluate(f *floors, p *profile, ratchet bool) report {
	actual := make(map[string]float64)
	for importPath, t := range p.packages() {
		actual[f.key(importPath)] = t.percent()
	}
	keys := make([]string, 0, len(actual)+len(f.pkg)+len(f.recorded))
	for _, named := range []map[string]float64{actual, f.pkg, f.recorded} {
		for key := range named {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)

	rep := report{rows: make([]row, 0, len(keys)), updates: make(map[string]float64), ratchet: ratchet}
	for _, key := range keys {
		rep.rows = append(rep.rows, rep.score(f, key, actual))
	}
	total := p.total()
	rep.total = row{key: totalKey, floor: f.global, hasFloor: true, actual: total.percent(), hasData: total.total > 0}
	rep.total.below = below(rep.total.actual, rep.total.floor)
	return rep
}

// score builds one package's row and, under a ratchet, notes the value to
// record for it.
func (rep *report) score(f *floors, key string, actual map[string]float64) row {
	r := row{key: key}
	r.actual, r.hasData = actual[key]
	r.floor, r.hasFloor = f.pkg[key]
	r.recorded, r.hasRecord = f.recorded[key]
	r.below = r.hasFloor && below(r.actual, r.floor)
	if !rep.ratchet {
		return r
	}
	r.regressed = r.hasRecord && below(r.actual, r.recorded)
	if r.hasData && (!r.hasRecord || r.actual > r.recorded+tolerance) {
		rep.updates[key] = r.actual
	}
	return r
}

// below reports whether actual misses limit by more than storage rounding.
func below(actual, limit float64) bool { return actual+tolerance < limit }

// failures returns every row that fails the gate, in table order.
func (rep *report) failures() []row {
	failed := make([]row, 0, len(rep.rows))
	for _, r := range rep.rows {
		if r.failed() {
			failed = append(failed, r)
		}
	}
	if rep.total.failed() {
		failed = append(failed, rep.total)
	}
	return failed
}

// exit is the process status the report calls for.
func (rep *report) exit() int {
	if len(rep.failures()) > 0 {
		return exitBelow
	}
	return exitOK
}

// The table's geometry: the three percentage columns share one width, and the
// status column closes the line and needs none. The rule is measured from the
// header rather than recomputed from the column widths, so the two cannot
// disagree about how wide the table is.
const (
	rowFormat     = "%-*s  %*s  %*s  %*s  %s\n"
	percentColumn = 9
	statusHeader  = "status"
)

// write renders the coverage table, one row per package and one for the
// repository total.
func (rep *report) write(w io.Writer) error {
	o := &out{w: w}
	width := columns("package")
	for _, r := range rep.rows {
		width = max(width, columns(r.key))
	}
	width = max(width, columns(totalKey))

	header := fmt.Sprintf(rowFormat, width, "package",
		percentColumn, "floor", percentColumn, "actual", percentColumn, "recorded", statusHeader)
	rule := strings.Repeat("-", columns(strings.TrimSuffix(header, "\n"))) + "\n"
	o.printf("%s", header)
	o.printf("%s", rule)
	for _, r := range rep.rows {
		writeRow(o, width, r)
	}
	o.printf("%s", rule)
	writeRow(o, width, rep.total)
	return o.err
}

// columns counts the table columns a string occupies, which is the unit a fmt
// width pads to.
func columns(s string) int { return utf8.RuneCountInString(s) }

// writeRow renders one line of the table.
func writeRow(o *out, width int, r row) {
	o.printf(rowFormat, width, r.key,
		percentColumn, cell(r.floor, r.hasFloor),
		percentColumn, cell(r.actual, r.hasData),
		percentColumn, cell(r.recorded, r.hasRecord),
		r.verdict())
}

// cell renders a percentage column, marking a value the file does not carry.
func cell(percent float64, present bool) string {
	if !present {
		return "-"
	}
	return formatPercent(percent)
}

// explain names every failure, with the measurement it made and the limit
// that measurement missed. A package the profile does not cover reports that
// absence rather than the zero it scores as, because a package with no tests
// left and a package whose tests all fail are different repairs.
func (rep *report) explain(w io.Writer) error {
	o := &out{w: w}
	for _, r := range rep.failures() {
		switch {
		case r.below && r.hasData:
			o.printf("%s: %s%% is below the %s%% floor\n", r.key, formatPercent(r.actual), formatPercent(r.floor))
		case r.below:
			o.printf("%s: the profile covers no statements in it, and its floor is %s%%\n", r.key, formatPercent(r.floor))
		case r.hasData:
			o.printf("%s: %s%% fell from the recorded %s%%\n", r.key, formatPercent(r.actual), formatPercent(r.recorded))
		default:
			o.printf("%s: the profile covers no statements in it, and it recorded %s%%\n", r.key, formatPercent(r.recorded))
		}
	}
	return o.err
}
