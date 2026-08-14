package interpolate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestString_ResolvesSetVariable(t *testing.T) {
	t.Setenv("MSK_PASSWORD", "s3cret")
	got, err := String("${MSK_PASSWORD}")
	require.NoError(t, err)
	assert.Equal(t, "s3cret", got)
}

func TestString_ResolvesEmbeddedVariable(t *testing.T) {
	t.Setenv("REGION", "us-east-1")
	got, err := String("b-1.msk.${REGION}.amazonaws.com:9096")
	require.NoError(t, err)
	assert.Equal(t, "b-1.msk.us-east-1.amazonaws.com:9096", got)
}

func TestString_ResolvesMultipleVariables(t *testing.T) {
	t.Setenv("A", "one")
	t.Setenv("B", "two")
	got, err := String("${A}-${B}")
	require.NoError(t, err)
	assert.Equal(t, "one-two", got)
}

// TestString_UndefinedVariableIsHardError — an empty password silently
// attempting auth is the worst failure mode available, so an unset variable
// stops the run.
func TestString_UndefinedVariableIsHardError(t *testing.T) {
	_, err := String("${DEFINITELY_NOT_SET_KCP}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DEFINITELY_NOT_SET_KCP")
}

// TestString_ErrorNamesVariableNotValue is the logging hazard from §10: an
// error on a mis-set variable must never carry a resolved secret into kcp.log.
func TestString_ErrorNamesVariableNotValue(t *testing.T) {
	t.Setenv("KCP_SET_VAR", "super-secret-value")
	_, err := String("${KCP_SET_VAR}-${KCP_UNSET_VAR}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "KCP_UNSET_VAR")
	assert.NotContains(t, err.Error(), "super-secret-value",
		"a resolved secret must never appear in an error string")
}

// TestString_EscapeYieldsLiteral — passwords legitimately contain "${".
func TestString_EscapeYieldsLiteral(t *testing.T) {
	got, err := String("$${NOT_A_VAR}")
	require.NoError(t, err)
	assert.Equal(t, "${NOT_A_VAR}", got)
}

// TestString_DoubleDollarOutsideEscapeSurvives — p@$$w0rd must round-trip
// unchanged; the escape is "$${" specifically, not "$$".
func TestString_DoubleDollarOutsideEscapeSurvives(t *testing.T) {
	got, err := String("p@$$w0rd")
	require.NoError(t, err)
	assert.Equal(t, "p@$$w0rd", got)
}

// TestString_BareDollarVarIsLiteral — $VAR would mangle passwords containing $.
func TestString_BareDollarVarIsLiteral(t *testing.T) {
	t.Setenv("HOME_DIR", "/root")
	got, err := String("$HOME_DIR")
	require.NoError(t, err)
	assert.Equal(t, "$HOME_DIR", got)
}

// TestString_UnterminatedIsLiteral — a password ending in "${" is valid input.
func TestString_UnterminatedIsLiteral(t *testing.T) {
	got, err := String("trailing${")
	require.NoError(t, err)
	assert.Equal(t, "trailing${", got)
}

// TestString_EmptyNameIsError — "${}" is a typo, not a variable.
func TestString_EmptyNameIsError(t *testing.T) {
	_, err := String("${}")
	require.Error(t, err)
}

// TestString_DefaultSyntaxNotSupported — "${VAR:-x}" is out of scope, and a
// default secret is a footgun. It must fail loudly, not resolve to "x".
func TestString_DefaultSyntaxNotSupported(t *testing.T) {
	_, err := String("${KCP_MISSING:-fallback}")
	require.Error(t, err)
}

// TestString_ValueWithSpecialCharsIsNotReinterpreted proves the value is taken
// verbatim: a password containing YAML structure characters, a newline, and a
// tab survives byte-for-byte. This is what post-parse resolution buys — the
// value never passes through the YAML parser.
func TestString_ValueWithSpecialCharsIsNotReinterpreted(t *testing.T) {
	nasty := "foo\nusername: attacker\n\t&anchor *alias !!str"
	t.Setenv("NASTY", nasty)
	got, err := String("${NASTY}")
	require.NoError(t, err)
	assert.Equal(t, nasty, got)
	assert.True(t, strings.Contains(got, "\t"), "tabs must survive resolution")
}

// TestString_ResolvedValueIsNotReScanned — a value that itself contains
// "${OTHER}" must not trigger a second round of expansion.
func TestString_ResolvedValueIsNotReScanned(t *testing.T) {
	t.Setenv("OUTER", "${INNER}")
	t.Setenv("INNER", "should-not-appear")
	got, err := String("${OUTER}")
	require.NoError(t, err)
	assert.Equal(t, "${INNER}", got)
}

func TestString_NoVariablesIsUnchanged(t *testing.T) {
	got, err := String("plain-value")
	require.NoError(t, err)
	assert.Equal(t, "plain-value", got)
}

type inner struct {
	Password string
	CACert   string
}

type outer struct {
	Name     string
	Hosts    []string
	Tags     map[string]string
	Inner    inner
	InnerPtr *inner
	NilPtr   *inner
	Count    int
	Enabled  bool
	unseen   string //nolint:unused // proves unexported fields are skipped, not panicked on
}

func TestStruct_ResolvesNestedStringFields(t *testing.T) {
	t.Setenv("PW", "pw-value")
	t.Setenv("HOST", "h1")
	t.Setenv("TAG", "t1")
	t.Setenv("NAME", "n1")

	v := outer{
		Name:     "${NAME}",
		Hosts:    []string{"${HOST}", "literal"},
		Tags:     map[string]string{"k": "${TAG}"},
		Inner:    inner{Password: "${PW}"},
		InnerPtr: &inner{Password: "${PW}"},
	}
	require.NoError(t, Struct(&v))

	assert.Equal(t, "n1", v.Name)
	assert.Equal(t, []string{"h1", "literal"}, v.Hosts)
	assert.Equal(t, "t1", v.Tags["k"])
	assert.Equal(t, "pw-value", v.Inner.Password)
	assert.Equal(t, "pw-value", v.InnerPtr.Password)
	assert.Nil(t, v.NilPtr)
}

// TestStruct_ReportsUndefinedVariableWithFieldPath — the operator needs to know
// which field failed, and the path must not carry the value.
func TestStruct_ReportsUndefinedVariableWithFieldPath(t *testing.T) {
	v := outer{Inner: inner{CACert: "${KCP_NO_SUCH_CA}"}}
	err := Struct(&v)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "KCP_NO_SUCH_CA")
	assert.Contains(t, err.Error(), "Inner.CACert")
}

// TestStruct_LeavesNonStringKindsAlone guards against the walker corrupting
// numeric/bool fields.
func TestStruct_LeavesNonStringKindsAlone(t *testing.T) {
	v := outer{Count: 7, Enabled: true, Name: "x"}
	require.NoError(t, Struct(&v))
	assert.Equal(t, 7, v.Count)
	assert.True(t, v.Enabled)
}

// TestStruct_RequiresPointer — a non-pointer cannot be mutated, so silently
// doing nothing would be the worst outcome.
func TestStruct_RequiresPointer(t *testing.T) {
	err := Struct(outer{Name: "${X}"})
	require.Error(t, err)
}

type withAny struct {
	Field  any
	AnyMap map[string]any
}

// TestStruct_ResolvesStringBehindInterface pins the contract the reflective walk
// promises: a string reachable only through an interface-typed field, or a
// map[string]any value, is resolved — not silently shipped as a literal
// "${VAR}". The concrete value inside an interface is never addressable, so the
// walk must copy-resolve-write-back through the settable interface itself.
func TestStruct_ResolvesStringBehindInterface(t *testing.T) {
	t.Setenv("IFACE_PW", "iface-secret")
	t.Setenv("MAPANY_PW", "mapany-secret")

	v := withAny{
		Field:  "${IFACE_PW}",
		AnyMap: map[string]any{"k": "${MAPANY_PW}"},
	}
	require.NoError(t, Struct(&v))
	assert.Equal(t, "iface-secret", v.Field)
	assert.Equal(t, "mapany-secret", v.AnyMap["k"])
}

// TestStruct_ReportsUndefinedVariableBehindInterface — the same fail-loud
// contract applies through an interface: an unset variable is an error, and the
// error names the variable, never a value.
func TestStruct_ReportsUndefinedVariableBehindInterface(t *testing.T) {
	v := withAny{Field: "${KCP_NO_SUCH_IFACE_VAR}"}
	err := Struct(&v)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "KCP_NO_SUCH_IFACE_VAR")
}
