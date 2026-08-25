package config

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
)

// A ceiling that is not in the table cannot be set and cannot be documented, so
// a field added to Config without a row fails here rather than shipping as a
// knob nobody can reach.
func TestLimitsNamesEveryConfigField(t *testing.T) {
	fields := reflect.VisibleFields(reflect.TypeOf(Config{}))
	limits := Limits()
	if len(limits) != len(fields) {
		t.Fatalf("Limits() has %d rows, Config has %d fields:\nrows:   %v\nfields: %v",
			len(limits), len(fields), names(limits), fieldNames(fields))
	}
	for i, f := range fields {
		if limits[i].Name != f.Name {
			t.Errorf("Limits()[%d].Name = %q, Config field %d is %q; the table's order is Config's field order",
				i, limits[i].Name, i, f.Name)
		}
	}
}

func names(limits []Limit) []string {
	out := make([]string, 0, len(limits))
	for _, l := range limits {
		out = append(out, l.Name)
	}
	return out
}

func fieldNames(fields []reflect.StructField) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Name)
	}
	return out
}

// A unit decides how a flag is registered, how its value is parsed and how its
// default is rendered, so a row's unit has to match the field's Go type.
func TestLimitUnitsMatchFieldTypes(t *testing.T) {
	typ := reflect.TypeOf(Config{})
	duration := reflect.TypeOf(time.Duration(0))
	strSlice := reflect.TypeOf([]string(nil))
	for _, l := range Limits() {
		f, ok := typ.FieldByName(l.Name)
		if !ok {
			t.Errorf("%s: Config has no such field", l.Name)
			continue
		}
		var want bool
		switch l.Unit {
		case UnitBytes, UnitCount:
			want = f.Type.Kind() == reflect.Int || f.Type.Kind() == reflect.Int64
			want = want && f.Type != duration && f.Type != modeType
		case UnitDuration:
			want = f.Type == duration
		case UnitPath:
			want = f.Type.Kind() == reflect.String
		case UnitEnum:
			want = f.Type.Kind() == reflect.String || f.Type == modeType
		case UnitBool:
			want = f.Type.Kind() == reflect.Bool
		case UnitList:
			want = f.Type == strSlice
		default:
			t.Errorf("%s: unit %q is outside the vocabulary", l.Name, l.Unit)
			continue
		}
		if !want {
			t.Errorf("%s: unit %q does not describe a %s", l.Name, l.Unit, f.Type)
		}
	}
}

// An operator who knows the flag knows the environment variable.
func TestLimitNamesFollowTheFieldName(t *testing.T) {
	for _, l := range Limits() {
		if got := kebab(l.Name); got != l.Flag {
			t.Errorf("%s: flag is %q, want %q", l.Name, l.Flag, got)
		}
		if want := EnvPrefix + strings.ToUpper(strings.ReplaceAll(l.Flag, "-", "_")); l.Env != want {
			t.Errorf("%s: env is %q, want %q", l.Name, l.Env, want)
		}
	}
}

// The environment spelling is the one an operator reads in the reference, so
// the prefix is held to its literal form rather than to whatever EnvPrefix
// happens to carry.
func TestLimitEnvVariablesCarryTheAgentfsPrefix(t *testing.T) {
	const want = "AGENTFS_"
	if EnvPrefix != want {
		t.Errorf("EnvPrefix = %q, want %q", EnvPrefix, want)
	}
	for _, l := range Limits() {
		if !strings.HasPrefix(l.Env, want) {
			t.Errorf("%s: env is %q, want it to start %q", l.Name, l.Env, want)
		}
	}
}

// A flag offers its value set from the row, so the row carries the whole
// vocabulary and not merely one that contains the default.
func TestLimitEnumRowsCarryTheirWholeVocabulary(t *testing.T) {
	enums := map[string][]string{}
	for _, l := range Limits() {
		enums[l.Name] = l.Enum
	}
	want := map[string][]string{
		"Watch": {"auto", "notify", "sweep", "hybrid"},
		"Color": {"auto", "always", "never"},
	}
	for name, w := range want {
		if !slices.Equal(enums[name], w) {
			t.Errorf("%s enum = %v, want %v", name, enums[name], w)
		}
	}
}

func TestLimitNamesAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, l := range Limits() {
		for _, key := range []string{"flag " + l.Flag, "env " + l.Env, "name " + l.Name} {
			if prev, dup := seen[key]; dup {
				t.Errorf("%s and %s share %s", prev, l.Name, key)
			}
			seen[key] = l.Name
		}
	}
}

func TestLimitProse(t *testing.T) {
	for _, l := range Limits() {
		switch {
		case l.Summary == "":
			t.Errorf("%s: no summary", l.Name)
		case !strings.HasPrefix(l.Summary, l.Name):
			t.Errorf("%s: summary starts %q, want it to open with the field name", l.Name, first(l.Summary))
		case !strings.HasSuffix(l.Summary, "."):
			t.Errorf("%s: summary is not a sentence: %q", l.Name, l.Summary)
		}
		if l.Default == "" && l.Unit != UnitList {
			t.Errorf("%s: no rendered default", l.Name)
		}
	}
}

func first(s string) string {
	if len(s) > 24 {
		return s[:24]
	}
	return s
}

// An enum row is the only kind a flag can offer a value set for, so the two
// have to agree in both directions.
func TestLimitEnumsAccompanyEnumUnits(t *testing.T) {
	for _, l := range Limits() {
		if (l.Unit == UnitEnum) != (len(l.Enum) > 0) {
			t.Errorf("%s: unit %q with enum %v", l.Name, l.Unit, l.Enum)
		}
		if l.Unit != UnitEnum {
			continue
		}
		if !contains(l.Enum, l.Default) {
			t.Errorf("%s: default %q is not one of %v", l.Name, l.Default, l.Enum)
		}
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestLimitsRenderDefaults(t *testing.T) {
	want := map[string]string{
		"Root":            ".",
		"Watch":           "auto",
		"MaxDepth":        "32",
		"MaxNodes":        "200000",
		"MaxPreviewBytes": "1MiB",
		"MaxExtraBytes":   "64KiB",
		"SweepInterval":   "2s",
		"StaleAfter":      "1m30s",
		"Strict":          "false",
		"RedactKeys":      "api_key,apikey,authorization,cookie,credential,password,secret,token",
		"Color":           "auto",
		"ASCII":           "false",
	}
	got := map[string]string{}
	for _, l := range Limits() {
		got[l.Name] = l.Default
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s default renders as %q, want %q", name, got[name], w)
		}
	}
}

// A rendered list is read back through the one separator, so a default an
// operator copies into a flag returns the names it was rendered from.
func TestListDefaultsSurviveTheirEncoding(t *testing.T) {
	d := reflect.ValueOf(Defaults())
	for _, l := range Limits() {
		if l.Unit != UnitList {
			continue
		}
		want, ok := d.FieldByName(l.Name).Interface().([]string)
		if !ok {
			t.Errorf("%s: unit %q on a field that is not a string slice", l.Name, l.Unit)
			continue
		}
		if got := strings.Split(l.Default, ListSeparator); !slices.Equal(got, want) {
			t.Errorf("%s default %q splits to %v, want %v", l.Name, l.Default, got, want)
		}
	}
}

// A caller that edits a row it was handed cannot change what the next caller
// reads.
func TestLimitsRowsAreOwnedByTheCaller(t *testing.T) {
	a := Limits()
	for i := range a {
		a[i].Summary = clobbered
		if len(a[i].Enum) > 0 {
			a[i].Enum[0] = clobbered
		}
	}
	for _, l := range Limits() {
		if l.Summary == clobbered {
			t.Fatalf("%s: Limits() returns a shared row", l.Name)
		}
		if len(l.Enum) > 0 && l.Enum[0] == clobbered {
			t.Fatalf("%s: Limits() returns a shared enum", l.Name)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0B"},
		{1, "1B"},
		{1023, "1023B"},
		{1024, "1KiB"},
		{1536, "1536B"},
		{64 << 10, "64KiB"},
		{4 << 20, "4MiB"},
		{3 << 30, "3GiB"},
		{5 << 40, "5TiB"},
		{1 << 50, "1024TiB"},
		{-1, "-1B"},
	}
	for _, tt := range tests {
		if got := formatBytes(tt.in); got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A rendered ceiling is read back by an operator and typed into a flag, so the
// rendering has to preserve the number.
func FuzzFormatBytes(f *testing.F) {
	for _, n := range []int64{0, 1, 1023, 1024, 64 << 10, 4 << 20, 1 << 50, 1<<63 - 1} {
		f.Add(n)
	}
	f.Fuzz(func(t *testing.T, n int64) {
		if n < 0 {
			return
		}
		got := formatBytes(n)
		back, err := parseBytes(got)
		if err != nil {
			t.Fatalf("formatBytes(%d) = %q, which does not parse: %v", n, got, err)
		}
		if back != n {
			t.Fatalf("formatBytes(%d) = %q, which reads back as %d", n, got, back)
		}
	})
}

func parseBytes(s string) (int64, error) {
	for i := len(byteSuffixes) - 1; i >= 0; i-- {
		suffix := byteSuffixes[i]
		digits, ok := strings.CutSuffix(s, suffix)
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(digits, 10, 64)
		if err != nil {
			return 0, err
		}
		for range i {
			n *= 1024
		}
		return n, nil
	}
	return 0, strconv.ErrSyntax
}

// Rendering is total: a row naming a field that does not exist, or a value of
// the wrong shape, yields an empty string rather than a panic.
func TestRenderFieldIsTotal(t *testing.T) {
	v := reflect.ValueOf(Defaults())
	if got := renderField(v, &spec{name: "Absent", unit: UnitCount}); got != "" {
		t.Errorf("renderField for an absent field = %q, want empty", got)
	}
	unrenderable := struct{ Root map[string]int }{}
	if got := renderField(reflect.ValueOf(unrenderable), &spec{name: "Root", unit: UnitPath}); got != "" {
		t.Errorf("renderField for a map field = %q, want empty", got)
	}
	notStrings := struct{ RedactKeys []int }{RedactKeys: []int{1}}
	if got := renderField(reflect.ValueOf(notStrings), &spec{name: "RedactKeys", unit: UnitList}); got != "" {
		t.Errorf("renderField for a non-string list = %q, want empty", got)
	}
}

// kebab is the flag spelling of a Go field name: lower case, words separated by
// dashes, an initialism kept whole.
func kebab(name string) string {
	rs := []rune(name)
	var words []string
	start := 0
	for i := 1; i < len(rs); i++ {
		boundary := unicode.IsUpper(rs[i]) &&
			(!unicode.IsUpper(rs[i-1]) ||
				(i+1 < len(rs) && !unicode.IsUpper(rs[i+1])))
		if boundary {
			words = append(words, string(rs[start:i]))
			start = i
		}
	}
	words = append(words, string(rs[start:]))
	return strings.ToLower(strings.Join(words, "-"))
}

func TestKebab(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Root", "root"},
		{"MaxDepth", "max-depth"},
		{"MaxEntriesPerDir", "max-entries-per-dir"},
		{"DedupTTL", "dedup-ttl"},
		{"ASCII", "ascii"},
		{"RootRetryMin", "root-retry-min"},
	}
	for _, tt := range tests {
		if got := kebab(tt.in); got != tt.want {
			t.Errorf("kebab(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
