package manifest

import (
	"fmt"
	"os"
	"strings"

	"github.com/confluentinc/kcp/internal/targets"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/goccy/go-yaml"
)

// CredentialsRef is a credentials slot spelled as a path to a credentials file.
//
// It is a struct rather than a bare string so callers keep the IsZero/String/
// NewCredentialsPath helpers and the never-print-secret-contents property.
// Inline credential mappings are not accepted anywhere: secret material belongs
// in a referenced file, never embedded in a manifest.
type CredentialsRef struct {
	// Path is the credentials file path the slot names.
	Path string
}

// IsZero reports whether the slot was omitted, or is a path of only
// whitespace — treated the same as omitted so callers see one clear "must not
// be empty" error instead of a raw OS error from resolving a blank path.
func (r CredentialsRef) IsZero() bool { return strings.TrimSpace(r.Path) == "" }

// UnmarshalYAML implements goccy's BytesUnmarshaler. The slot must be a scalar
// file path; a mapping (an inline secret block) is rejected. The error never
// names a specific field — this hook runs identically for every credentials
// slot in the manifest — and never echoes the node's contents, so a secret
// spelled inline by mistake does not reach err.Error() and therefore kcp.log.
func (r *CredentialsRef) UnmarshalYAML(b []byte) error {
	var s string
	if err := yaml.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("this credentials field must be a path to a credentials file; inline credential blocks are not supported")
	}
	r.Path = s
	return nil
}

// MarshalYAML renders the ref as its path string, so a manifest round-trips.
func (r CredentialsRef) MarshalYAML() (any, error) {
	return r.Path, nil
}

// ResolveMigrateCluster reads the referenced file into auth-only Kafka
// credentials, then validates. No field is inferred: an omitted required field
// (e.g. the SASL/SCRAM mechanism) is rejected rather than defaulted, so the
// config file alone determines what was selected — the same rule for kcp
// migration and kcp migrate.
func (r CredentialsRef) ResolveMigrateCluster() (types.MigrateClusterCredentials, []error) {
	if r.IsZero() {
		return types.MigrateClusterCredentials{}, []error{fmt.Errorf("credentials: must not be empty")}
	}
	warnIfGroupOrWorldReadable(r.Path, "credentials file")
	data, err := os.ReadFile(r.Path)
	if err != nil {
		return types.MigrateClusterCredentials{}, []error{fmt.Errorf("failed to read migrate credentials file: %w", err)}
	}
	mc, err := types.UnmarshalMigrateClusterCredentials(data)
	if err != nil {
		return types.MigrateClusterCredentials{}, []error{err}
	}
	return mc, types.ValidateMigrateClusterCredentials(mc)
}

// ResolveTarget reads the referenced file into REST-shaped target credentials.
func (r CredentialsRef) ResolveTarget() (*targets.Credentials, error) {
	if r.IsZero() {
		return nil, fmt.Errorf("credentials: must not be empty")
	}
	warnIfGroupOrWorldReadable(r.Path, "credentials file")
	return targets.LoadCredentials(r.Path)
}

// String renders the ref for error messages.
func (r CredentialsRef) String() string {
	return r.Path
}

// blankRef reports whether a ref is absent or is a path of only whitespace.
func blankRef(r CredentialsRef) bool {
	return r.IsZero()
}

// NewCredentialsPath builds a path-form CredentialsRef. It exists so callers
// that already hold a path (tests, and the flag→manifest paths) can build a ref
// without going through YAML.
func NewCredentialsPath(path string) CredentialsRef {
	return CredentialsRef{Path: path}
}
