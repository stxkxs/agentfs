package cli

import (
	"flag"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/stxkxs/agentfs/internal/config"
)

// bind registers one flag per row of [config.Limits], writing into the matching
// field of cfg.
//
// The table is the registry, so a setting that is not in it cannot be set from
// the command line and cannot appear in the generated reference. A test asserts
// the table names every settable field, which closes the other direction.
func bind(fs *flag.FlagSet, cfg *config.Config) {
	v := reflect.ValueOf(cfg).Elem()
	for _, l := range config.Limits() {
		if l.Flag == "" || l.Flag == "root" {
			// The workspace is a positional argument. Offering it as a flag as
			// well would give one setting two spellings.
			continue
		}
		field := v.FieldByName(l.Name)
		if !field.IsValid() || !field.CanSet() {
			continue
		}
		bindField(fs, field, l)
	}
}

// bindField registers one flag against one field, choosing the registration
// from the unit the table declares rather than from the field's Go type: the
// unit is what the reference documents and what the environment parses.
func bindField(fs *flag.FlagSet, field reflect.Value, l config.Limit) {
	usage := firstSentence(describe(l.Summary, l.Flag))
	switch l.Unit {
	case config.UnitBool:
		if ptr, ok := field.Addr().Interface().(*bool); ok {
			fs.BoolVar(ptr, l.Flag, field.Bool(), usage)
		}
	case config.UnitDuration:
		if ptr, ok := field.Addr().Interface().(*time.Duration); ok {
			fs.DurationVar(ptr, l.Flag, time.Duration(field.Int()), usage)
		}
	case config.UnitList:
		fs.Var(listValue{field}, l.Flag, usage+" (comma separated)")
	case config.UnitBytes, config.UnitCount:
		bindNumber(fs, field, l, usage)
	default:
		fs.Var(stringValue{field}, l.Flag, usage)
	}
}

// bindNumber registers a counted or byte-valued field.
func bindNumber(fs *flag.FlagSet, field reflect.Value, l config.Limit, usage string) {
	switch field.Kind() {
	case reflect.Int:
		if ptr, ok := field.Addr().Interface().(*int); ok {
			fs.IntVar(ptr, l.Flag, int(field.Int()), usage)
		}
	case reflect.Int64:
		if ptr, ok := field.Addr().Interface().(*int64); ok {
			fs.Int64Var(ptr, l.Flag, field.Int(), usage)
		}
	default:
		fs.Var(stringValue{field}, l.Flag, usage)
	}
}

// stringValue sets a string-shaped field, including the watch mode.
type stringValue struct{ field reflect.Value }

func (s stringValue) String() string {
	if !s.field.IsValid() {
		return ""
	}
	if s.field.Kind() == reflect.String {
		return s.field.String()
	}
	if m, ok := s.field.Interface().(config.Mode); ok {
		return m.String()
	}
	return ""
}

func (s stringValue) Set(v string) error {
	if s.field.Kind() == reflect.String {
		s.field.SetString(v)
		return nil
	}
	if _, ok := s.field.Interface().(config.Mode); ok {
		m, err := config.ParseMode(v)
		if err != nil {
			return err
		}
		s.field.Set(reflect.ValueOf(m))
		return nil
	}
	return fmt.Errorf("cannot set %s from a string", s.field.Type())
}

// listValue sets a comma-separated string slice.
type listValue struct{ field reflect.Value }

func (l listValue) String() string {
	if !l.field.IsValid() || l.field.Kind() != reflect.Slice {
		return ""
	}
	parts := make([]string, l.field.Len())
	for i := range parts {
		parts[i] = l.field.Index(i).String()
	}
	return strings.Join(parts, ",")
}

func (l listValue) Set(v string) error {
	if v == "" {
		l.field.Set(reflect.ValueOf([]string(nil)))
		return nil
	}
	parts := strings.Split(v, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	l.field.Set(reflect.ValueOf(parts))
	return nil
}

// applyEnv fills cfg from the environment. A variable that does not parse is
// ignored rather than fatal: an exported value is ambient, and refusing to
// start because of one would make an unrelated shell setting break the tool.
func applyEnv(env Env, cfg *config.Config) {
	if env.Getenv == nil {
		return
	}
	v := reflect.ValueOf(cfg).Elem()
	for _, l := range config.Limits() {
		if l.Env == "" {
			continue
		}
		raw := strings.TrimSpace(env.getenv(l.Env))
		if raw == "" {
			continue
		}
		field := v.FieldByName(l.Name)
		if !field.IsValid() || !field.CanSet() {
			continue
		}
		setFromString(field, l.Unit, raw)
	}
}

// setFromString applies one environment value, dispatching on the unit the
// table declares rather than on the field's Go kind. A mode is an int in Go and
// a word on the wire, so a kind-first dispatch would try to parse "sweep" as a
// number and silently apply nothing.
//
// A value that does not parse is left unapplied: an exported variable is
// ambient rather than asked for, and refusing to start because of an unrelated
// shell setting would be worse than proceeding with the default.
func setFromString(field reflect.Value, unit, raw string) {
	switch unit {
	case config.UnitBool:
		if b, err := strconv.ParseBool(raw); err == nil {
			field.SetBool(b)
		}
	case config.UnitDuration:
		if d, err := time.ParseDuration(raw); err == nil {
			field.SetInt(int64(d))
		}
	case config.UnitList:
		_ = listValue{field}.Set(raw)
	case config.UnitEnum, config.UnitPath:
		_ = stringValue{field}.Set(raw)
	case config.UnitBytes, config.UnitCount:
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			field.SetInt(n)
		}
	default:
		_ = stringValue{field}.Set(raw)
	}
}
