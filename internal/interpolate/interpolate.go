// Package interpolate resolves ${ENV_VAR} references in already-parsed
// configuration values.
//
// Resolution deliberately runs on parsed strings, never on raw file bytes.
// Substituting into bytes before the YAML parse is a document-injection hazard:
// a password containing a newline, ": ", or a leading &/*/! would alter the
// structure of the document rather than the value of one field. goccy/go-yaml
// additionally discards tabs in plain scalars, so a tab-bearing secret would be
// silently corrupted. Resolving after the parse means a value never passes
// through the parser at all.
//
// Errors name the variable and the field path — never the resolved value, which
// would leak a secret into kcp.log.
package interpolate

import (
	"fmt"
	"os"
	"reflect"
	"strings"
)

// String resolves ${VAR} references in s against the process environment.
//
// Syntax is deliberately narrow: only ${VAR} is a reference, so a bare $VAR and
// any other unescaped $ survive verbatim (p@$$w0rd must round-trip). "$${" is
// the escape for a literal "${", and an unterminated "${" is literal too, since
// a password may legitimately end in one. An unset variable is a hard error —
// an empty password silently attempting auth is the worst failure mode
// available. Resolved values are not re-scanned, so a secret that itself
// contains "${...}" cannot trigger a second expansion.
func String(s string) (string, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}

	var b strings.Builder
	for i := 0; i < len(s); {
		// "$${" — escape for a literal "${".
		if s[i] == '$' && i+2 < len(s) && s[i+1] == '$' && s[i+2] == '{' {
			b.WriteString("${")
			i += 3
			continue
		}
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '{' {
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				// Unterminated — the remainder is literal.
				b.WriteString(s[i:])
				break
			}
			name := s[i+2 : i+2+end]
			if name == "" {
				return "", fmt.Errorf("empty variable name in ${}")
			}
			val, ok := os.LookupEnv(name)
			if !ok {
				return "", fmt.Errorf("environment variable %q is not set", name)
			}
			b.WriteString(val)
			i += 2 + end + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String(), nil
}

// Struct resolves ${VAR} references in every settable string reachable from v,
// which must be a non-nil pointer. It walks nested structs, pointers, slices,
// arrays and maps; unexported fields and non-string kinds are left alone.
//
// A reflective walk rather than a hand-written per-struct pass: a field added
// later without a matching line would otherwise silently stop being resolved,
// which for a credential field means shipping a literal "${MSK_PASSWORD}" to a
// broker as the password.
func Struct(v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("interpolate: target must be a non-nil pointer, got %T", v)
	}
	return walk(rv.Elem(), "")
}

func walk(v reflect.Value, path string) error {
	switch v.Kind() {
	case reflect.String:
		if !v.CanSet() {
			return nil
		}
		out, err := String(v.String())
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		v.SetString(out)

	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return walk(v.Elem(), path)

	case reflect.Struct:
		t := v.Type()
		for i := range t.NumField() {
			if t.Field(i).PkgPath != "" {
				continue // unexported
			}
			if err := walk(v.Field(i), joinPath(path, t.Field(i).Name)); err != nil {
				return err
			}
		}

	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if err := walk(v.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}

	case reflect.Map:
		// Map values are not addressable, so copy, resolve, and write back.
		for _, k := range v.MapKeys() {
			cp := reflect.New(v.Type().Elem()).Elem()
			cp.Set(v.MapIndex(k))
			if err := walk(cp, fmt.Sprintf("%s[%v]", path, k.Interface())); err != nil {
				return err
			}
			v.SetMapIndex(k, cp)
		}
	}
	return nil
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}
