package main

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/confluentinc/kcp/internal/types"
)

const (
	stepDiscover               = "discover"
	stepScanClusters           = "scan-clusters"
	stepScanMSKConnectors      = "scan-msk-connectors"
	stepScanSelfManagedConnect = "scan-self-managed-connectors"
	stepScanSchemaRegistry     = "scan-schema-registry"

	// Lambda's synchronous response limit is 6 MB; leave headroom for logs and JSON escaping.
	rawStateLimit = 3 << 20

	defaultLogTailBytes = 6000
	maxLogTailBytes     = 64 << 10
	redacted            = "***"
	maxWarnings         = 40
	maxWarnLineLen      = 600
)

// authMethods are the values accepted for auth_method (the auth_method keys of msk-credentials.yaml).
var authMethods = []string{"iam", "sasl_scram", "sasl_plain", "tls", "unauthenticated_tls", "unauthenticated_plaintext"}

// metricsGranularities are the values kcp scan msk-connectors accepts for --metrics-granularity.
var metricsGranularities = []string{"60s", "5m", "1h", "1d"}

// maxStateInflated bounds the decompressed size of state_gzip_b64 (a 6 MB payload can expand a lot).
const maxStateInflated = 100 << 20

// allSteps is the fixed execution order, regardless of the order in the event.
var allSteps = []string{stepDiscover, stepScanClusters, stepScanMSKConnectors, stepScanSelfManagedConnect, stepScanSchemaRegistry}

type connectAuth struct {
	Type     string `json:"type"` // unauthenticated (default) | basic
	Username string `json:"username"`
	Password string `json:"password"`
	// SecretArn is a Secrets Manager secret {"username","password"} that replaces username and
	// password for type basic. Mutually exclusive with password.
	SecretArn string `json:"secret_arn"`
}

// schemaRegistry describes one registry for the scan-schema-registry step.
type schemaRegistry struct {
	Type string `json:"type"` // confluent | glue

	// confluent
	URL                   string      `json:"url"`
	Auth                  connectAuth `json:"auth"`
	InsecureSkipTLSVerify bool        `json:"insecure_skip_tls_verify"`

	// glue
	RegistryName string `json:"registry_name"`
	Region       string `json:"region"` // default: the event region

	// Not supported yet (the files they reference cannot be supplied in the payload); rejected.
	UseMTLS   bool   `json:"use_mtls"`
	TLSCACert string `json:"tls_ca_cert"`
}

type event struct {
	ClusterArn string   `json:"cluster_arn"`
	Region     string   `json:"region"`
	Steps      []string `json:"steps"` // default: all applicable

	State           json.RawMessage `json:"state"`            // input state (required if discover is not a step)
	StateGzipB64    string          `json:"state_gzip_b64"`   // alternative to state: gzip + base64 of the state JSON
	CredentialsYAML string          `json:"credentials_yaml"` // msk-credentials.yaml (required if discover is not a step)

	AccessKeyID     string `json:"aws_access_key_id"`
	SecretAccessKey string `json:"aws_secret_access_key"`
	SessionToken    string `json:"aws_session_token"`

	SkipMetrics                 bool   `json:"skip_metrics"`                  // discover --skip-metrics
	ConnectorMetricsGranularity string `json:"connector_metrics_granularity"` // msk-connectors --metrics-granularity ("" = 1d, "none" = skip)

	ConnectRestURL         string      `json:"connect_rest_url"`
	ConnectAuth            connectAuth `json:"connect_auth"`
	ConnectInsecureSkipTLS bool        `json:"connect_insecure_skip_tls_verify"`

	SchemaRegistries []schemaRegistry `json:"schema_registries"` // scan-schema-registry runs once per item

	// AuthMethod forces the auth used by scan-clusters (iam | sasl_scram | sasl_plain | tls |
	// unauthenticated_tls | unauthenticated_plaintext). Default: whatever discover selected.
	AuthMethod string `json:"auth_method"`

	// SASLSecretArn is a Secrets Manager secret {"username","password"} applied to the target
	// cluster's sasl_scram (or sasl_plain, per auth_method) entry before scan-clusters.
	SASLSecretArn string `json:"sasl_secret_arn"`

	LogTailBytes int `json:"log_tail_bytes"` // per step, default 6000
}

type stepResult struct {
	Step       string   `json:"step"`
	Target     string   `json:"target,omitempty"` // which registry, for scan-schema-registry
	Args       []string `json:"args,omitempty"`
	ExitCode   int      `json:"exit_code"` // -1 = skipped
	DurationMs int64    `json:"duration_ms"`
	LogTail    string   `json:"log_tail"`
	Warnings   []string `json:"warnings,omitempty"` // WARN/ERROR lines kcp wrote to kcp.log during this step
}

type result struct {
	Steps           []stepResult    `json:"steps"`
	DurationMs      int64           `json:"duration_ms"`
	State           json.RawMessage `json:"state,omitempty"`            // when small enough
	StateGzipB64    string          `json:"state_gzip_b64,omitempty"`   // otherwise
	StateBytes      int             `json:"state_bytes"`                // uncompressed size
	CredentialsYAML string          `json:"credentials_yaml,omitempty"` // from discover (no secrets: auth methods + bootstrap)
	Error           string          `json:"error,omitempty"`

	// fatal marks hard failures (bad event, working-dir setup) that are reported to the Lambda
	// runtime as a function error rather than as a normal response.
	fatal bool
}

type handler struct {
	kcpPath string // path to the bundled kcp binary
	tmpRoot string // parent of the per-invocation working dir
	logger  *slog.Logger

	secretsClient secretsClientFunc // nil = client from the process (execution role) credentials
}

// validate checks the event and returns the steps to run, in execution order.
func (ev event) validate() ([]string, error) {
	if ev.ClusterArn == "" || ev.Region == "" {
		return nil, errors.New("cluster_arn and region are required")
	}
	// arn:aws:kafka:<region>:<account>:cluster/<name>/<uuid>
	if parts := strings.Split(ev.ClusterArn, ":"); len(parts) < 6 || parts[3] != ev.Region {
		return nil, errors.New("region does not match the region in cluster_arn")
	}
	if ev.AccessKeyID == "" || ev.SecretAccessKey == "" {
		return nil, errors.New("aws_access_key_id and aws_secret_access_key are required")
	}
	if ev.AuthMethod != "" && !slices.Contains(authMethods, ev.AuthMethod) {
		return nil, errors.New("unknown auth_method " + ev.AuthMethod)
	}
	if ev.ConnectorMetricsGranularity != "" && ev.ConnectorMetricsGranularity != "none" &&
		!slices.Contains(metricsGranularities, ev.ConnectorMetricsGranularity) {
		return nil, errors.New("unknown connector_metrics_granularity " + ev.ConnectorMetricsGranularity)
	}
	if len(ev.State) > 0 && string(ev.State) != "null" && ev.StateGzipB64 != "" {
		return nil, errors.New("state and state_gzip_b64 are mutually exclusive")
	}
	if ev.SASLSecretArn != "" {
		if _, err := parseSecretArn(ev.SASLSecretArn); err != nil {
			return nil, fmt.Errorf("sasl_secret_arn: %w", err)
		}
		if ev.AuthMethod != "" && ev.AuthMethod != "sasl_scram" && ev.AuthMethod != "sasl_plain" {
			return nil, errors.New("sasl_secret_arn requires auth_method sasl_scram or sasl_plain (or empty)")
		}
	}
	for _, s := range ev.Steps {
		if !slices.Contains(allSteps, s) {
			return nil, errors.New("unknown step " + s)
		}
	}
	if ev.CredentialsYAML != "" {
		// yaml errors quote source lines (secrets included); reject without echoing them.
		var creds types.Credentials
		if yaml.Unmarshal([]byte(ev.CredentialsYAML), &creds) != nil {
			return nil, errors.New("credentials_yaml is not valid YAML")
		}
	}
	if err := checkNoUserinfo(ev.ConnectRestURL); err != nil {
		return nil, fmt.Errorf("connect_rest_url: %w", err)
	}
	if err := ev.ConnectAuth.validate(); err != nil {
		return nil, fmt.Errorf("connect_auth: %w", err)
	}
	for i, sr := range ev.SchemaRegistries {
		if err := sr.validate(); err != nil {
			return nil, fmt.Errorf("schema_registries[%d]: %w", i, err)
		}
	}
	if len(ev.Steps) == 0 {
		return slices.Clone(allSteps), nil
	}
	ordered := make([]string, 0, len(allSteps))
	for _, s := range allSteps {
		if slices.Contains(ev.Steps, s) {
			ordered = append(ordered, s)
		}
	}
	return ordered, nil
}

// checkNoUserinfo rejects URLs with embedded credentials (user:pass@host), which would leak via
// argv and logs. The error never echoes the URL.
func checkNoUserinfo(raw string) error {
	if raw == "" {
		return nil
	}
	// Same normalisation as kcp scan self-managed-connectors: without it, "alice:pw@host:8083"
	// parses with "alice" as the scheme and the userinfo check is bypassed.
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("is not a valid URL")
	}
	if u.User != nil {
		return errors.New("must not contain credentials (userinfo); use the auth fields instead")
	}
	return nil
}

func (a connectAuth) validate() error {
	if a.SecretArn == "" {
		return nil
	}
	if a.Password != "" {
		return errors.New("secret_arn and password are mutually exclusive")
	}
	if a.Type != "basic" {
		return errors.New("secret_arn requires type basic")
	}
	if _, err := parseSecretArn(a.SecretArn); err != nil {
		return fmt.Errorf("secret_arn: %w", err)
	}
	return nil
}

func (sr schemaRegistry) validate() error {
	if sr.UseMTLS || sr.TLSCACert != "" {
		return errors.New("mTLS and custom CA are not supported yet")
	}
	switch sr.Type {
	case "confluent":
		if sr.URL == "" {
			return errors.New("url is required for type confluent")
		}
		if err := checkNoUserinfo(sr.URL); err != nil {
			return fmt.Errorf("url: %w", err)
		}
		if sr.Auth.Type != "" && sr.Auth.Type != "unauthenticated" && sr.Auth.Type != "basic" {
			return errors.New("unknown auth type " + sr.Auth.Type)
		}
		if err := sr.Auth.validate(); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	case "glue":
		if sr.RegistryName == "" {
			return errors.New("registry_name is required for type glue")
		}
	default:
		return errors.New("unknown type " + sr.Type)
	}
	return nil
}

// cleanStale removes working dirs left behind by a previous sandbox that was killed mid-run.
func (h *handler) cleanStale() {
	matches, err := filepath.Glob(filepath.Join(h.tmpRoot, "kcp-*"))
	if err != nil {
		return
	}
	for _, m := range matches {
		_ = os.RemoveAll(m)
	}
}

// handle runs the requested steps. ctx should end shortly before the Lambda deadline; when it
// does, remaining steps are skipped and whatever state exists is still returned.
func (r *result) addError(msg string) {
	if r.Error != "" {
		r.Error += "; "
	}
	r.Error += msg
}

func (h *handler) handle(ctx context.Context, ev event) result {
	start := time.Now()
	var r result

	steps, err := ev.validate()
	if err != nil {
		r.Error, r.fatal = err.Error(), true
		return r
	}
	tailN := ev.LogTailBytes
	if tailN <= 0 {
		tailN = defaultLogTailBytes
	}
	tailN = min(tailN, maxLogTailBytes)

	h.cleanStale()
	dir, err := os.MkdirTemp(h.tmpRoot, "kcp-")
	if err != nil {
		r.Error, r.fatal = "failed to create working dir: "+err.Error(), true
		return r
	}
	defer func() { _ = os.RemoveAll(dir) }()
	statePath := filepath.Join(dir, "kcp-state.json")
	credsPath := filepath.Join(dir, "msk-credentials.yaml")
	scanCredsPath := filepath.Join(dir, "msk-credentials.scan.yaml")
	inputState, err := ev.inputState()
	if err != nil {
		r.Error, r.fatal = err.Error(), true
		return r
	}
	haveInputState := len(inputState) > 0
	if haveInputState {
		if err := os.WriteFile(statePath, inputState, 0o600); err != nil {
			r.Error, r.fatal = "failed to write state: "+err.Error(), true
			return r
		}
	}
	if ev.CredentialsYAML != "" {
		if err := os.WriteFile(credsPath, []byte(ev.CredentialsYAML), 0o600); err != nil {
			r.Error, r.fatal = "failed to write credentials: "+err.Error(), true
			return r
		}
	}
	scrub := newScrubber(ev)

	// Read Secrets Manager secrets with the function role before any step runs, and register the
	// values with the scrubber. A failed read fails only the step that needs the secret.
	secrets := h.readSecrets(ctx, ev.secretArns(steps))
	for _, sc := range secrets {
		scrub.add(sc.username)
		scrub.add(sc.password)
	}

	// Only the payload credentials are visible to kcp; the function role's are dropped.
	env := []string{
		"HOME=" + dir,
		"AWS_ACCESS_KEY_ID=" + ev.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY=" + ev.SecretAccessKey,
		"AWS_SESSION_TOKEN=" + ev.SessionToken,
		"AWS_REGION=" + ev.Region,
		"AWS_DEFAULT_REGION=" + ev.Region,
		"AWS_EC2_METADATA_DISABLED=true",
	}

	var target string
	failStep := func(name, msg string) {
		r.Steps = append(r.Steps, stepResult{Step: name, Target: target, ExitCode: 1, LogTail: msg})
	}
	// basicAuth returns the kcp args and env for basic auth. Inline credentials: username on argv,
	// password in env. From a secret: both in env (kcp binds --username and --password to
	// USERNAME and PASSWORD), so neither appears in the returned args.
	basicAuth := func(a connectAuth) ([]string, []string, error) {
		if a.SecretArn == "" {
			return []string{"--use-basic-auth", "--username", a.Username}, []string{"PASSWORD=" + a.Password}, nil
		}
		sc := secrets[a.SecretArn]
		if sc.err != nil {
			return nil, nil, sc.err
		}
		return []string{"--use-basic-auth"}, []string{"USERNAME=" + sc.username, "PASSWORD=" + sc.password}, nil
	}
	run := func(name string, args, shown, extraEnv []string, needState, needCreds bool) {
		sr := stepResult{Step: name, Target: target, Args: shown}
		switch {
		case needState && !fileExists(statePath):
			sr.ExitCode, sr.LogTail = -1, "skipped: no kcp-state.json (run discover or pass state)"
		case needCreds && !fileExists(credsPath):
			sr.ExitCode, sr.LogTail = -1, "skipped: no msk-credentials.yaml (run discover or pass credentials_yaml)"
		default:
			if name == stepScanClusters {
				// MSKSource.Scan walks every cluster in the file; hand kcp only the requested one.
				// The auth override and secret values go into this copy only, never into credsPath
				// (which is what is returned as credentials_yaml).
				if err := filterCredentials(credsPath, scanCredsPath, ev.ClusterArn); err != nil {
					sr.ExitCode, sr.LogTail = 1, "credentials: "+err.Error()
					r.Steps = append(r.Steps, sr)
					return
				}
			}
			if name == stepScanClusters && ev.SASLSecretArn != "" {
				sc := secrets[ev.SASLSecretArn]
				method := cmp.Or(ev.AuthMethod, "sasl_scram")
				err := sc.err
				if err == nil {
					err = applySASLSecret(scanCredsPath, ev.ClusterArn, method, sc.username, sc.password)
				}
				if err == nil {
					err = forceAuth(scanCredsPath, ev.ClusterArn, method)
				}
				if err != nil {
					sr.ExitCode, sr.LogTail = 1, "sasl_secret_arn: "+err.Error()
					r.Steps = append(r.Steps, sr)
					return
				}
			} else if name == stepScanClusters && ev.AuthMethod != "" {
				if err := forceAuth(scanCredsPath, ev.ClusterArn, ev.AuthMethod); err != nil {
					// Never fall back to whatever auth discover picked (possibly plaintext).
					sr.ExitCode, sr.LogTail = 1, "auth_method: "+err.Error()
					r.Steps = append(r.Steps, sr)
					return
				}
			}
			logPath := filepath.Join(dir, "kcp.log")
			var off int64
			if fi, err := os.Stat(logPath); err == nil {
				off = fi.Size()
			}
			cmd := exec.CommandContext(ctx, h.kcpPath, args...)
			cmd.Dir, cmd.Env = dir, append(slices.Clone(env), extraEnv...)
			cmd.WaitDelay = 5 * time.Second
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			t := time.Now()
			err := cmd.Run()
			sr.DurationMs = time.Since(t).Milliseconds()
			sr.Warnings = warnLines(logPath, off, maxWarnings, scrub)
			// Scrub the full output before truncating so a secret cannot be cut in half.
			sr.LogTail += tail(scrub.apply(out.String()), tailN)
			if err != nil {
				sr.ExitCode = 1
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					sr.ExitCode = ee.ExitCode()
				}
				// -1 means "skipped"; a signal-killed (e.g. deadline-interrupted) step failed.
				if sr.ExitCode == -1 {
					sr.ExitCode = 1
				}
				if ctx.Err() != nil {
					r.addError("Lambda deadline reached during " + name)
				}
				sr.LogTail += "\n" + err.Error()
			}
		}
		r.Steps = append(r.Steps, sr)
	}

	var abort string // when set, remaining steps are recorded as skipped
	for _, s := range steps {
		if s == stepScanSelfManagedConnect && ev.ConnectRestURL == "" {
			if len(ev.Steps) > 0 { // explicitly requested but not applicable
				r.Steps = append(r.Steps, stepResult{Step: s, ExitCode: -1, LogTail: "skipped: no connect_rest_url"})
			}
			continue
		}
		if s == stepScanSchemaRegistry && len(ev.SchemaRegistries) == 0 {
			if len(ev.Steps) > 0 { // explicitly requested but not applicable
				r.Steps = append(r.Steps, stepResult{Step: s, ExitCode: -1, LogTail: "skipped: no schema_registries"})
			}
			continue
		}
		if abort == "" && ctx.Err() != nil {
			abort = "skipped: Lambda deadline reached"
			r.addError("Lambda deadline reached; remaining steps skipped")
		}
		if abort != "" {
			r.Steps = append(r.Steps, stepResult{Step: s, ExitCode: -1, LogTail: abort})
			continue
		}

		switch s {
		case stepDiscover:
			a := []string{"discover", "--cluster-arn", ev.ClusterArn, "--skip-costs"}
			if ev.SkipMetrics {
				a = append(a, "--skip-metrics")
			}
			run(s, a, a, nil, false, false)
			if last := r.Steps[len(r.Steps)-1]; last.ExitCode != 0 && !haveInputState {
				abort = "skipped: discover failed and no input state was supplied"
			}
		case stepScanClusters:
			a := []string{"scan", "clusters", "--source-type", "msk", "--state-file", statePath, "--credentials-file", scanCredsPath}
			run(s, a, a, nil, true, true)
		case stepScanMSKConnectors:
			a := []string{"scan", "msk-connectors", "--state-file", statePath, "--cluster-arn", ev.ClusterArn}
			g := ev.ConnectorMetricsGranularity
			if g == "" {
				g = "1d"
			}
			if g != "none" {
				a = append(a, "--metrics-granularity", g)
			}
			run(s, a, a, nil, true, false)
		case stepScanSelfManagedConnect:
			a := []string{"scan", "self-managed-connectors", "--state-file", statePath,
				"--connect-rest-url", ev.ConnectRestURL, "--cluster-id", ev.ClusterArn}
			var extraEnv []string
			if ev.ConnectAuth.Type == "basic" {
				basicArgs, basicEnv, err := basicAuth(ev.ConnectAuth)
				if err != nil {
					failStep(s, "connect_auth.secret_arn: "+err.Error())
					continue
				}
				// The password goes via the env var kcp binds to --password, not argv.
				a = append(a, basicArgs...)
				extraEnv = basicEnv
			} else {
				a = append(a, "--use-unauthenticated")
			}
			if ev.ConnectInsecureSkipTLS {
				a = append(a, "--insecure-skip-tls-verify")
			}
			run(s, a, a, extraEnv, true, false)
		case stepScanSchemaRegistry:
			for _, sr := range ev.SchemaRegistries {
				if ctx.Err() != nil {
					target = sr.URL
					if sr.Type == "glue" {
						target = "glue:" + sr.RegistryName
					}
					r.Steps = append(r.Steps, stepResult{Step: s, Target: target, ExitCode: -1, LogTail: "skipped: Lambda deadline reached"})
					if !strings.Contains(r.Error, "deadline") {
						r.addError("Lambda deadline reached; remaining steps skipped")
					}
					continue
				}
				a := []string{"scan", "schema-registry", "--state-file", statePath, "--sr-type", sr.Type}
				var extraEnv []string
				if sr.Type == "glue" {
					region := sr.Region
					if region == "" {
						region = ev.Region
					}
					a = append(a, "--region", region, "--registry-name", sr.RegistryName)
					target = "glue:" + sr.RegistryName
				} else {
					a = append(a, "--url", sr.URL)
					if sr.Auth.Type == "basic" {
						target = sr.URL
						basicArgs, basicEnv, err := basicAuth(sr.Auth)
						if err != nil {
							failStep(s, "schema_registries.auth.secret_arn: "+err.Error())
							continue
						}
						a = append(a, basicArgs...)
						extraEnv = basicEnv
					} else {
						a = append(a, "--use-unauthenticated")
					}
					if sr.InsecureSkipTLSVerify {
						a = append(a, "--insecure-skip-tls-verify")
					}
					target = sr.URL
				}
				run(s, a, a, extraEnv, true, false)
			}
			target = ""
		}
	}

	for i := range r.Steps {
		r.Steps[i].LogTail = scrub.apply(r.Steps[i].LogTail)
		for j := range r.Steps[i].Warnings {
			r.Steps[i].Warnings[j] = scrub.apply(r.Steps[i].Warnings[j])
		}
		for j := range r.Steps[i].Args {
			r.Steps[i].Args[j] = scrub.apply(r.Steps[i].Args[j])
		}
	}

	if b, err := os.ReadFile(statePath); err == nil {
		r.StateBytes = len(b)
		if len(b) <= rawStateLimit && json.Valid(b) {
			r.State = b
		} else if r.StateGzipB64, err = gzipBase64(b); err != nil {
			r.addError("failed to compress state: " + err.Error())
		}
	}
	if b, err := os.ReadFile(credsPath); err == nil && slices.Contains(steps, stepDiscover) {
		// The file can carry caller-supplied secrets that survived discover's merge.
		red, err := redactCredentials(b)
		if err != nil {
			r.addError("credentials_yaml withheld: " + err.Error())
		} else {
			// Not scrubbed: redactCredentials already blanked every secret field, and a raw
			// substring replace could corrupt the document (e.g. a password equal to "true").
			r.CredentialsYAML = string(red)
		}
	}
	r.DurationMs = time.Since(start).Milliseconds()
	return r
}

// scrubber masks known secret values in anything returned to the caller.
type scrubber struct{ secrets []string }

func newScrubber(ev event) scrubber {
	var sc scrubber
	add := func(v string) {
		if v != "" {
			sc.secrets = append(sc.secrets, v)
		}
	}
	add(ev.SecretAccessKey)
	add(ev.SessionToken)
	add(ev.ConnectAuth.Password)
	for _, sr := range ev.SchemaRegistries {
		add(sr.Auth.Password)
	}
	for _, v := range credentialSecrets([]byte(ev.CredentialsYAML)) {
		add(v)
	}
	return sc
}

// secretArns lists the distinct secret ARNs needed by the steps that will run.
func (ev event) secretArns(steps []string) []string {
	var out []string
	add := func(a string) {
		if a != "" && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	if slices.Contains(steps, stepScanClusters) {
		add(ev.SASLSecretArn)
	}
	if slices.Contains(steps, stepScanSelfManagedConnect) && ev.ConnectRestURL != "" && ev.ConnectAuth.Type == "basic" {
		add(ev.ConnectAuth.SecretArn)
	}
	if slices.Contains(steps, stepScanSchemaRegistry) {
		for _, sr := range ev.SchemaRegistries {
			if sr.Type == "confluent" && sr.Auth.Type == "basic" {
				add(sr.Auth.SecretArn)
			}
		}
	}
	return out
}

func (sc *scrubber) add(v string) {
	if v != "" {
		sc.secrets = append(sc.secrets, v)
	}
}

func (sc scrubber) apply(s string) string {
	// Longest first, so a secret that contains another is not left as a fragment.
	secrets := slices.Clone(sc.secrets)
	slices.SortFunc(secrets, func(a, b string) int { return cmp.Or(len(b)-len(a), strings.Compare(a, b)) })
	secrets = slices.Compact(secrets)
	for _, v := range secrets {
		if v == "" {
			continue
		}
		s = strings.ReplaceAll(s, v, redacted)
	}
	return s
}

// inputState returns the state JSON from state or state_gzip_b64, or nil if neither is set.
func (ev event) inputState() ([]byte, error) {
	if ev.StateGzipB64 == "" {
		if len(ev.State) == 0 || string(ev.State) == "null" {
			return nil, nil
		}
		return ev.State, nil
	}
	raw, err := base64.StdEncoding.DecodeString(ev.StateGzipB64)
	if err != nil {
		return nil, errors.New("state_gzip_b64 is not valid base64")
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("state_gzip_b64 is not valid gzip")
	}
	b, err := io.ReadAll(io.LimitReader(zr, maxStateInflated+1))
	if err != nil {
		return nil, errors.New("state_gzip_b64 is not valid gzip")
	}
	if len(b) > maxStateInflated {
		return nil, errors.New("state_gzip_b64 is too large when decompressed")
	}
	if !json.Valid(b) {
		return nil, errors.New("state_gzip_b64 does not contain valid JSON")
	}
	return b, nil
}

func gzipBase64(b []byte) (string, error) {
	var gz bytes.Buffer
	w, err := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(b); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gz.Bytes()), nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// warnLines returns up to max scrubbed WARN/ERROR lines written to the log file after offset off.
func warnLines(path string, off int64, max int, scrub scrubber) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	// kcp.log is rotated (lumberjack): a file smaller than the saved offset is a new one.
	if fi, err := f.Stat(); err == nil && fi.Size() < off {
		off = 0
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, " WARN ") || strings.Contains(l, " ERROR ") {
			l = scrub.apply(l) // before truncation, so a secret cannot be cut in half
			if len(l) > maxWarnLineLen {
				l = l[:maxWarnLineLen]
			}
			out = append(out, l)
			if len(out) == max {
				break
			}
		}
	}
	return out
}

func tail(s string, n int) string {
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}
