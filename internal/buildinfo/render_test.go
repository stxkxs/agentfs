package buildinfo

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/textx"
)

func sample() Info {
	return Info{
		Version:   "1.4.0",
		Commit:    "0123456789ab",
		BuildDate: "2026-08-24T13:00:00Z",
		GoVersion: testGo,
		Schema:    agentstate.SchemaVersion,
	}
}

func TestString(t *testing.T) {
	t.Parallel()

	want := "agentfs 1.4.0 (0123456789ab, built 2026-08-24T13:00:00Z, go1.26.3, schema agentfs/v1)"
	if got := sample().String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestStringIsOneLine(t *testing.T) {
	t.Parallel()

	got := resolve("1.4.0\nrm -rf", "abc\r\n", "2026-08-24T13:00:00Z", nil).String()
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("String() = %q, want one line", got)
	}
}

func TestLong(t *testing.T) {
	t.Parallel()

	want := strings.Join([]string{
		"agentfs 1.4.0",
		"  commit  0123456789ab",
		"  built   2026-08-24T13:00:00Z",
		"  go      go1.26.3",
		"  schema  agentfs/v1",
	}, "\n")
	if got := sample().Long(); got != want {
		t.Errorf("Long() =\n%s\nwant\n%s", got, want)
	}
}

func TestLongAlignsItsValues(t *testing.T) {
	t.Parallel()

	lines := strings.Split(sample().Long(), "\n")
	if len(lines) != 5 {
		t.Fatalf("Long() has %d lines, want one heading and four facts", len(lines))
	}
	if strings.HasSuffix(sample().Long(), "\n") {
		t.Error("Long() ends with a newline, want the caller to decide how it terminates")
	}

	column := -1
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			t.Fatalf("fact line %q carries no value", line)
		}
		at := strings.Index(line, fields[1])
		if column == -1 {
			column = at
		}
		if at != column {
			t.Errorf("line %q starts its value at column %d, want %d", line, at, column)
		}
		if textx.Width(fields[0]) >= labelWidth {
			t.Errorf("label %q is %d cells wide, want less than the %d it is padded to", fields[0], textx.Width(fields[0]), labelWidth)
		}
	}
}

// TestRenderersReportEveryField holds both renderers to the struct. A fact
// added to [Info] that no rendered form carries is a fact the identity resolves
// and never reports, which no assertion on a fixed expected string catches.
func TestRenderersReportEveryField(t *testing.T) {
	t.Parallel()

	info := sample()
	renders := map[string]string{"String()": info.String(), "Long()": info.Long()}

	v := reflect.ValueOf(info)
	for i := range v.NumField() {
		field := v.Type().Field(i)
		if v.Field(i).Kind() != reflect.String {
			t.Fatalf("%s is a %s: every fact is rendered as text", field.Name, v.Field(i).Kind())
		}
		value := v.Field(i).String()
		for form, out := range renders {
			if !strings.Contains(out, value) {
				t.Errorf("%s omits %s (%q), got %q", form, field.Name, value, out)
			}
		}
	}
}
