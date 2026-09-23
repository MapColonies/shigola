package env

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// evnVarRegex matches a variable surrounded by curly braces with leading dollar sign.
// ex: ${MY_VAR}
var envVarRegex = regexp.MustCompile(`\${[A-Z]+[A-Z1-9_]*}`)

// ErrEnvVar corresponds with a missing environment variable
type ErrEnvVar string

func (e ErrEnvVar) Error() string {
	return fmt.Sprintf("environment variable %q not found", string(e))
}

// ErrType corresponds with a value of the wrong type, or a string that does not
// parse as the type, passed to UnmarshalTOML.
//
// It names the value and what the value should have been, and nothing else:
// UnmarshalTOML is never told the key it is decoding, so the key is not ours to
// report. The TOML decoder wraps this error with the key and line it came from
// (MAPCO-11617).
type ErrType struct {
	v any
	// want is what v should have been, as a noun phrase: "a boolean".
	want string
}

func (te ErrType) Error() string {
	// A string is shown quoted and without its type, because in a config the
	// quotes already say it was a string.
	if s, ok := te.v.(string); ok {
		return fmt.Sprintf("%q is not %s", s, te.want)
	}
	return fmt.Sprintf("%v (%T) is not %s", te.v, te.v, te.want)
}

// What each parser wants, as ErrType reports it.
const (
	wantString = "a string"
	wantBool   = "a boolean (true or false)"
	wantInt    = "an integer"
	wantUint   = "a non-negative integer"
	// TOML reads 1 as an integer, and ParseFloat takes only a float, so the
	// message has to show what a float looks like.
	wantFloat = "a floating-point number (e.g. 1.0)"
	wantDict  = "a table"
	wantURL   = "a URL string"
)

// replaceEnvVars replaces environment variable placeholders in reader stream with values
func replaceEnvVar(in string) (string, error) {
	// loop through all environment variable matches
	for locs := envVarRegex.FindStringIndex(in); locs != nil; locs = envVarRegex.FindStringIndex(in) {

		// extract match from the input string
		match := in[locs[0]:locs[1]]

		// trim the leading '${' and trailing '}'
		varName := match[2 : len(match)-1]

		// get env var
		envVar, ok := os.LookupEnv(varName)
		if !ok {
			return "", ErrEnvVar(varName)
		}

		// update the input string with the env values
		in = strings.Replace(in, match, envVar, -1)
	}

	return in, nil
}

//TODO(@ear7h): implement UnmarshalJSON for types

func (t *Dict) UnmarshalTOML(v any) error {
	var d *Dict
	var err error

	d, err = ParseDict(v)
	if err != nil {
		return err
	}

	*t = *d

	return nil
}

type Bool bool

func BoolPtr(v Bool) *Bool {
	return &v
}

func (t *Bool) UnmarshalTOML(v any) error {
	var b *bool
	var err error

	b, err = ParseBool(v)
	if err != nil {
		return err
	}

	*t = Bool(*b)
	return nil
}

type String string

func StringPtr(v String) *String {
	return &v
}

func (t *String) UnmarshalTOML(v any) error {
	var s *string
	var err error

	s, err = ParseString(v)
	if err != nil {
		return err
	}

	*t = String(*s)
	return nil
}

type Int int

func IntPtr(v Int) *Int {
	return &v
}

func (t *Int) UnmarshalTOML(v any) error {
	var i *int
	var err error

	i, err = ParseInt(v)
	if err != nil {
		return err
	}

	*t = Int(*i)
	return nil
}

type Uint uint

func UintPtr(v Uint) *Uint {
	return &v
}

func (t *Uint) UnmarshalTOML(v any) error {
	var ui *uint
	var err error

	ui, err = ParseUint(v)
	if err != nil {
		return err
	}

	*t = Uint(*ui)
	return nil
}

type Float float64

func FloatPtr(v Float) *Float {
	return &v
}

func (t *Float) UnmarshalTOML(v any) error {
	var f *float64
	var err error

	f, err = ParseFloat(v)
	if err != nil {
		return err
	}

	*t = Float(*f)
	return nil
}

type URL url.URL

func (t *URL) UnmarshalTOML(v any) error {
	u, err := ParseURL(v)
	if err != nil {
		return err
	}

	if u == nil {
		return nil
	}

	*t = URL(*u)
	return nil
}
