package config

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
)

// MinSweepInterval is the shortest permitted [Config.SweepInterval]. Below it a
// sweep pass is still running when the next one is due, and agentfs spends more
// of the machine re-reading directories than the agents it watches spend
// writing them.
const MinSweepInterval = 100 * time.Millisecond

// FieldError reports one setting that [Config.Validate] refused. It names the
// Go field and the flag, so the same finding is actionable to a caller holding
// a [Config] and to an operator holding a command line.
type FieldError struct {
	// Field is the [Config] field name.
	Field string
	// Flag is the command-line flag that sets the field, without dashes.
	Flag string
	// Reason states what the value must satisfy and what it was.
	Reason string
}

// Error implements the error interface.
func (e FieldError) Error() string {
	return e.Field + " (--" + e.Flag + "): " + e.Reason
}

// Validate reports every setting the program cannot run under, joined with
// [errors.Join] into one error whose findings are each a [FieldError]. All of
// them are reported from one call: an operator correcting a configuration file
// fixes the whole file rather than discovering the next mistake on the next
// start.
//
// A count, byte or duration ceiling must be at least the floor its table row
// carries, which is one for a quantity and zero for a window that switches off
// when empty. Beyond the per-field floors, two relations between fields have to
// hold: preserved undefined members cannot outsize the document holding them,
// and a backoff cannot start above its own ceiling.
func (c Config) Validate() error {
	v := reflect.ValueOf(c)
	errs := make([]error, 0, len(limitSpecs))
	for i := range limitSpecs {
		s := &limitSpecs[i]
		if err := checkField(v, s); err != nil {
			errs = append(errs, err)
		}
	}
	errs = append(errs, relations(&c)...)
	return errors.Join(errs...)
}

// checkField holds one field to the shape and floor its row declares.
func checkField(v reflect.Value, s *spec) error {
	f := v.FieldByName(s.name)
	if !f.IsValid() {
		return nil
	}
	switch s.unit {
	case UnitCount, UnitBytes, UnitDuration:
		if f.CanInt() && f.Int() < s.min {
			return FieldError{s.name, s.flag, fmt.Sprintf("must be at least %s, got %s",
				renderNumber(s.unit, s.min), renderField(v, s))}
		}
	case UnitEnum:
		if got := renderField(v, s); !slices.Contains(s.enum, got) {
			return FieldError{s.name, s.flag, fmt.Sprintf("must be one of %s, got %q",
				strings.Join(s.enum, ", "), got)}
		}
	case UnitPath:
		if strings.TrimSpace(f.String()) == "" {
			return FieldError{s.name, s.flag, "must name a directory"}
		}
	case UnitList:
		// An empty list is a deliberate opt out. An entry within one has to be
		// a name the encoding reads back: a blank matches every member or
		// none, and one carrying [ListSeparator] returns as two names.
		if xs, ok := f.Interface().([]string); ok {
			if i := slices.IndexFunc(xs, unnameable); i >= 0 {
				return FieldError{s.name, s.flag, fmt.Sprintf(
					"entry %d is %q: a name is not blank and does not contain %q",
					i, xs[i], ListSeparator)}
			}
		}
	case UnitBool:
		// A boolean has no value outside its range.
	}
	return nil
}

// unnameable reports whether s cannot serve as an entry of a [UnitList] value.
func unnameable(s string) bool {
	return strings.TrimSpace(s) == "" || strings.Contains(s, ListSeparator)
}

// relations reports the settings that are individually permitted but cannot
// hold together.
func relations(c *Config) []error {
	var errs []error
	if c.MaxExtraBytes > c.MaxDocumentBytes {
		errs = append(errs, FieldError{"MaxExtraBytes", "max-extra-bytes",
			fmt.Sprintf("must not exceed max-document-bytes %s, got %s",
				formatBytes(c.MaxDocumentBytes), formatBytes(c.MaxExtraBytes))})
	}
	if c.RootRetryMin > c.RootRetryMax {
		errs = append(errs, FieldError{"RootRetryMin", "root-retry-min",
			fmt.Sprintf("must not exceed root-retry-max %s, got %s",
				c.RootRetryMax, c.RootRetryMin)})
	}
	return errs
}
