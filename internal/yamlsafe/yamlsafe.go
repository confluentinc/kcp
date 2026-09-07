// Package yamlsafe holds the one YAML-error rule every secret-bearing decode in
// kcp must apply.
package yamlsafe

import "github.com/goccy/go-yaml"

// StripSourceExcerpt removes the source excerpt goccy attaches to decode errors.
//
// goccy annotates every decode error with three lines of the offending
// document. Under Strict(), a single typo anywhere near a credential therefore
// renders the neighbouring `password:` or `api_secret:` line into err.Error() —
// and main.go funnels every command error to slog.Error, so it lands in
// kcp.log, the rotating artefact operators attach to support tickets. The
// credentials file and the manifest are not.
//
// The position and the offending key survive, which is the part an operator can
// act on; only the rendered source goes.
//
// Apply this at EVERY decode point that can see credentials: the two
// credentials loaders, the inline-block decodes, and the manifest itself, which
// is secret-bearing whenever credentials are written inline.
func StripSourceExcerpt(err error) error {
	if err == nil {
		return nil
	}
	return yamlError(yaml.FormatError(err, false, false))
}

// yamlError is a plain error carrying an already-formatted YAML message.
type yamlError string

func (e yamlError) Error() string { return string(e) }
