package cli

import (
	"flag"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/config"
)

// fixtureRoot stands in for the workspace argument. Resolving a command line
// reads no filesystem, so the path only has to be a path.
const fixtureRoot = "/workspace"

// resolved returns the configuration one command line runs under.
func resolved(t *testing.T, env Env, command string, args ...string) config.Config {
	t.Helper()
	opts, err := resolve(env, mustLookup(command), args)
	if err != nil {
		t.Fatalf("resolve %s %v: %v", command, args, err)
	}
	return opts.Config
}

// exported returns an environment that reads one variable.
func exported(key, value string) Env {
	return Env{Getenv: func(k string) string {
		if k == key {
			return value
		}
		return ""
	}}
}

// A flag is registered from the unit the table declares rather than from the
// field's Go type, so every unit reaches its field in the shape the reference
// documents rather than only the ones whose Go type happens to match.
func TestAFlagSetsEveryUnit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want any
		of   func(config.Config) any
	}{
		{"bool", []string{"-strict"}, true, func(c config.Config) any { return c.Strict }},
		{"bool given a value", []string{"-ascii=true"}, true, func(c config.Config) any { return c.ASCII }},
		{"duration", []string{"-sweep-interval", "7s"}, 7 * time.Second, func(c config.Config) any { return c.SweepInterval }},
		{"list", []string{"-redact-keys", "token,pin"}, []string{"token", "pin"}, func(c config.Config) any { return c.RedactKeys }},
		{"enum", []string{"-watch", "sweep"}, config.ModeSweep, func(c config.Config) any { return c.Watch }},
		{"enum on a string field", []string{"-color", "never"}, config.ColorNever, func(c config.Config) any { return c.Color }},
		{"bytes", []string{"-max-preview-bytes", "8388608"}, int64(8 << 20), func(c config.Config) any { return c.MaxPreviewBytes }},
		{"count", []string{"-max-nodes", "17"}, 17, func(c config.Config) any { return c.MaxNodes }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.of(resolved(t, Env{}, "scan", append(tt.args, fixtureRoot)...))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%v resolved to %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

// Every flag has an environment variable, parsed from the same unit, so an
// operator who knows one setting's spelling knows the other's.
func TestAnEnvironmentVariableSetsEveryUnit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		key     string
		value   string
		command string
		args    []string
		want    any
		of      func(config.Config) any
	}{
		{"bool", "AGENTFS_STRICT", "true", "scan", []string{fixtureRoot}, true,
			func(c config.Config) any { return c.Strict }},
		{"duration", "AGENTFS_SWEEP_INTERVAL", "7s", "scan", []string{fixtureRoot}, 7 * time.Second,
			func(c config.Config) any { return c.SweepInterval }},
		{"list", "AGENTFS_REDACT_KEYS", "token,pin", "scan", []string{fixtureRoot}, []string{"token", "pin"},
			func(c config.Config) any { return c.RedactKeys }},
		{"enum", "AGENTFS_WATCH", "sweep", "scan", []string{fixtureRoot}, config.ModeSweep,
			func(c config.Config) any { return c.Watch }},
		{"enum on a string field", "AGENTFS_COLOR", "always", "scan", []string{fixtureRoot}, config.ColorAlways,
			func(c config.Config) any { return c.Color }},
		// A command that takes no workspace argument is where a path from the
		// environment survives, the positional one being the more specific
		// statement of intent everywhere else.
		{"path", "AGENTFS_ROOT", "/srv/agents", "schema", nil, "/srv/agents",
			func(c config.Config) any { return c.Root }},
		{"bytes", "AGENTFS_MAX_PREVIEW_BYTES", "8388608", "scan", []string{fixtureRoot}, int64(8 << 20),
			func(c config.Config) any { return c.MaxPreviewBytes }},
		{"count", "AGENTFS_MAX_NODES", "17", "scan", []string{fixtureRoot}, 17,
			func(c config.Config) any { return c.MaxNodes }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.of(resolved(t, exported(tt.key, tt.value), tt.command, tt.args...))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s=%q resolved to %v, want %v", tt.key, tt.value, got, tt.want)
			}
		})
	}
}

// An exported value is ambient rather than asked for, so one that does not
// parse leaves the whole configuration as it was instead of stopping the run or
// applying something else.
func TestAnUnparseableEnvironmentValueLeavesEverySettingAlone(t *testing.T) {
	t.Parallel()
	want := config.Defaults()
	want.Root = fixtureRoot

	for _, tt := range []struct{ name, key, value string }{
		{"bool", "AGENTFS_STRICT", "yes please"},
		{"duration", "AGENTFS_SWEEP_INTERVAL", "a while"},
		{"enum", "AGENTFS_WATCH", "telepathy"},
		{"count", "AGENTFS_MAX_NODES", "lots"},
		{"bytes", "AGENTFS_MAX_PREVIEW_BYTES", "loads"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := resolved(t, exported(tt.key, tt.value), "scan", fixtureRoot); !reflect.DeepEqual(got, want) {
				t.Errorf("%s=%q resolved to %v, want the defaults", tt.key, tt.value, got)
			}
		})
	}
}

// The entries of a list are names, and the spacing an operator writes around a
// separator is not part of one. An empty value is a deliberate opt out rather
// than a list holding one blank name.
func TestAListValueReadsNamesRatherThanSpacing(t *testing.T) {
	t.Parallel()

	spaced := resolved(t, Env{}, "scan", "-redact-keys", " token , pin ", fixtureRoot).RedactKeys
	if want := []string{"token", "pin"}; !reflect.DeepEqual(spaced, want) {
		t.Errorf("redact-keys = %q, want %q", spaced, want)
	}
	if empty := resolved(t, Env{}, "scan", "-redact-keys", "", fixtureRoot).RedactKeys; empty != nil {
		t.Errorf("an empty list resolved to %q, want none", empty)
	}
}

// A list renders in the form the flag reads, so the default shown in the help
// text is a value that can be given back.
func TestAListValueRoundTripsThroughItsRenderedForm(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	v := listValue{reflect.ValueOf(&cfg).Elem().FieldByName("RedactKeys")}

	rendered := v.String()
	cfg.RedactKeys = nil
	if err := v.Set(rendered); err != nil {
		t.Fatalf("Set(%q): %v", rendered, err)
	}
	if got := v.String(); got != rendered {
		t.Errorf("round trip = %q, want %q", got, rendered)
	}
	if !reflect.DeepEqual(cfg.RedactKeys, config.Defaults().RedactKeys) {
		t.Errorf("round trip produced %q, want %q", cfg.RedactKeys, config.Defaults().RedactKeys)
	}
}

// A value bound to a field it cannot represent says so rather than writing
// something else, which is what keeps the unit vocabulary closed: a row
// carrying a unit the field cannot hold fails where it is registered.
func TestAValueBoundToAFieldItCannotHoldRefusesIt(t *testing.T) {
	t.Parallel()

	var n int
	number := stringValue{reflect.ValueOf(&n).Elem()}
	if err := number.Set("7"); err == nil {
		t.Error("setting a number field from a string succeeded")
	}
	if n != 0 {
		t.Errorf("the refused value was written anyway: %d", n)
	}
	for name, got := range map[string]string{
		"a field of the wrong type": number.String(),
		"an unbound string":         stringValue{}.String(),
		"an unbound list":           listValue{}.String(),
	} {
		if got != "" {
			t.Errorf("%s renders as %q, want empty", name, got)
		}
	}
}

// An environment reader is optional, and a command given none consults no
// variable rather than reaching through nothing.
func TestAnAbsentEnvironmentReaderReportsNothing(t *testing.T) {
	t.Parallel()
	if got := (Env{}).getenv(config.EnvPrefix + "WATCH"); got != "" {
		t.Errorf("getenv = %q, want empty", got)
	}
}

// A registered flag carries the description the flag list shows, not the raw
// table sentence. The table opens each summary with the Go field name because
// it documents a struct; a Go identifier names nothing a reader can type, so
// the string reaching [flag.FlagSet] is the one [describe] has turned into flag
// help. Registration and the rendered list are written apart from each other
// and drift silently, which this closes from the registration side.
func TestARegisteredFlagCarriesItsRenderedDescription(t *testing.T) {
	t.Parallel()
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	bind(fs, &config.Config{})

	byFlag := make(map[string]config.Limit)
	for _, l := range config.Limits() {
		byFlag[l.Flag] = l
	}
	fs.VisitAll(func(f *flag.Flag) {
		l, ok := byFlag[f.Name]
		if !ok {
			t.Errorf("-%s is registered but the table does not name it", f.Name)
			return
		}
		want := firstSentence(describe(l.Summary, l.Flag))
		if got := strings.TrimSuffix(f.Usage, " (comma separated)"); got != want {
			t.Errorf("-%s usage = %q, want %q", f.Name, got, want)
		}
		if first, _, _ := strings.Cut(f.Usage, " "); strings.EqualFold(undash(first), undash(l.Name)) {
			t.Errorf("-%s usage opens with the Go field name %q", f.Name, l.Name)
		}
	})
}
