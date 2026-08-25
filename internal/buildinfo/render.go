package buildinfo

import (
	"fmt"
	"strings"

	"github.com/stxkxs/agentfs/internal/textx"
)

// labelWidth is the column the values in [Info.Long] start at, wide enough for
// the longest label plus its separating space.
const labelWidth = 8

// String returns the identity on one line, the form a --version flag prints.
func (i Info) String() string {
	return fmt.Sprintf("%s %s (%s, built %s, %s, schema %s)",
		Name, i.Version, i.Commit, i.BuildDate, i.GoVersion, i.Schema)
}

// Long returns the identity as one fact per line, the form a version
// subcommand prints. The result carries no trailing newline, so the caller
// decides how it terminates.
func (i Info) Long() string {
	rows := []struct{ label, value string }{
		{"commit", i.Commit},
		{"built", i.BuildDate},
		{"go", i.GoVersion},
		{"schema", i.Schema},
	}

	var b strings.Builder
	b.WriteString(Name)
	b.WriteByte(' ')
	b.WriteString(i.Version)
	for _, row := range rows {
		b.WriteString("\n  ")
		b.WriteString(textx.Pad(row.label, labelWidth))
		b.WriteString(row.value)
	}
	return b.String()
}
