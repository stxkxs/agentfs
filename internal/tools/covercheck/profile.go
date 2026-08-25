package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"strconv"
	"strings"
)

// maxProfileLine caps one profile record. A coverage profile is machine
// written and its longest line is a path plus five small integers, so a line
// past this cap is a corrupt or hostile file rather than a large one.
const maxProfileLine = 64 << 10

// tally is a covered-over-total statement count.
type tally struct {
	covered int
	total   int
}

// percent returns covered statements as a percentage of total statements.
// A region holding no statements is complete by definition and reports 100,
// which keeps a package of declarations from failing a floor it cannot move.
func (t tally) percent() float64 {
	if t.total == 0 {
		return 100
	}
	return float64(t.covered) / float64(t.total) * 100
}

// add accumulates o into t.
func (t *tally) add(o tally) {
	t.covered += o.covered
	t.total += o.total
}

// span identifies one counted region of one source file. A profile merged
// from several test binaries repeats a region once per binary, so keying by
// span makes the execution count a sum while the statement count contributes
// exactly once.
type span struct {
	file      string
	startLine int
	startCol  int
	endLine   int
	endCol    int
}

// region is what a profile records about one span.
type region struct {
	stmts int
	count int
}

// profile is a parsed Go coverage profile.
type profile struct {
	mode    string
	regions map[span]region
}

// modes are the counting strategies "go test -covermode" produces. A profile
// declaring anything else was not written by the toolchain and is refused
// rather than counted with a guessed meaning.
var modes = []string{"set", "count", "atomic"}

// parseProfile reads a coverage profile: a "mode:" header followed by one
// record per counted region, "path/file.go:line.col,line.col stmts count".
func parseProfile(r io.Reader) (*profile, error) {
	p := &profile{regions: make(map[span]region)}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4<<10), maxProfileLine)
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if p.mode == "" {
			mode, err := parseMode(line)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
			p.mode = mode
			continue
		}
		key, rec, err := parseRegion(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		p.merge(key, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	if p.mode == "" {
		return nil, errors.New("profile holds no mode header")
	}
	return p, nil
}

// merge folds one record into the profile, summing counts across the binaries
// that reported the same region. The sum saturates: a wrapped total would read
// as never executed and report a covered region as uncovered.
func (p *profile) merge(key span, rec region) {
	prior, seen := p.regions[key]
	if seen {
		rec.stmts = prior.stmts
		rec.count = saturatingAdd(prior.count, rec.count)
	}
	p.regions[key] = rec
}

// saturatingAdd adds two non-negative counts, clamping at the largest int
// rather than wrapping past it.
func saturatingAdd(a, b int) int {
	if sum := a + b; sum >= a {
		return sum
	}
	return math.MaxInt
}

// parseMode reads the header that opens a profile.
func parseMode(line string) (string, error) {
	mode, ok := strings.CutPrefix(line, "mode: ")
	if !ok {
		return "", fmt.Errorf("want a %q header, got %q", "mode: ", abbrev(line))
	}
	mode = strings.TrimSpace(mode)
	for _, known := range modes {
		if mode == known {
			return mode, nil
		}
	}
	return "", fmt.Errorf("unknown coverage mode %q, want one of %s", abbrev(mode), strings.Join(modes, ", "))
}

// parseRegion reads one record. The file name is separated from the position
// range at the last colon and from the counts at the last two spaces, so a
// path holding either character parses the same as one that does not.
func parseRegion(line string) (span, region, error) {
	head, countText, ok := cutLast(line, ' ')
	if !ok {
		return span{}, region{}, recordError(line)
	}
	locator, stmtsText, ok := cutLast(head, ' ')
	if !ok {
		return span{}, region{}, recordError(line)
	}
	file, positions, ok := cutLast(locator, ':')
	if !ok || file == "" {
		return span{}, region{}, recordError(line)
	}
	startText, endText, ok := strings.Cut(positions, ",")
	if !ok {
		return span{}, region{}, recordError(line)
	}
	startLine, startCol, err := parsePosition(startText)
	if err != nil {
		return span{}, region{}, err
	}
	endLine, endCol, err := parsePosition(endText)
	if err != nil {
		return span{}, region{}, err
	}
	stmts, err := parseCount(stmtsText, "statement count")
	if err != nil {
		return span{}, region{}, err
	}
	count, err := parseCount(countText, "execution count")
	if err != nil {
		return span{}, region{}, err
	}
	key := span{file: path.Clean(file), startLine: startLine, startCol: startCol, endLine: endLine, endCol: endCol}
	return key, region{stmts: stmts, count: count}, nil
}

// recordError names the shape a record must take.
func recordError(line string) error {
	return fmt.Errorf("want %q, got %q", "file.go:line.col,line.col stmts count", abbrev(line))
}

// parsePosition reads a "line.col" pair. Both are 1-based in a profile, so
// zero is rejected along with a negative.
func parsePosition(text string) (line, col int, err error) {
	lineText, colText, ok := strings.Cut(text, ".")
	if !ok {
		return 0, 0, fmt.Errorf("want a %q position, got %q", "line.col", abbrev(text))
	}
	line, err = strconv.Atoi(lineText)
	if err != nil || line < 1 {
		return 0, 0, fmt.Errorf("want a positive line number, got %q", abbrev(lineText))
	}
	col, err = strconv.Atoi(colText)
	if err != nil || col < 1 {
		return 0, 0, fmt.Errorf("want a positive column number, got %q", abbrev(colText))
	}
	return line, col, nil
}

// parseCount reads one of the two trailing integers, named by role so the
// error says which of them the file got wrong.
func parseCount(text, role string) (int, error) {
	n, err := strconv.Atoi(text)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("want a non-negative %s, got %q", role, abbrev(text))
	}
	return n, nil
}

// packages returns the statement tally of every package the profile covers.
// A profile file name is an import path with the source file appended, so the
// package is that path's parent.
func (p *profile) packages() map[string]tally {
	out := make(map[string]tally, len(p.regions))
	for key, rec := range p.regions {
		t := out[path.Dir(key.file)]
		t.add(regionTally(rec))
		out[path.Dir(key.file)] = t
	}
	return out
}

// total returns the statement tally across every package the profile covers.
func (p *profile) total() tally {
	var t tally
	for _, rec := range p.regions {
		t.add(regionTally(rec))
	}
	return t
}

// regionTally scores one region: its statements count as covered when the
// region executed at least once.
func regionTally(rec region) tally {
	if rec.count > 0 {
		return tally{covered: rec.stmts, total: rec.stmts}
	}
	return tally{total: rec.stmts}
}

// cutLast splits text around the last instance of sep.
func cutLast(text string, sep byte) (before, after string, found bool) {
	i := strings.LastIndexByte(text, sep)
	if i < 0 {
		return text, "", false
	}
	return text[:i], text[i+1:], true
}

// abbrev shortens v for an error message, cutting on a rune boundary so the
// result stays valid UTF-8.
func abbrev(v string) string {
	const limit = 60
	runes := []rune(v)
	if len(runes) <= limit {
		return v
	}
	return string(runes[:limit]) + "..."
}
