package main

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Section headers a floors file recognizes.
const (
	sectionPackages = "packages"
	sectionRecorded = "recorded"
)

// defaultIndent is the indent written under a section header when the file
// holds no indented line to copy the width from.
const defaultIndent = "  "

// floors is a parsed floors file: the gate it declares, and the lines it was
// read from so a ratchet rewrites values in place and leaves comments,
// ordering and spacing untouched.
type floors struct {
	// module is the import path package keys are written relative to.
	module string
	// global is the floor the repository total must meet.
	global float64
	// pkg is the floor declared for each package key.
	pkg map[string]float64
	// recorded is the coverage last measured for each package key.
	recorded map[string]float64
	// lines is the file as read, without line terminators.
	lines []string
	// at is the index in lines of each recorded entry.
	at map[string]int
	// end is the index just past the recorded section, or -1 when the file
	// declares no such section.
	end int
	// indent is the leading whitespace a section entry carries.
	indent string
}

// floorParser accumulates one floors file. The bookkeeping a parse needs and
// the result it produces are separate types so the result carries nothing a
// caller has to ignore.
type floorParser struct {
	f       *floors
	section string
	seen    map[string]bool
}

// parseFloors reads a floors file.
//
// The format is a two-level key/value file. A top-level key sits in column
// zero; a section entry is indented beneath its header:
//
//	module: github.com/stxkxs/agentfs
//	global: 60
//	packages:
//	  internal/agentstate: 85
//	recorded:
//	  internal/agentstate: 91.4
//
// A blank line is ignored, and so is a line whose first non-space character is
// "#". A percent is a decimal from 0 to 100. Package keys are import paths,
// written relative to module when module is declared and it contains them.
func parseFloors(data []byte) (*floors, error) {
	p := &floorParser{
		f: &floors{
			pkg:      make(map[string]float64),
			recorded: make(map[string]float64),
			at:       make(map[string]int),
			end:      -1,
			indent:   defaultIndent,
		},
		seen: make(map[string]bool),
	}
	p.f.lines = splitLines(data)
	for i, raw := range p.f.lines {
		if err := p.line(i, raw); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
	}
	return p.f, nil
}

// line folds one line of the file into the parse.
func (p *floorParser) line(i int, raw string) error {
	line := strings.TrimRight(raw, " \t\r")
	body := strings.TrimLeft(line, " \t")
	if body == "" || strings.HasPrefix(body, "#") {
		return nil
	}
	key, value, ok := strings.Cut(body, ":")
	if !ok {
		return fmt.Errorf("want %q, got %q", "key: value", abbrev(body))
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" {
		return fmt.Errorf("want a key before the colon, got %q", abbrev(body))
	}
	if len(line) == len(body) {
		return p.top(key, value, i)
	}
	p.f.indent = line[:len(line)-len(body)]
	return p.entry(key, value, i)
}

// claim registers a key within a scope and refuses a repeat, which would
// otherwise resolve to whichever of the two lines the parser read last.
func (p *floorParser) claim(scope, key string) error {
	id := scope + "\x00" + key
	if p.seen[id] {
		return fmt.Errorf("duplicate key %q", abbrev(key))
	}
	p.seen[id] = true
	return nil
}

// top folds a key that sits in column zero.
func (p *floorParser) top(key, value string, i int) error {
	if err := p.claim("", key); err != nil {
		return err
	}
	switch key {
	case "module":
		if value == "" {
			return fmt.Errorf("%q wants an import path", key)
		}
		p.f.module = value
		p.section = ""
		return nil
	case "global":
		percent, err := parsePercent(value)
		if err != nil {
			return err
		}
		p.f.global = percent
		p.section = ""
		return nil
	case sectionPackages, sectionRecorded:
		if value != "" {
			return fmt.Errorf("%q takes no value; list its entries indented beneath it", key)
		}
		if key == sectionRecorded {
			p.f.end = i + 1
		}
		p.section = key
		return nil
	default:
		return fmt.Errorf("unknown key %q", abbrev(key))
	}
}

// entry folds an indented key into the section that opened above it.
func (p *floorParser) entry(key, value string, i int) error {
	if p.section == "" {
		return fmt.Errorf("%q is indented under no section header", abbrev(key))
	}
	if err := p.claim(p.section, key); err != nil {
		return err
	}
	percent, err := parsePercent(value)
	if err != nil {
		return err
	}
	if p.section == sectionPackages {
		p.f.pkg[key] = percent
		return nil
	}
	p.f.recorded[key] = percent
	p.f.at[key] = i
	p.f.end = i + 1
	return nil
}

// parsePercent reads a coverage percentage.
func parsePercent(text string) (float64, error) {
	percent, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(percent) || percent < 0 || percent > 100 {
		return 0, fmt.Errorf("want a percent from 0 to 100, got %q", abbrev(text))
	}
	return percent, nil
}

// formatPercent renders a percentage the way a floors file stores it.
func formatPercent(percent float64) string {
	return strconv.FormatFloat(percent, 'f', 1, 64)
}

// key returns the package key an import path is listed under: relative to
// module when module contains it, and the import path itself otherwise.
func (f *floors) key(importPath string) string {
	switch {
	case f.module == "":
		return importPath
	case importPath == f.module:
		return "."
	default:
		if rest, ok := strings.CutPrefix(importPath, f.module+"/"); ok {
			return rest
		}
		return importPath
	}
}

// render returns the file with the given recorded values written into it. An
// entry the file already lists is rewritten where it stands; one it does not
// is appended to the recorded section, which is created when it is absent.
func (f *floors) render(updates map[string]float64) []byte {
	lines := slices.Clone(f.lines)
	keys := make([]string, 0, len(updates))
	for key := range updates {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	added := make([]string, 0, len(keys))
	for _, key := range keys {
		entry := f.indent + key + ": " + formatPercent(updates[key])
		if i, ok := f.at[key]; ok {
			lines[i] = entry
			continue
		}
		added = append(added, entry)
	}
	if len(added) > 0 {
		at := f.end
		if at < 0 {
			lines = append(lines, sectionRecorded+":")
			at = len(lines)
		}
		lines = slices.Insert(lines, at, added...)
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// splitLines splits a file into lines, dropping the terminator of the last
// one so a join restores the file rather than growing it by a blank line.
func splitLines(data []byte) []string {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
