package plan

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/confluentinc/kcp/internal/services/plan"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile     string
	planInputs    string
	outputDir     string
	output        string
	filterCluster string
	filterRegion  string
)

func NewReportPlanCmd() *cobra.Command {
	reportPlanCmd := &cobra.Command{
		Use:   "plan",
		Short: "Generate a Migration Plan to migrate to Confluent Cloud",
		Long: "Generate a Migration Plan to migrate to Confluent Cloud from a kcp state file produced by `kcp scan`.\n\n" +
			"For each scanned cluster the plan recommends a target cluster type, sizing, networking, authentication, and data-migration approach, and lists the questions a scan can't answer.\n\n" +
			"**How it works:** kcp auto-answers everything it can from the scan. What it can't derive is written to `plan-inputs.yaml` (in the current directory) — fleet-wide questions once under `defaults:`, per-cluster facts under each cluster, each answer a short, stable token with the full wording in the comment above it. Edit that file in place and re-run; kcp reads your answers back, folds them into the plan, and rewrites the file with any follow-ups revealed — so it converges over a couple of passes.\n\n" +
			"**Output:** `plan.md` and `plan.json` go to `--output-dir` (default `./plan-output`); `plan-inputs.yaml` is read from and written back to `--plan-inputs` (default `./plan-inputs.yaml`) — edit it in place between runs.",
		Example: `  # First pass: state file in; writes ./plan-inputs.yaml + plan.md/plan.json
  kcp report plan --state-file kcp-state.json

  # Edit ./plan-inputs.yaml, then re-run — same command refines the plan
  kcp report plan --state-file kcp-state.json

  # JSON only
  kcp report plan --state-file kcp-state.json --output json`,
		SilenceErrors: true,
		SilenceUsage:  true, // don't dump --help on runtime errors (only flag-parse errors should surface usage)
		PreRunE:       preRunReportPlan,
		RunE:          runReportPlan,
	}

	groups := map[*pflag.FlagSet]string{}

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&stateFile, "state-file", "", "Path to your kcp-state.json file (produced by kcp scan). Optional: omit it to run as a pure questionnaire — the planner starts one cluster with nothing derived, so every fact becomes a question in plan-inputs.yaml.")
	optionalFlags.StringVar(&planInputs, "plan-inputs", "./plan-inputs.yaml", "Path to plan-inputs.yaml — read from and written back to in place. kcp seeds it on the first run; edit it and re-run to refine the plan. All answers optional.")
	optionalFlags.StringVar(&outputDir, "output-dir", "./plan-output", "Directory to write plan.md / plan.json into.")
	optionalFlags.StringVar(&output, "output", "md,json", "Comma-separated output formats: md, json, or both.")
	optionalFlags.StringVar(&filterCluster, "cluster-id", "", "[Optional] Plan only this cluster (name or ARN). Default: every cluster in the scan.")
	optionalFlags.StringVar(&filterRegion, "region", "", "[Optional] Plan only clusters in this region. Default: every region in the scan.")
	reportPlanCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	reportPlanCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)
		if usage := optionalFlags.FlagUsages(); usage != "" {
			fmt.Printf("Flags:\n%s\n", usage)
		}
		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")
		return nil
	})

	return reportPlanCmd
}

func preRunReportPlan(cmd *cobra.Command, _ []string) error {
	return utils.BindEnvToFlags(cmd)
}

func runReportPlan(_ *cobra.Command, _ []string) error {
	// No --state-file: run as a pure questionnaire against one synthetic cluster,
	// so every fact that a scan would derive surfaces as a question instead.
	scanless := stateFile == ""
	var processed report.ProcessedState
	if scanless {
		processed = plan.ScanlessState()
	} else {
		if _, err := os.Stat(stateFile); os.IsNotExist(err) {
			return fmt.Errorf("state file does not exist: %s", stateFile)
		}
		state, err := loadState(stateFile)
		if err != nil {
			return fmt.Errorf("load --state-file %s: %w", stateFile, err)
		}
		rs := report.NewReportService()
		processed = rs.ProcessState(*state)
	}

	// Read the customer's existing answers if the file is present. A missing file
	// is the normal first-run case — proceed with no declared inputs and seed one.
	loadPath := planInputs
	if _, statErr := os.Stat(planInputs); os.IsNotExist(statErr) {
		loadPath = ""
	}
	declared, inputErrs, err := plan.LoadDeclaredInputs(loadPath)
	if err != nil {
		return fmt.Errorf("load --plan-inputs %s: %w", planInputs, err)
	}
	// Misspelled keys, invalid values, or misspelled cluster names would otherwise be
	// silently ignored, leaving the user with a plan that looks answered but isn't. So
	// collect every such mistake and fail before writing anything, rather than emit a
	// plan built on dropped answers. Cluster-name checks need a real scan to compare
	// against (skip them in questionnaire mode).
	if !scanless {
		inputErrs = append(inputErrs, plan.ValidateDeclaredClusters(declared, processed)...)
	}
	if len(inputErrs) > 0 {
		return fmt.Errorf("plan-inputs %s has %d problem(s) to fix (no plan was written):\n  - %s",
			planInputs, len(inputErrs), strings.Join(inputErrs, "\n  - "))
	}

	// Optional fleet filter — never prompts; unset means the whole scan.
	if filterCluster != "" || filterRegion != "" {
		filtered, matched := plan.FilterState(processed, filterCluster, filterRegion)
		if matched == 0 {
			return fmt.Errorf("no clusters match --cluster-id %q / --region %q; check the names against your scan", filterCluster, filterRegion)
		}
		processed = filtered
	}

	ep := plan.BuildEnginePlan(processed, declared, stateFile, nil)
	if scanless {
		ep.Header.Source = "Questionnaire (no scan file)"
	}
	// Remaining warnings are advisories, not user mistakes (for example Apache Kafka
	// clusters this command can't plan yet), so they inform but don't block.
	for _, w := range ep.Warnings {
		fmt.Println("warning: plan-inputs:", w)
	}

	writeMD, writeJSON, err := parseOutputFormats(output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create --output-dir %s: %w", outputDir, err)
	}

	if writeMD {
		md := plan.RenderEnginePlanMarkdown(ep)
		path := filepath.Join(outputDir, "plan.md")
		if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
			return fmt.Errorf("write plan.md: %w", err)
		}
		fmt.Println("wrote", path)
	}
	if writeJSON {
		js, err := plan.RenderEnginePlanJSON(ep)
		if err != nil {
			return err
		}
		path := filepath.Join(outputDir, "plan.json")
		if err := os.WriteFile(path, []byte(js), 0o644); err != nil {
			return fmt.Errorf("write plan.json: %w", err)
		}
		fmt.Println("wrote", path)
	}

	// Rewrite plan-inputs.yaml in place: the customer's current answers, the open
	// questions (optionals pre-filled), and the scan-detected facts, plus any
	// follow-ups this run revealed. Written to the same path it was read from
	// (--plan-inputs) so editing and re-running never loses answers — the write is
	// atomic (temp + rename) so an interrupted run can't leave a truncated file.
	if err := atomicWrite(planInputs, []byte(plan.RenderPlanInputsYAML(ep))); err != nil {
		return fmt.Errorf("write plan-inputs.yaml: %w", err)
	}
	fmt.Println("wrote", planInputs)

	printNextSteps(ep, planInputs)
	return nil
}

// printNextSteps prints the fleet summary and the exact command to re-run after
// editing plan-inputs.yaml, so the converge loop is obvious from the terminal.
func printNextSteps(ep *plan.EnginePlan, planInputsPath string) {
	s := ep.Summary
	fmt.Printf("\n%d migration plan(s): %d ready, %d need answers, %d need a specialist. %d required / %d optional question(s) open.\n",
		s.Clusters, s.Ready, s.NeedsAnswers, s.NeedsSpecialist, s.OpenRequired, s.OpenOptional)
	if s.OpenRequired > 0 || s.OpenOptional > 0 {
		fmt.Printf("Next: edit %s, then re-run:\n  kcp report plan", planInputsPath)
		// Echo --state-file only when one was given (omitted in questionnaire mode).
		if stateFile != "" {
			fmt.Printf(" --state-file %s", stateFile)
		}
		// Only echo --plan-inputs when it isn't the default (the plain command
		// already reads ./plan-inputs.yaml).
		if planInputsPath != "./plan-inputs.yaml" {
			fmt.Printf(" --plan-inputs %s", planInputsPath)
		}
		fmt.Println()
	} else {
		fmt.Println("All required questions are answered. Review plan-output/plan.md.")
	}
}

// atomicWrite writes data to a temp file in the destination's directory and
// renames it over the target, so a reader never sees a partial file and an
// interrupted write leaves the previous version intact.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".plan-inputs-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
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
