package main

import (
	"fmt"
	"strings"
)

// The markdown a page is assembled from. Every page uses these rather than
// writing its own separators, so the seven pages read as one document set.

// title writes a level-one heading followed by a paragraph.
func title(b *strings.Builder, heading, lead string) {
	fmt.Fprintf(b, "# %s\n\n%s\n\n", heading, lead)
}

// section writes a level-two heading.
func section(b *strings.Builder, heading string) {
	fmt.Fprintf(b, "## %s\n\n", heading)
}

// subsection writes a level-three heading.
func subsection(b *strings.Builder, heading string) {
	fmt.Fprintf(b, "### %s\n\n", heading)
}

// para writes a paragraph.
func para(b *strings.Builder, text string) {
	b.WriteString(text)
	b.WriteString("\n\n")
}

// fence writes a fenced block in the given language, which may be empty.
func fence(b *strings.Builder, language, body string) {
	fmt.Fprintf(b, "```%s\n%s\n```\n\n", language, strings.TrimRight(body, "\n"))
}

// code wraps s in a code span.
func code(s string) string { return "`" + s + "`" }

// codeList renders values as code spans separated by commas, for a vocabulary
// written into a sentence.
func codeList(values []string) string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, code(v))
	}
	return strings.Join(out, ", ")
}

// yesNo renders a boolean for a matrix cell.
func yesNo(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}

// table accumulates a markdown table.
type table struct {
	head []string
	rows [][]string
}

// newTable returns a table with the given column headings.
func newTable(head ...string) *table { return &table{head: head} }

// row appends one row.
func (t *table) row(cells ...string) { t.rows = append(t.rows, cells) }

// write renders the table. A row whose width does not match the header is an
// error rather than output, because a table that has lost a column renders as
// prose and reads as though the missing value were never there.
func (t *table) write(b *strings.Builder) error {
	writeRow(b, t.head)
	rule := make([]string, len(t.head))
	for i := range rule {
		rule[i] = "---"
	}
	writeRow(b, rule)
	for i, r := range t.rows {
		if len(r) != len(t.head) {
			return fmt.Errorf("row %d holds %d cells, the header holds %d", i, len(r), len(t.head))
		}
		writeRow(b, r)
	}
	b.WriteByte('\n')
	return nil
}

// writeRow renders one row, neutralizing what a cell value could do to the
// table: a pipe ends a column and any line break ends the row.
func writeRow(b *strings.Builder, cells []string) {
	b.WriteByte('|')
	for _, c := range cells {
		b.WriteByte(' ')
		b.WriteString(cell(c))
		b.WriteString(" |")
	}
	b.WriteByte('\n')
}

// cell returns s as a table cell: one line, with pipes escaped.
func cell(s string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "|", `\|`)
}
