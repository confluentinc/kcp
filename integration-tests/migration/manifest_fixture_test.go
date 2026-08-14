package migration_test

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"
)

// manifestOpts is everything that varies between the converted e2e scenarios.
// Topology comes from setup.sh via .env; policy is per-scenario and is read
// FRESH by execute on every run, which is what lets a scenario re-render the same
// path between init and execute.
type manifestOpts struct {
	MetadataName    string
	SourceBootstrap string
	DestBootstrap   string
	DestClusterID   string
	RestEndpoint    string
	ClusterLinkName string
	APIKey          string
	APISecret       string
	Namespace       string
	GatewayName     string
	FencedCR        string
	SwitchoverCR    string
	KubePath        string

	PauseConsumerOffsetSync bool
	Policy                  policyOpts
}

// policyOpts are the execute-time knobs the nine execute sites vary. Every zero
// value is rendered as an ABSENT key rather than as a literal 0: the schema's
// duration pattern requires a unit, so `0` would not match, and an absent key
// already means the zero the retired flags defaulted to.
type policyOpts struct {
	LagThreshold            int
	PromoteBatchSize        int
	RolloutTimeout          time.Duration
	DetectUnroutedProducers time.Duration
	ConsumerOffsetSyncDrain time.Duration
}

// Any reports whether any knob is set, so the template can omit the whole block.
func (p policyOpts) Any() bool {
	return p.LagThreshold != 0 || p.PromoteBatchSize != 0 || p.RolloutTimeout != 0 ||
		p.DetectUnroutedProducers != 0 || p.ConsumerOffsetSyncDrain != 0
}

func testdataDir() string {
	// integration-tests/migration/ -> integration-tests/migration/testdata/
	return "testdata"
}

// renderGatewayMigration renders the GatewayMigration manifest for one scenario.
//
// The fixture is rendered as TEXT rather than marshalled from a
// manifest.GatewayMigration struct on purpose: the e2e exists to exercise the
// real parser on a real user-facing artifact, and goccy marshals time.Duration
// as an integer nanosecond count, which is not the shipped spelling.
func renderGatewayMigration(opts manifestOpts) (string, error) {
	path := filepath.Join(testdataDir(), "gateway-migration.yaml.tmpl")
	name := filepath.Base(path)

	tmpl, err := template.New(name).
		Funcs(template.FuncMap{"yamlQuote": yamlQuote}).
		ParseFiles(path)
	if err != nil {
		return "", fmt.Errorf("parsing manifest template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, opts); err != nil {
		return "", fmt.Errorf("rendering manifest template: %w", err)
	}
	return buf.String(), nil
}

// yamlEscapableControls are the control characters strconv.Quote renders as a
// NAMED escape (\a \b \f \n \r \t \v). kcp's inline-credential decode handles
// these; it does not handle the \xNN / \uNNNN forms Quote uses for everything
// else non-printable.
var yamlEscapableControls = map[rune]bool{
	'\a': true, '\b': true, '\f': true, '\n': true, '\r': true, '\t': true, '\v': true,
}

// yamlQuote renders s as a YAML double-quoted scalar, or refuses.
//
// EVERY string substituted into the template goes through this. Templating text
// into YAML before the parser sees it is a YAML-injection hazard, and the failure
// modes are not all loud: a value beginning with `&` or `#` yields an EMPTY
// password rather than an error, and goccy silently discards tabs in plain
// scalars. Quoting is the only defence, because the substitution necessarily
// happens pre-parse.
//
// The escaping is generic rather than a list of the characters the abuse-case
// test enumerates: `"` and `\` are what actually terminate a double-quoted
// scalar, so a character allowlist would pass its own test and still be broken.
//
// It also REFUSES rather than emitting an escape kcp cannot read. YAML 1.2 does
// accept every escape strconv.Quote produces, but kcp does not read inline
// credentials with YAML's top-level parser: CredentialsRef hands the block's raw
// node bytes to a nested decode that implements only the named escapes, so a
// \xNN or \uNNNN value fails with "found unknown escape character" or "could not
// find end character of double-quoted text". Those are fail-closed, but they
// surface as a bare column number with the source excerpt (correctly) stripped —
// no context at all. The realistic trigger is a copy-pasted secret carrying a BOM
// or a zero-width character, and the instinct when debugging that is to disable
// the stripper or dump the manifest, either of which would turn a robustness bug
// into a credential leak. Better to refuse at render time and say why.
func yamlQuote(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("value is not valid UTF-8: it would render as a \\xNN escape that kcp's " +
			"inline-credential decode cannot read (value withheld)")
	}
	for _, r := range s {
		if !strconv.IsPrint(r) && !yamlEscapableControls[r] {
			return "", fmt.Errorf("value contains U+%04X, which renders as a \\x/\\u escape that kcp's "+
				"inline-credential decode cannot read (value withheld)", r)
		}
	}
	return strconv.Quote(s), nil
}

// podWriteScript creates the target file at mode 0600 and fills it from stdin.
//
// `umask 077` rather than a chmod afterwards, because `cat >` does not change an
// existing file's mode and a post-hoc chmod would leave a window at 0644. `rm -f`
// first so a re-render — which is how a scenario varies policy between init and
// execute — cannot inherit a mode from an earlier create. The path arrives as "$1"
// rather than spliced into the script.
//
// `&&` rather than `;` between rm and cat: with `;` the script's exit status is
// cat's alone, so an rm that failed (EACCES, a file owned by another uid) would
// be ignored — cat would truncate the existing file, PRESERVE its old mode, and
// the caller's require.NoError would still pass, silently defeating the 0600
// guarantee. `rm -f` exits 0 on a missing file, so `&&` costs nothing.
const podWriteScript = `umask 077; rm -f "$1" && cat > "$1"`

// podWriteCommand builds the kubectl argv that writes a manifest into the runner
// pod. Split out from writeManifestToPod, and taking plain strings rather than an
// envConfig, so its no-secrets-in-argv property is unit-testable without a
// cluster — and so this file needs no e2e build tag.
func podWriteCommand(kubeContext, namespace, pod, podPath string) []string {
	return []string{
		"--context", kubeContext,
		"-n", namespace,
		"exec", "-i", pod, "--",
		"sh", "-c", podWriteScript, "sh", podPath,
	}
}

// credentialKeys are the manifest's secret-bearing leaf keys.
var credentialKeys = []string{"username", "password", "api_key", "api_secret"}

// manifestForLog returns the manifest with its credential values replaced, for
// t.Logf. A failed e2e run is much harder to diagnose without the manifest in the
// output, and test output is CI output.
//
// Redaction keys off the FIELD PATH, not the value. A value-substring replace
// would corrupt the topology whenever a credential happened to share a value with
// a topology field, and — the real hazard — it only ever redacts the values the
// caller remembered to pass in, so a credential field added to the manifest later
// would log in clear with no test failure to catch it. Keying off the field name
// means a new secret under one of these keys is redacted by default.
// It is line-oriented, which is sound here for a REASON THAT MUST NOT SILENTLY
// CHANGE: yamlQuote collapses every value onto a single double-quoted line, so a
// credential can never span lines or share a line with another key. If yamlQuote
// ever emitted a block scalar (`password: |`) or a flow mapping
// (`sasl_plain: {password: x}`), this would print "<redacted>" on the key line
// and leak the value anyway — the worst kind of failure, because it looks
// redacted. The two are coupled; change one and re-check the other.
var credentialLineRe = regexp.MustCompile(
	`^([-\s]*)['"]?(` + strings.Join(credentialKeys, "|") + `)['"]?(\s*):`)

func manifestForLog(rendered string) string {
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		if m := credentialLineRe.FindStringSubmatch(line); m != nil {
			lines[i] = m[1] + m[2] + m[3] + ": <redacted>"
		}
	}
	return strings.Join(lines, "\n")
}
