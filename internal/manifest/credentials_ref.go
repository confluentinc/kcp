package manifest

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/confluentinc/kcp/internal/interpolate"
	"github.com/confluentinc/kcp/internal/targets"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/goccy/go-yaml"
)

// CredentialsRef is a credentials slot spelled EITHER as a path to a
// credentials file OR as an inline mapping with that file's top-level shape.
//
// A polymorphic field rather than a `credentials:`/`credentialsFile:` sibling
// pair: a sibling pair would make `credentials:` mean "inline block" in one
// kind and "path" in another, in two files a user has open side by side —
// exactly the two-meanings-one-name drift this format exists to avoid.
type CredentialsRef struct {
	// Path is the credentials file path when the slot is spelled as a string.
	// It is an ordinary manifest string, so a manifest that opts in to
	// interpolation may write ${SECRETS_DIR}/source.yaml here.
	Path string
	// Inline is the raw YAML of the mapping when the slot is spelled inline.
	// Kept as bytes rather than a typed struct because the same spelling serves
	// two different credentials shapes (Kafka legs and the REST leg); the
	// decode happens in the Resolve* method that knows which is wanted.
	Inline []byte
}

// IsZero reports whether the slot was omitted entirely.
func (r CredentialsRef) IsZero() bool { return r.Path == "" && len(r.Inline) == 0 }

// IsInline reports whether the slot was spelled as a mapping.
func (r CredentialsRef) IsInline() bool { return len(r.Inline) > 0 }

// UnmarshalYAML implements goccy's BytesUnmarshaler. It receives the raw YAML
// of the node, which is what makes the string/mapping discrimination possible
// without a second parse of the whole document.
func (r *CredentialsRef) UnmarshalYAML(b []byte) error {
	var s string
	if err := yaml.Unmarshal(b, &s); err == nil {
		r.Path = s
		r.Inline = nil
		return nil
	}
	r.Path = ""
	r.Inline = bytes.Clone(b)
	return nil
}

// MarshalYAML renders the ref back in whichever form it was written, so a
// manifest round-trips.
func (r CredentialsRef) MarshalYAML() (any, error) {
	if r.IsInline() {
		var node any
		if err := yaml.Unmarshal(r.Inline, &node); err != nil {
			return nil, err
		}
		return node, nil
	}
	return r.Path, nil
}

// rejectInlineInterpolateKey guards the one key that must not appear inside an
// inline block. `interpolate` is file-level: honouring it here would resolve
// the block twice — once on this decode and once on the manifest's own pass —
// and a secret that legitimately contains "${...}" would be expanded on the
// second pass.
func rejectInlineInterpolateKey(inline []byte) error {
	var probe struct {
		Interpolate *bool `yaml:"interpolate"`
	}
	if err := yaml.Unmarshal(inline, &probe); err == nil && probe.Interpolate != nil {
		return fmt.Errorf("interpolate: is a file-level key and is not valid inside an inline credentials block; set it at the top level of the manifest instead")
	}
	return nil
}

// ResolveMigrateCluster resolves the slot into auth-only Kafka credentials.
//
// interp governs ONLY the inline form. A referenced file governs itself via its
// own top-level `interpolate:` key, so a manifest opting in never changes how a
// shared kcp migrate credentials file is read.
func (r CredentialsRef) ResolveMigrateCluster(interp bool) (types.MigrateClusterCredentials, []error) {
	if r.IsZero() {
		return types.MigrateClusterCredentials{}, []error{fmt.Errorf("credentials: must not be empty")}
	}
	if !r.IsInline() {
		return types.LoadMigrateClusterCredentials(r.Path)
	}

	if err := rejectInlineInterpolateKey(r.Inline); err != nil {
		return types.MigrateClusterCredentials{}, []error{err}
	}

	// Strict() is re-applied explicitly: goccy does not propagate the outer
	// decode's options into a custom unmarshaler, so without this an inline
	// block would silently accept typos that a file rejects.
	var mc types.MigrateClusterCredentials
	if err := yaml.UnmarshalWithOptions(r.Inline, &mc, yaml.Strict()); err != nil {
		return types.MigrateClusterCredentials{}, []error{fmt.Errorf("parsing inline credentials: %w", err)}
	}
	if interp {
		if err := interpolate.Struct(&mc); err != nil {
			return types.MigrateClusterCredentials{}, []error{fmt.Errorf("resolving inline credentials: %w", err)}
		}
	}
	return mc, types.ValidateMigrateClusterCredentials(mc)
}

// ResolveTarget resolves the slot into REST-shaped target credentials, with the
// same inline/file rules as ResolveMigrateCluster.
func (r CredentialsRef) ResolveTarget(interp bool) (*targets.Credentials, error) {
	if r.IsZero() {
		return nil, fmt.Errorf("credentials: must not be empty")
	}
	if !r.IsInline() {
		return targets.LoadCredentials(r.Path)
	}

	if err := rejectInlineInterpolateKey(r.Inline); err != nil {
		return nil, err
	}

	var c targets.Credentials
	if err := yaml.UnmarshalWithOptions(r.Inline, &c, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("parsing inline credentials: %w", err)
	}
	if interp {
		if err := interpolate.Struct(&c); err != nil {
			return nil, fmt.Errorf("resolving inline credentials: %w", err)
		}
	}
	if err := targets.ValidateCredentials(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

// String renders the ref for error messages. An inline block is summarised
// rather than printed, because printing it would put credentials in a log line.
func (r CredentialsRef) String() string {
	if r.IsInline() {
		return "<inline credentials>"
	}
	return r.Path
}

// interpolateInto resolves ${ENV_VAR} references across every string reachable
// from v. Inline credential blocks are held as raw bytes and so are NOT reached
// by this walk — they are resolved on decode instead (ResolveMigrateCluster),
// which keeps each block to exactly one resolution pass.
func interpolateInto(v any) error {
	return interpolate.Struct(v)
}

// blankRef reports whether a ref is absent or is a path of only whitespace.
func blankRef(r CredentialsRef) bool {
	return r.IsZero() || (!r.IsInline() && strings.TrimSpace(r.Path) == "")
}

// NewCredentialsPath builds a path-form CredentialsRef. It exists so callers
// that already hold a path (tests, and the flag→manifest paths) can build a ref
// without going through YAML.
func NewCredentialsPath(path string) CredentialsRef {
	return CredentialsRef{Path: path}
}
