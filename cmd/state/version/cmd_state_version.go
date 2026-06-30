package version

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// stateMetadata holds only the top-level metadata of a kcp-state.json.
//
// It is parsed leniently — a plain json.Unmarshal with no DisallowUnknownFields, no full
// types.State decode, and no migration — so `kcp state version` reports metadata for state
// files from ANY KCP version, including ones the strict loader cannot read (e.g. files whose
// schema_registries is the old array form). Unknown/extra fields are simply ignored.
//
// This is INTENTIONALLY a standalone struct (incl. the inline kcp_build_info) and does NOT
// reuse types.State / types.KcpBuildInfo. Staying decoupled from the canonical schema is
// precisely what lets this command read files those structs reject; coupling it would make a
// lenient inspector track changes to types it is meant to be resilient to. Do not "dedup" it.
type stateMetadata struct {
	SchemaVersion int `json:"schema_version"`
	KcpBuildInfo  struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	} `json:"kcp_build_info"`
	Timestamp    string `json:"timestamp"`
	UpdatedAt    string `json:"updated_at"`
	MigratedFrom string `json:"migrated_from"`
}

// parseStateMetadata leniently extracts metadata from raw state-file bytes. It only fails if
// the bytes are not a JSON object.
func parseStateMetadata(data []byte) (stateMetadata, error) {
	var meta stateMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return stateMetadata{}, fmt.Errorf("could not parse file as a JSON object: %w", err)
	}
	return meta, nil
}

// hasKCPMarkers reports whether the file carries any field that identifies it as a kcp-state.json.
func (m stateMetadata) hasKCPMarkers() bool {
	return m.KcpBuildInfo.Version != "" || m.SchemaVersion != 0 || m.Timestamp != "" ||
		m.UpdatedAt != "" || m.MigratedFrom != ""
}

// renderStateMetadata prints the same fields, in the same order, as the UI's state-file info
// popover (cmd/ui/frontend/src/components/common/StateMetadataPopover.tsx) — keep the two in
// sync when the metadata set changes.
func renderStateMetadata(w io.Writer, path string, m stateMetadata) {
	lines := []string{fmt.Sprintf("State file: %s", path)}

	// A valid JSON object with none of the KCP markers isn't a kcp-state.json — say so and
	// stop, rather than printing a misleading "Schema version: unversioned (legacy)" row.
	if !m.hasKCPMarkers() {
		lines = append(lines, "  (no KCP metadata found — this does not look like a kcp-state.json file)")
		_, _ = fmt.Fprintln(w, strings.Join(lines, "\n"))
		return
	}

	row := func(label, value string) {
		lines = append(lines, fmt.Sprintf("  %-15s %s", label+":", value))
	}

	schema := "unversioned (legacy)"
	if m.SchemaVersion != 0 {
		schema = strconv.Itoa(m.SchemaVersion)
	}
	row("Schema version", schema)
	if m.KcpBuildInfo.Version != "" {
		row("KCP build", m.KcpBuildInfo.Version)
	}
	if c := m.KcpBuildInfo.Commit; c != "" && c != "unknown" {
		row("Commit", c)
	}
	if d := m.KcpBuildInfo.Date; d != "" && d != "unknown" {
		row("Build date", d)
	}
	if m.Timestamp != "" {
		row("Created", m.Timestamp)
	}
	if m.UpdatedAt != "" {
		row("Last updated", m.UpdatedAt)
	}
	if m.MigratedFrom != "" {
		row("Migrated from", m.MigratedFrom)
	}
	_, _ = fmt.Fprintln(w, strings.Join(lines, "\n"))
}

func NewStateVersionCmd() *cobra.Command {
	var stateFile string
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Report the metadata of a kcp-state.json file",
		Long:  "Reads only the top-level metadata of a state file (schema version, KCP build, created/updated timestamps, migration provenance) using lenient JSON parsing, so it works on state files from any KCP version — even ones that cannot be fully loaded by the current build.",
		Example: `  # Report the schema version and build metadata of a state file
  kcp state version --state-file kcp-state.json`,
		SilenceErrors: true,
		SilenceUsage:  true, // a read/parse error is not a usage error — don't dump the flags
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			data, err := os.ReadFile(stateFile)
			if err != nil {
				return fmt.Errorf("failed to read state file %s: %w", stateFile, err)
			}
			meta, err := parseStateMetadata(data)
			if err != nil {
				return fmt.Errorf("%s: %w", stateFile, err)
			}
			renderStateMetadata(cmd.OutOrStdout(), stateFile, meta)
			return nil
		},
	}
	cmd.Flags().StringVar(&stateFile, "state-file", "", "Path to the state file to inspect (required)")
	_ = cmd.MarkFlagRequired("state-file")
	return cmd
}
