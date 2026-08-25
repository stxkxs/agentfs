package config

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDefaultsValidate(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("Defaults() does not validate: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		fields []string
	}{
		{
			name:   "a ceiling of zero",
			mutate: func(c *Config) { c.MaxDepth = 0 },
			fields: []string{"MaxDepth"},
		},
		{
			name:   "a negative ceiling",
			mutate: func(c *Config) { c.MaxNodes = -1 },
			fields: []string{"MaxNodes"},
		},
		{
			name:   "a byte ceiling of zero",
			mutate: func(c *Config) { c.MaxPreviewBytes = 0 },
			fields: []string{"MaxPreviewBytes"},
		},
		{
			name:   "preserved members larger than the document holding them",
			mutate: func(c *Config) { c.MaxExtraBytes = c.MaxDocumentBytes + 1 },
			fields: []string{"MaxExtraBytes"},
		},
		{
			name:   "a sweep interval below the floor",
			mutate: func(c *Config) { c.SweepInterval = MinSweepInterval - time.Nanosecond },
			fields: []string{"SweepInterval"},
		},
		{
			name:   "a backoff that starts above its ceiling",
			mutate: func(c *Config) { c.RootRetryMin = c.RootRetryMax + 1 },
			fields: []string{"RootRetryMin"},
		},
		{
			name:   "an unknown color",
			mutate: func(c *Config) { c.Color = "sometimes" },
			fields: []string{"Color"},
		},
		{
			name:   "an empty color",
			mutate: func(c *Config) { c.Color = "" },
			fields: []string{"Color"},
		},
		{
			name:   "a watch mode outside the defined set",
			mutate: func(c *Config) { c.Watch = Mode(9) },
			fields: []string{"Watch"},
		},
		{
			name:   "an empty root",
			mutate: func(c *Config) { c.Root = "" },
			fields: []string{"Root"},
		},
		{
			name:   "a root of spacing",
			mutate: func(c *Config) { c.Root = "  " },
			fields: []string{"Root"},
		},
		{
			name:   "a redaction list with an empty name",
			mutate: func(c *Config) { c.RedactKeys = []string{"token", " "} },
			fields: []string{"RedactKeys"},
		},
		{
			name:   "a redaction name carrying the list separator",
			mutate: func(c *Config) { c.RedactKeys = []string{"token,secret"} },
			fields: []string{"RedactKeys"},
		},
		{
			name:   "a negative window",
			mutate: func(c *Config) { c.DedupTTL = -time.Second },
			fields: []string{"DedupTTL"},
		},
		{
			name: "every invalid field in one call",
			mutate: func(c *Config) {
				c.Root = ""
				c.MaxQueue = 0
				c.SweepInterval = 0
				c.Color = "beige"
			},
			fields: []string{"Root", "MaxQueue", "SweepInterval", "Color"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Defaults()
			tt.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate() accepted %v", c)
			}
			got := fieldErrors(err)
			for _, want := range tt.fields {
				if !reportsField(got, want) {
					t.Errorf("Validate() reported %v, want a finding for %s", got, want)
				}
			}
			if len(got) < len(tt.fields) {
				t.Errorf("Validate() reported %d findings, want at least %d", len(got), len(tt.fields))
			}
		})
	}
}

// The floor is the number the reference states, not whatever MinSweepInterval
// happens to hold: a sweep running more often than this spends more of the
// machine re-reading directories than the agents spend writing them.
func TestSweepIntervalFloor(t *testing.T) {
	if want := 100 * time.Millisecond; MinSweepInterval != want {
		t.Errorf("MinSweepInterval = %v, want %v", MinSweepInterval, want)
	}
	tests := []struct {
		in     time.Duration
		reject bool
	}{
		{time.Nanosecond, true},
		{99 * time.Millisecond, true},
		{100 * time.Millisecond, false},
		{time.Second, false},
	}
	for _, tt := range tests {
		c := Defaults()
		c.SweepInterval = tt.in
		if got := reportsField(fieldErrors(c.Validate()), "SweepInterval"); got != tt.reject {
			t.Errorf("SweepInterval=%v reported = %v, want %v", tt.in, got, tt.reject)
		}
	}
}

// zeroWindows are the settings a zero switches off. Every other numeric row is
// a ceiling, and a ceiling of zero bounds the program to doing nothing.
var zeroWindows = map[string]bool{"DedupTTL": true, "SkewTolerance": true}

// A floor that is dropped from a row is a ceiling that accepts zero, which the
// per-row tests below cannot see because they read the floor they are checking.
// This one holds every row to the rule instead.
func TestEveryCeilingRejectsNonPositive(t *testing.T) {
	for i := range limitSpecs {
		s := &limitSpecs[i]
		if !numeric(s.unit) {
			continue
		}
		t.Run(s.name, func(t *testing.T) {
			switch {
			case zeroWindows[s.name]:
				if s.min != 0 {
					t.Errorf("floor is %d, want 0: a window switches off at zero", s.min)
				}
			case s.unit == UnitCount || s.unit == UnitBytes:
				if s.min != 1 {
					t.Errorf("floor is %d, want 1: a ceiling is a positive quantity", s.min)
				}
			case s.min < 1:
				t.Errorf("floor is %d, want a positive duration", s.min)
			}
			for _, n := range []int64{0, -1} {
				c := Defaults()
				setInt(&c, s.name, n)
				want := n < 0 || !zeroWindows[s.name]
				if got := reportsField(fieldErrors(c.Validate()), s.name); got != want {
					t.Errorf("%s=%d reported = %v, want %v", s.name, n, got, want)
				}
			}
		})
	}
}

// Every spelling the reference offers has to be one the program starts under,
// so the vocabulary is a set of accepted values rather than a set of strings.
func TestValidateAcceptsEveryEnumSpelling(t *testing.T) {
	if want := []string{"auto", "always", "never"}; !slices.Equal(colorValues, want) {
		t.Fatalf("colorValues = %v, want %v", colorValues, want)
	}
	for _, v := range colorValues {
		c := Defaults()
		c.Color = v
		if err := c.Validate(); err != nil {
			t.Errorf("Validate() refused color %q: %v", v, err)
		}
	}
	for _, name := range modeNames {
		m, err := ParseMode(name)
		if err != nil {
			t.Fatalf("ParseMode(%q): %v", name, err)
		}
		c := Defaults()
		c.Watch = m
		if err := c.Validate(); err != nil {
			t.Errorf("Validate() refused watch %q: %v", name, err)
		}
	}
}

func TestValidateAcceptsAnEmptyRedactionList(t *testing.T) {
	c := Defaults()
	c.RedactKeys = nil
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() refused an empty redaction list: %v", err)
	}
}

// Every ceiling holds at the floor its row declares, so the table's floors and
// the checks are the same statement.
func TestValidateAcceptsEveryFloor(t *testing.T) {
	for i := range limitSpecs {
		s := &limitSpecs[i]
		if !numeric(s.unit) {
			continue
		}
		t.Run(s.name, func(t *testing.T) {
			c := Defaults()
			setInt(&c, s.name, s.min)
			for _, fe := range fieldErrors(c.Validate()) {
				if fe.Field == s.name {
					t.Errorf("Validate() refused %s at its floor: %v", s.name, fe)
				}
			}
		})
	}
}

func TestValidateRejectsEveryCeilingBelowItsFloor(t *testing.T) {
	for i := range limitSpecs {
		s := &limitSpecs[i]
		if !numeric(s.unit) {
			continue
		}
		t.Run(s.name, func(t *testing.T) {
			c := Defaults()
			setInt(&c, s.name, s.min-1)
			got := fieldErrors(c.Validate())
			if !reportsField(got, s.name) {
				t.Fatalf("Validate() accepted %s below its floor; findings: %v", s.name, got)
			}
			for _, fe := range got {
				if fe.Field != s.name {
					continue
				}
				if !strings.Contains(fe.Error(), s.flag) {
					t.Errorf("finding %q does not name the flag %q", fe, s.flag)
				}
			}
		})
	}
}

func TestFieldErrorIsRecoverable(t *testing.T) {
	c := Defaults()
	c.MaxBatch = 0
	err := c.Validate()
	var fe FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("Validate() error %v is not a FieldError", err)
	}
	if fe.Field != "MaxBatch" || fe.Flag != "max-batch" {
		t.Errorf("FieldError = %+v, want MaxBatch/max-batch", fe)
	}
	if !strings.Contains(err.Error(), "MaxBatch (--max-batch)") {
		t.Errorf("error reads %q, want it to name the field and the flag", err)
	}
}

func TestCheckFieldIsTotal(t *testing.T) {
	v := reflect.ValueOf(Defaults())
	if err := checkField(v, &spec{name: "Absent", flag: "absent", unit: UnitCount, min: 1}); err != nil {
		t.Errorf("checkField for an absent field = %v, want nil", err)
	}
	if err := checkField(v, &spec{name: "Strict", flag: "strict", unit: UnitBool}); err != nil {
		t.Errorf("checkField for a bool = %v, want nil", err)
	}
	notStrings := struct{ RedactKeys []int }{RedactKeys: []int{1}}
	if err := checkField(reflect.ValueOf(notStrings), &spec{name: "RedactKeys", flag: "redact-keys", unit: UnitList}); err != nil {
		t.Errorf("checkField for a non-string list = %v, want nil", err)
	}
}

func numeric(unit string) bool {
	return unit == UnitCount || unit == UnitBytes || unit == UnitDuration
}

func setInt(c *Config, name string, n int64) {
	reflect.ValueOf(c).Elem().FieldByName(name).SetInt(n)
}

func reportsField(errs []FieldError, name string) bool {
	for _, fe := range errs {
		if fe.Field == name {
			return true
		}
	}
	return false
}

// fieldErrors collects every finding a joined error carries, rather than the
// first one errors.As would stop at.
func fieldErrors(err error) []FieldError {
	var out []FieldError
	var walk func(error)
	walk = func(e error) {
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			for _, x := range joined.Unwrap() {
				walk(x)
			}
			return
		}
		var fe FieldError
		if errors.As(e, &fe) {
			out = append(out, fe)
		}
	}
	walk(err)
	return out
}
