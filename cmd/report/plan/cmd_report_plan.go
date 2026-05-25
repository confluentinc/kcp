package plan

import (
	"encoding/json"
	"fmt"
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
	stateFile  string
	planInputs string
	outputDir  string
	output     string
	configPath string
)

func NewReportPlanCmd() *cobra.Command {
	reportPlanCmd := &cobra.Command{
		Use:   "plan",
		Short: "Generate a deterministic Migration Plan from a kcp state file",
		Long: "Generate a deterministic Migration Plan from a kcp-state.json produced by `kcp discover` and `kcp scan ...`. " +
			"The same state file + same plan-inputs + same KCP version produce a byte-identical plan (modulo the `generated_at` timestamp), so the output is auditable.\n\n" +
			"**Output:** writes `plan.md` and/or `plan.json` to `--output-dir` (default `./plan-output`).",
		Example: `  # Minimal: state file in, plan.md/plan.json out
  kcp report plan --state-file kcp-state.json

  # With customer overrides
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
	requiredFlags.StringVar(&stateFile, "state-file", "", "Path to the kcp state file written by `kcp discover` + `kcp scan ...`.")
	reportPlanCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&planInputs, "plan-inputs", "", "Path to plan-inputs.yaml with per-customer overrides. All fields optional.")
	optionalFlags.StringVar(&outputDir, "output-dir", "./plan-output", "Directory to write plan.md / plan.json into.")
	optionalFlags.StringVar(&output, "output", "md,json", "Comma-separated output formats: md, json, or both.")
	optionalFlags.StringVar(&configPath, "config", "", "Path to a plan-config.yaml override. Embedded config is the default.")
	reportPlanCmd.Flags().AddFlagSet(optionalFlags)
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
		fmt.Println("wrote", path)
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
		fmt.Println("wrote", path)
	}
	return nil
}

// loadState reads the state file and falls back to a pre-0.7 layout —
// `regions` at the top level instead of `msk_sources.regions` — when the
// modern unmarshal produces a state with no source data. The fallback uses
// a typed decode (not a map round-trip), so byte ordering of the original
// state file does not influence the resulting `*types.State`.
func loadState(path string) (*types.State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	state, err := types.NewStateFromBytes(data)
	if err != nil {
		return nil, err
	}
	if state.MSKSources != nil || state.OSKSources != nil {
		return state, nil
	}
	// Legacy MSK-only shape: try `{ "regions": [...] }`.
	var legacy struct {
		Regions []types.DiscoveredRegion `json:"regions"`
	}
	if jerr := json.Unmarshal(data, &legacy); jerr == nil && len(legacy.Regions) > 0 {
		state.MSKSources = &types.MSKSourcesState{Regions: legacy.Regions}
	}
	return state, nil
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
