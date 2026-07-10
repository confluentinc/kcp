package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/build_info"
)

// CurrentSchemaVersion is the schema_version this build reads and writes.
// Bump in lockstep with any breaking change to the kcp-state.json shape, and
// add the matching upcaster to steps (see internal/state/migrate/steps.go).
const CurrentSchemaVersion = 1

// ErrNewerSchema means the file was written by a newer (released) KCP than this build can model.
var ErrNewerSchema = errors.New("state file schema is newer than this KCP build supports")

// ErrNewerSchemaDev is ErrNewerSchema's variant for a file STAMPED by a dev build
// (its declared schema_version may correspond to an unreleased/local shape — see spec §6.9).
var ErrNewerSchemaDev = errors.New("state file schema is newer than this KCP build supports and was written by a development build")

// ErrUnsupportedLegacy means the file is an old shape for which no upcaster exists yet.
var ErrUnsupportedLegacy = errors.New("state file is from an unsupported legacy format")

// Upgrade transforms raw kcp-state.json bytes of any prior shape into bytes
// conforming to CurrentSchemaVersion. It never decodes into types.State.
// fromLabel is a human-readable description of the detected source shape.
func Upgrade(data []byte) (migrated []byte, fromLabel string, err error) {
	schemaVersion, buildVersion, era, err := detectVersion(data)
	if err != nil {
		return nil, "", fmt.Errorf("failed to inspect state file: %w", err)
	}

	slog.Debug("inspecting state file schema",
		"detected_schema_version", schemaVersion,
		"kcp_build_version", buildVersion,
		"era", era,
		"current_schema_version", CurrentSchemaVersion,
	)

	if schemaVersion > CurrentSchemaVersion {
		// File-driven dev check: a dev-STAMPED file may carry a schema_version for an
		// unreleased/local shape, so do not advise `kcp update` (spec §6.9). We inspect
		// the file's own build version only — never the reader binary's. A MISSING
		// version is "unknown", not dev: such a file takes the released-newer path
		// (advise `kcp update`), since "dev-stamped" requires an actual dev stamp.
		if buildVersion != "" && build_info.IsDevVersion(buildVersion) {
			return nil, "", fmt.Errorf("%w (file is schema_version %d, this build supports up to %d)",
				ErrNewerSchemaDev, schemaVersion, CurrentSchemaVersion)
		}
		return nil, "", fmt.Errorf("%w (file is schema_version %d, this build supports up to %d)",
			ErrNewerSchema, schemaVersion, CurrentSchemaVersion)
	}

	// Current shape: pass through unchanged.
	if schemaVersion == CurrentSchemaVersion {
		slog.Debug("state file already at current schema, no migration needed", "schema_version", schemaVersion)
		return data, fmt.Sprintf("schema_version=%d", schemaVersion), nil
	}
	// Era C file without an explicit schema_version is the current shape. A pre-v0.4.0
	// region-scan file or unrelated JSON also lands here (era defaults to C, spec N5):
	// it passes through unchanged and fails later at the strict decode, like any foreign file.
	if schemaVersion == 0 && era == "C" {
		label := "era=C"
		if buildVersion != "" {
			label = "kcp_build_info.version=" + buildVersion
		}
		slog.Debug("state file has current-era shape without an explicit schema_version, treating as current", "label", label)
		return data, label, nil
	}

	// Legacy file: run the ordered upcaster chain.
	// Decode with UseNumber so every JSON number survives as its exact literal
	// (json.Number) instead of being widened to float64. The upcasters only
	// reshuffle top-level keys and never read numeric values, so a faithful
	// round-trip matters: plain json.Unmarshal into map[string]any would lose
	// integer precision above 2^53 and re-serialize values >= 1e21 in scientific
	// notation (e.g. "1e+21"), which the strict types.State decode then rejects.
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, "", fmt.Errorf("failed to parse legacy state file: %w", err)
	}
	applied := false
	for _, s := range steps {
		if s.appliesWhen(era, buildVersion) {
			slog.Debug("applying state schema migration step", "step", s.name, "era", era)
			doc, err = s.transform(doc)
			if err != nil {
				return nil, "", fmt.Errorf("migration step %q failed: %w", s.name, err)
			}
			applied = true
		}
	}
	if !applied {
		return nil, "", fmt.Errorf("%w (era %s, build %q)", ErrUnsupportedLegacy, era, buildVersion)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, "", fmt.Errorf("failed to re-serialize migrated state: %w", err)
	}
	label := "era=" + era
	if buildVersion != "" {
		label = "kcp_build_info.version=" + buildVersion
	}
	slog.Info("migrated state file to current schema", "from", label, "to_schema_version", CurrentSchemaVersion)
	return out, label, nil
}
