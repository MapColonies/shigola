package config

import (
	"encoding"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/MapColonies/shigola/internal/env"
)

// withTypeHint adds what the offending key takes to a TOML parse error, when
// that can be said for certain (MAPCO-11617).
//
// A value the lexer cannot read at all, like `enabled = xx`, is rejected before
// any type reaches our code, so the parser can only say it "expected value".
// It does record the key, and the key names a field of Config, whose type says
// what the value should have been. Reading it off the struct rather than from a
// hand-kept table means the hint cannot go stale as keys are added.
//
// Any other error is returned unchanged, and so is a parse error whose key does
// not resolve to a field typeHint can describe. The result wraps err, so
// errors.As still finds the toml.ParseError.
func withTypeHint(err error) error {
	var pe toml.ParseError
	if !errors.As(err, &pe) {
		return err
	}

	hint, ok := typeHint(reflect.TypeOf(Config{}), pe.LastKey)
	if !ok {
		return err
	}

	return fmt.Errorf("%w (%s takes %s)", err, pe.LastKey, hint)
}

// typeHint describes the TOML value the dotted key takes in t, walking t through
// its toml tags. An array of tables is walked through its element, so
// "maps.layers.min_zoom" resolves inside []provider.Map and []MapLayer.
func typeHint(t reflect.Type, key string) (string, bool) {
	if key == "" {
		return "", false
	}

	for _, name := range strings.Split(key, ".") {
		t = tableType(t)
		if t.Kind() != reflect.Struct {
			// An env.Dict, say: a table whose keys are not fields.
			return "", false
		}

		f, ok := fieldByKey(t, name)
		if !ok {
			return "", false
		}
		t = f.Type
	}

	return describe(t)
}

// deref strips the pointers from t.
func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// tableType strips the pointers and arrays of tables between a key and the
// struct its sub-keys are fields of.
func tableType(t reflect.Type) reflect.Type {
	for {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			t = t.Elem()
		default:
			return t
		}
	}
}

// fieldByKey finds the field the decoder would fill for name: the toml tag, or
// failing that the field name compared case-insensitively, as BurntSushi/toml
// does.
func fieldByKey(t reflect.Type, name string) (reflect.StructField, bool) {
	var byName *reflect.StructField

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}

		tag, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
		switch {
		case tag == "-":
			continue
		case tag == name:
			return f, true
		case tag == "" && byName == nil && strings.EqualFold(f.Name, name):
			byName = &f
		}
	}

	if byName == nil {
		return reflect.StructField{}, false
	}
	return *byName, true
}

// scalarHint describes a scalar kind as the value a key takes (one) and as what
// an array of it holds (many).
type scalarHint struct {
	one, many string
}

var scalarHints = map[reflect.Kind]scalarHint{
	reflect.Bool:    {"a boolean, true or false", "booleans"},
	reflect.String:  {"a string", "strings"},
	reflect.Int:     {"an integer", "integers"},
	reflect.Int8:    {"an integer", "integers"},
	reflect.Int16:   {"an integer", "integers"},
	reflect.Int32:   {"an integer", "integers"},
	reflect.Int64:   {"an integer", "integers"},
	reflect.Uint:    {"a non-negative integer", "non-negative integers"},
	reflect.Uint8:   {"a non-negative integer", "non-negative integers"},
	reflect.Uint16:  {"a non-negative integer", "non-negative integers"},
	reflect.Uint32:  {"a non-negative integer", "non-negative integers"},
	reflect.Uint64:  {"a non-negative integer", "non-negative integers"},
	reflect.Float32: {"a floating-point number, e.g. 1.0", "floating-point numbers"},
	reflect.Float64: {"a floating-point number, e.g. 1.0", "floating-point numbers"},
}

// describe says what TOML value a field of type t takes, from t's kind.
//
// Only the kinds that are unambiguous are described, and anything else is left
// silent, so a hint is either right or absent. A struct is not described as a
// table because env.URL is a struct written as a string; a map is not because
// env.Dict is a table of anything; an array of tables has nothing useful to say
// about a key that named the array itself.
func describe(t reflect.Type) (string, bool) {
	t = deref(t)

	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		elem := deref(t.Elem())
		if !kindIsTruthful(elem) {
			return "", false
		}
		hint, ok := scalarHints[elem.Kind()]
		if !ok {
			return "", false
		}
		return "an array of " + hint.many, true
	}

	if !kindIsTruthful(t) {
		return "", false
	}
	hint, ok := scalarHints[t.Kind()]
	return hint.one, ok
}

var (
	envPkgPath      = reflect.TypeOf(env.Bool(false)).PkgPath()
	tomlUnmarshaler = reflect.TypeOf((*toml.Unmarshaler)(nil)).Elem()
	textUnmarshaler = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

// kindIsTruthful reports whether t's kind is the kind of TOML value it takes.
//
// A type that decodes itself can take anything — a time.Duration-like type of
// kind int64 that unmarshals "5s" would be described as an integer. env's
// scalar types are the exception this package relies on: each accepts its own
// kind, or a string holding a ${VAR}, so the kind is still the right thing to
// tell an operator.
func kindIsTruthful(t reflect.Type) bool {
	ptr := reflect.PointerTo(t)
	if !ptr.Implements(tomlUnmarshaler) && !ptr.Implements(textUnmarshaler) {
		return true
	}
	return t.PkgPath() == envPkgPath
}
