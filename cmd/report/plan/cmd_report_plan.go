package plan

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	kcpoutput "github.com/confluentinc/kcp/internal/output"
	"github.com/confluentinc/kcp/internal/services/plan"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile  string
	planInputs string
	outputDir  string
	output     string
	configPath string
)

func NewReportPlanCmd() *cobra.Command {
	reportPlanCmd := &cobra.Command{
		Use:   "plan",
		Short: "Generate a Migration Plan to migrate to Confluent Cloud (Experimental / WIP)",
		Long: "Generate a Migration Plan to migrate to Confluent Cloud from a kcp state file produced by `kcp scan` (Experimental / WIP). " +
			"The plan provides technical recommendations on target cluster sizing, networking, authentication, and migration approach for each source cluster, and surfaces open questions to capture your intent so the generated plan fits your use case.\n\n" +
			"**Output:** writes `plan.md` and/or `plan.json` to `--output-dir` (default `./plan-output`).",
		Example: `  # Minimal: state file in, plan.md/plan.json out
  kcp report plan --state-file kcp-state.json

  # With your overrides
  kcp report plan --state-file kcp-state.json --plan-inputs plan-inputs.yaml

  # JSON only
  kcp report plan --state-file kcp-state.json --output json`,
		SilenceErrors: true,
		SilenceUsage:  true, // don't dump --help on runtime errors (only flag-parse errors should surface usage)
		PreRunE:       preRunReportPlan,
		RunE:          runReportPlan,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "Path to your kcp-state.json file (produced by kcp scan).")
	reportPlanCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&planInputs, "plan-inputs", "", "Path to plan-inputs.yaml with your overrides. All fields optional.")
	optionalFlags.StringVar(&outputDir, "output-dir", "./plan-output", "Directory to write plan.md / plan.json into.")
	optionalFlags.StringVar(&output, "output", "md,json", "Comma-separated output formats: md, json, or both.")
	optionalFlags.StringVar(&configPath, "config", "", "Path to a plan-config.yaml override. Embedded config is the default.")
	reportPlanCmd.Flags().AddFlagSet(optionalFlags)
	_ = reportPlanCmd.Flags().MarkHidden("config")
	groups[optionalFlags] = "Optional Flags"

	reportPlanCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)
		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags}
		groupNames := []string{"Required Flags", "Optional Flags"}
		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}
		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")
		return nil
	})

	_ = reportPlanCmd.MarkFlagRequired("state-file")
	return reportPlanCmd
}

func preRunReportPlan(cmd *cobra.Command, _ []string) error {
	return utils.BindEnvToFlags(cmd)
}

func runReportPlan(_ *cobra.Command, _ []string) error {
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		return fmt.Errorf("state file does not exist: %s", stateFile)
	}
	state, err := loadState(stateFile)
	if err != nil {
		return fmt.Errorf("load --state-file %s: %w", stateFile, err)
	}

	cfg, err := plan.LoadPlanConfig(configPath)
	if err != nil {
		if configPath != "" {
			return fmt.Errorf("load --config %s: %w", configPath, err)
		}
		return fmt.Errorf("load embedded plan-config: %w", err)
	}

	rawInputs, err := plan.LoadPlanInputs(planInputs)
	if err != nil {
		return fmt.Errorf("load --plan-inputs %s: %w", planInputs, err)
	}
	inputs := plan.ResolvePlanInputs(rawInputs, cfg)

	rs := report.NewReportService()
	processed := rs.ProcessState(*state)

	svc := plan.NewPlanService(cfg, nil)
	p, err := svc.Build(processed, inputs, stateFile)
	if err != nil {
		return fmt.Errorf("build plan: %w", err)
	}

	writeMD, writeJSON, err := parseOutputFormats(output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create --output-dir %s: %w", outputDir, err)
	}

	if writeMD {
		data, err := plan.RenderMarkdown(p, cfg)
		if err != nil {
			return err
		}
		path := filepath.Join(outputDir, "plan.md")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("write plan.md: %w", err)
		}
		kcpoutput.Println("wrote", path)
	}
	if writeJSON {
		data, err := plan.RenderJSON(p)
		if err != nil {
			return err
		}
		path := filepath.Join(outputDir, "plan.json")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("write plan.json: %w", err)
		}
		kcpoutput.Println("wrote", path)
	}
	return nil
}

// loadState reads the state file and tolerates two pre-0.7 layouts the
// strict decoder rejects on its own: (1) `regions` at the top level
// instead of `msk_sources.regions`, and (2) `schema_registries` as a
// flat array (entries discriminated by a `type` field) instead of an
// object with `confluent_schema_registry` / `aws_glue` buckets. When
// the strict decode succeeds the file is taken as-is; only on strict
// failure do we attempt the lenient legacy decode and rebuild into the
// modern shape. The fallback uses typed decodes (not map round-trips),
// so byte ordering of the original state file does not influence the
// resulting `*types.State`.
func loadState(path string) (*types.State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	state, strictErr := types.NewStateFromBytes(data)
	if strictErr == nil {
		return state, nil
	}
	migrated, mErr := migrateLegacyState(data)
	if mErr != nil {
		// Surface the original strict-decode error (more actionable
		// for genuine version mismatches than the legacy attempt's
		// own failure).
		return nil, strictErr
	}
	return migrated, nil
}

// migrateLegacyState rebuilds a *types.State from a pre-0.7 state-file
// JSON layout. Strips known-legacy top-level keys (`regions`,
// `schema_registries`-as-array) from the raw JSON before the lenient
// decode so a type mismatch on a legacy field can't block migration,
// then re-attaches each legacy field into the modern shape. Returns an
// error if no recognised legacy shape is found so the caller falls
// back to surfacing the original strict-decode error.
func migrateLegacyState(data []byte) (*types.State, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	// Refuse to migrate when the state file claims it was produced by
	// kcp >= 0.7. The legacy shapes only existed pre-0.7; a newer file
	// hitting this path means we encountered a forward-incompatible
	// shape (not a legacy one) and should NOT silently mutate it into
	// the modern layout. The strict-decode error path is the correct
	// signal in that case.
	if v := stateFileKCPVersion(raw); v != "" && isPostLegacyVersion(v) {
		return nil, fmt.Errorf("state file claims kcp_build_info.version=%q, which is post-legacy; refusing to apply pre-0.7 migration shim", v)
	}
	legacyRegions := raw["regions"]
	legacySchemaRegistries := raw["schema_registries"]
	// Only strip schema_registries if it's a JSON array (legacy shape);
	// modern files have it as an object and should be preserved as-is.
	isLegacySR := len(legacySchemaRegistries) > 0 && legacySchemaRegistries[0] == '['
	if len(legacyRegions) == 0 && !isLegacySR {
		return nil, fmt.Errorf("no legacy state-file shape detected")
	}
	delete(raw, "regions")
	if isLegacySR {
		delete(raw, "schema_registries")
	}
	stripped, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var state types.State
	if err := json.Unmarshal(stripped, &state); err != nil {
		return nil, err
	}
	if len(legacyRegions) > 0 && state.MSKSources == nil {
		var regions []types.DiscoveredRegion
		if err := json.Unmarshal(legacyRegions, &regions); err == nil && len(regions) > 0 {
			state.MSKSources = &types.MSKSourcesState{Regions: regions}
		}
	}
	if isLegacySR && state.SchemaRegistries == nil {
		var entries []json.RawMessage
		if err := json.Unmarshal(legacySchemaRegistries, &entries); err == nil {
			srs := &types.SchemaRegistriesState{}
			for _, raw := range entries {
				var disc struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal(raw, &disc); err != nil {
					continue
				}
				switch disc.Type {
				case "glue", "aws_glue":
					var g types.GlueSchemaRegistryInformation
					if err := json.Unmarshal(raw, &g); err == nil {
						srs.AWSGlue = append(srs.AWSGlue, g)
					}
				default:
					var sr types.SchemaRegistryInformation
					if err := json.Unmarshal(raw, &sr); err == nil {
						srs.ConfluentSchemaRegistry = append(srs.ConfluentSchemaRegistry, sr)
					}
				}
			}
			if len(srs.ConfluentSchemaRegistry) > 0 || len(srs.AWSGlue) > 0 {
				state.SchemaRegistries = srs
			}
		}
	}
	if state.MSKSources == nil && state.OSKSources == nil && state.SchemaRegistries == nil {
		return nil, fmt.Errorf("legacy keys present but nothing populated after migration")
	}
	slog.Warn("loaded pre-0.7 state-file layout in legacy-compatibility mode; re-run `kcp discover` / `kcp scan` to refresh the file in the current schema")
	return &state, nil
}

// stateFileKCPVersion extracts kcp_build_info.version from the raw
// state-file JSON map. Returns "" when the field is absent or
// unparseable — the caller treats absent / unparseable as "no signal"
// and proceeds with the legacy migration.
func stateFileKCPVersion(raw map[string]json.RawMessage) string {
	bi, ok := raw["kcp_build_info"]
	if !ok {
		return ""
	}
	var info struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(bi, &info); err != nil {
		return ""
	}
	return info.Version
}

// isPostLegacyVersion reports whether the state-file version string
// represents kcp 0.7 or later (the cutoff where legacy `regions` /
// flat `schema_registries` shapes ceased to be produced). The
// localdev sentinel "0.0.0-localdev" is treated as pre-0.7 by virtue
// of its leading zeros — fine, since dev builds shouldn't be loading
// production state files.
func isPostLegacyVersion(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	// Strip leading "v" if present.
	if strings.HasPrefix(v, "v") || strings.HasPrefix(v, "V") {
		v = v[1:]
	}
	// Strip pre-release / build suffix at the first '-' or '+'.
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, mErr := parseInt(parts[0])
	minor, nErr := parseInt(parts[1])
	if mErr != nil || nErr != nil {
		return false
	}
	if major > 0 {
		return true
	}
	return minor >= 7
}

func parseInt(s string) (int, error) {
	var n int
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a non-negative integer: %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

// parseOutputFormats accepts a comma-separated `md,json` list (or the legacy
// `both`). Returns flags for each format and an actionable error if the
// input names anything else.
func parseOutputFormats(raw string) (md bool, jsonOut bool, err error) {
	if raw == "" || raw == "both" {
		return true, true, nil
	}
	for _, f := range strings.Split(raw, ",") {
		switch strings.TrimSpace(f) {
		case "md":
			md = true
		case "json":
			jsonOut = true
		default:
			return false, false, fmt.Errorf("--output %q is invalid; valid values are md, json, or md,json", raw)
		}
	}
	if !md && !jsonOut {
		return false, false, fmt.Errorf("--output %q produced no formats; supply at least md or json", raw)
	}
	return md, jsonOut, nil
}
