package cutover

import (
	"github.com/confluentinc/kcp/cmd/cutover/execute"
	i "github.com/confluentinc/kcp/cmd/cutover/init"
	"github.com/confluentinc/kcp/cmd/cutover/lag"
	"github.com/confluentinc/kcp/cmd/cutover/list"

	"github.com/spf13/cobra"
)

func NewCutoverCmd() *cobra.Command {
	cutoverCmd := &cobra.Command{
		Use:   "cutover",
		Short: "Commands for gateway cutover using CPC Gateway.",
		Long: `Execute the gateway cutover phase for Confluent Cloud using the Confluent Platform Connect (CPC) Gateway.

The cutover workflow follows a defined lifecycle managed by a finite state machine:

1. **Initialize** — validate cluster link and gateway CRs, persist cutover config (` + "`kcp cutover init`" + `).
2. **Check Lags** — compare source and destination offsets until lag drops below the configured threshold.
3. **Fence Gateway** — apply the fenced gateway CR to block traffic during cutover.
4. **Promote Topics** — promote mirror topics at zero lag.
5. **Switch Gateway** — apply the switchover gateway CR to route traffic to Confluent Cloud.

If execution is interrupted at any step, re-running ` + "`kcp cutover execute`" + ` resumes from the last completed step.

Supporting documentation:

- [Gateway Switchover Examples](../../gateway-switchover/index.md) — Gateway CR YAML templates for each supported authentication combination.
- [Getting Started with Zero-Cut](../../getting-started-with-zero-cut-migrations.md) — end-to-end reference for the KCP + Gateway approach, including networking and auth pre-requisites.`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	cutoverCmd.AddCommand(
		i.NewCutoverInitCmd(),
		execute.NewCutoverExecuteCmd(),
		lag.NewCutoverLagCmd(),
		list.NewCutoverListCmd(),
	)

	return cutoverCmd
}
