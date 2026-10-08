package d2s

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/confluentinc/kcp/internal/services/groupoffsets"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/types"
)

// ErrVerifyRefused is returned by VerifyFence when a conversion check refuses on the facts gathered after the
// fence. The refusal itself is printed with the reconcile report renderer, as --dry-run prints it.
var ErrVerifyRefused = errors.New("the conversion's checks refused after the fence")

// VerifyFence runs the verify_fence transition, the conversion's authoritative safety check (spec decision
// 7). It runs after the fence is confirmed on every gateway pod and is never skipped as a no-op. Nothing about
// groups or offsets carries over from reconcile; every fact is read again, through the same gather-only
// function reconcile uses. In order:
//
//  1. gather the facts (migplan.GatherConvertFacts) and re-run the route preconditions (for the route view)
//     and the five credential checks; a refusal here comes before snapshot 1 reads listings a denied probe
//     says may be partial;
//  2. snapshot 1: a fresh strict listing of every source group, then each group's committed offsets;
//  3. the link and group checks (reconcile.CheckLinkScope) on the gathered facts with snapshot 1's commit
//     topics, including a first split-brain check on the gathered destination listing; the in-scope groups
//     form G;
//  4. to 6. wait the detection window, list the source groups again and take snapshot 2, and compare
//     (groupoffsets.DetectRogueCommits): any addition, change or removal is groupoffsets.ErrRogueCommits;
//  7. split-brain again, on a fresh destination listing (decision 23);
//  8. hand snapshot 2, restricted to G, to sync_offsets in memory.
//
// A refusal is rendered with migplan's report renderer and returned as ErrVerifyRefused (decision 24), with
// every failed check in the error so it reaches kcp.log. A read failure is a plain error, never a refusal.
func (a *D2SActions) VerifyFence(ctx context.Context, config *migration.MigrationConfig, p Policy) error {
	a.handoff = nil
	if p.DetectUnroutedCommitsDuration <= 0 {
		return fmt.Errorf("verify_fence needs a detection window above zero, got %s; the direct-commit check can never be skipped", p.DetectUnroutedCommitsDuration)
	}

	// 1. Gather and probe.
	facts, err := a.deps.Gather(ctx)
	if err != nil {
		return fmt.Errorf("gathering the conversion's facts after the fence: %w", err)
	}
	if facts == nil || facts.Link == nil {
		return errors.New("gathering the conversion's facts after the fence: the gatherer returned no cluster-link status")
	}
	var report reconcile.Report
	routeChecks, view, ok := reconcile.CheckPreconditions(a.deps.Input, facts.Gateway, facts.Link.OffsetSyncEnabled, facts.IDs)
	report.Preconditions = routeChecks
	if !ok {
		return a.refuse(config, report)
	}
	report.Preconditions = append(report.Preconditions, convertFenceCheck(facts.Gateway))
	if report.Refused() {
		return a.refuse(config, report)
	}
	report.Preconditions = append(report.Preconditions, reconcile.ConvertCredentialChecks(facts.GroupFacts(nil))...)
	if report.Refused() {
		return a.refuse(config, report)
	}
	a.reporter.Success("Route and credentials re-checked after the fence")

	// 2. Snapshot 1.
	groups, err := a.deps.SourceGroups(ctx)
	if err != nil {
		return fmt.Errorf("listing source consumer groups for the first snapshot: %w", err)
	}
	first, err := groupoffsets.TakeSnapshot(ctx, a.deps.Fetchers, groups, p.Workers)
	if err != nil {
		return fmt.Errorf("taking the first committed-offset snapshot: %w", err)
	}
	a.reporter.Detail("First snapshot: %d of %d source consumer group(s) have committed offsets", len(first), len(groups))

	// 3. The link and group checks. The untracked-topic warning (scope.UntrackedTopicsWarning) is reconcile's
	// alone and is not repeated here.
	gf := facts.GroupFacts(committedTopics(first))
	scope := reconcile.CheckLinkScope(reconcile.LinkScopeInput{
		Mirrors:         facts.Link.LinkMirrors,
		SourceTopics:    facts.SourceTopics,
		TargetTopics:    facts.TargetTopics,
		Partitions:      facts.Partitions,
		View:            view,
		CommittedTopics: gf.TrackedTopics,
		TargetStates:    gf.TargetStates,
	})
	report.Unchanged = scope.Unchanged
	report.FailFast = scope.FailFast
	report.Preconditions = append(report.Preconditions, scope.Preconditions...)
	report.Warnings = append(report.Warnings, scope.Warnings...)
	if scope.Refused() {
		return a.refuse(config, report)
	}
	inScope := scope.InScopeGroups
	a.reporter.Success("Link topics and consumer groups re-checked: %d link topic(s), %d in-scope consumer group(s)", len(facts.Link.LinkMirrors), len(inScope))

	// 4-6. Wait, snapshot 2 from a fresh listing, and the direct-commit diff. ErrRogueCommits is returned as
	// is; so is a failure to wait, list or fetch, which is not evidence of a commit.
	a.reporter.Detail("Watching committed offsets for %s, for a client committing to the source directly...", p.DetectUnroutedCommitsDuration)
	second, err := groupoffsets.DetectRogueCommits(ctx, first, a.deps.SourceGroups, a.deps.Fetchers, p.Workers, p.DetectUnroutedCommitsDuration, a.deps.Wait)
	if err != nil {
		return err
	}
	a.reporter.Success("Committed offsets held still while fenced — no direct commits detected")

	// 7. Split-brain again, after the wait. Refusal only; the idle-group warning is not repeated.
	listing, err := a.deps.DestinationGroups(ctx)
	if err != nil {
		return fmt.Errorf("listing destination consumer groups after the wait: %w", err)
	}
	splitBrain, _ := reconcile.CheckGroupSplitBrain(inScope, groupStates(listing))
	if !splitBrain.OK {
		report.Preconditions = replacePrecondition(report.Preconditions, splitBrain)
		return a.refuse(config, report)
	}

	// 8. Hand-off. Never nil after a pass, even with no in-scope group: sync_offsets then writes nothing. A pass
	// renders no report, so the link scope's warnings (idle destination groups whose offsets sync will
	// overwrite) are printed here.
	a.handoff = restrictTo(second, inScope)
	for _, wn := range scope.Warnings {
		a.reporter.warn("%s", wn)
	}
	a.reporter.Success("Fence verified — %d consumer group(s) to sync", len(a.handoff))
	return nil
}

// convertFenceCheckName names verify_fence's check that kcp's conversion fence is still on the route.
const convertFenceCheckName = "route carries kcp's conversion fence"

// convertFenceCheck refuses a route whose rules no longer carry kcp's convert fence: verify_fence watches
// for direct commits on the assumption that the fence is up, so a fence removed or reverted after the fence
// step voids every check that follows. gw has passed CheckPreconditions, so its route is set.
func convertFenceCheck(gw *reconcile.GatewayConfig) reconcile.PreconditionResult {
	rules, err := reconcile.ParseRules(gw.Route.Rules)
	if err == nil && rules.HasConvertFence() {
		return reconcile.PreconditionResult{Name: convertFenceCheckName, OK: true}
	}
	return reconcile.PreconditionResult{Name: convertFenceCheckName, Detail: "the route no longer carries kcp's conversion fence — it was removed or reverted after the fence step"}
}

// refuse renders report as --dry-run would and returns ErrVerifyRefused carrying every failed check.
func (a *D2SActions) refuse(config *migration.MigrationConfig, report reconcile.Report) error {
	migplan.RenderReport(a.deps.Out, report, migplan.RenderView{Route: config.Route, TargetDomain: a.deps.Input.TargetDomain})
	var reasons []string
	for _, pc := range report.Preconditions {
		if !pc.OK {
			reasons = append(reasons, pc.Name+": "+pc.Detail)
		}
	}
	for _, tv := range report.FailFast {
		reasons = append(reasons, tv.Topic+": "+tv.Reason)
	}
	return fmt.Errorf("%w:\n%s", ErrVerifyRefused, strings.Join(reasons, "\n"))
}

// committedTopics is, per group in snap, the sorted topics it has committed on: the group rule's input.
func committedTopics(snap groupoffsets.Snapshot) map[string][]string {
	out := make(map[string][]string, len(snap))
	for group, topics := range snap {
		names := make([]string, 0, len(topics))
		for t := range topics {
			names = append(names, t)
		}
		sort.Strings(names)
		out[group] = names
	}
	return out
}

// groupStates is each listed group's state.
func groupStates(listing []types.ConsumerGroupListing) map[string]string {
	out := make(map[string]string, len(listing))
	for _, l := range listing {
		out[l.GroupID] = l.State
	}
	return out
}

// restrictTo is snap with only groups' entries. The result is never nil.
func restrictTo(snap groupoffsets.Snapshot, groups []string) groupoffsets.Snapshot {
	out := groupoffsets.Snapshot{}
	for _, g := range groups {
		if offsets, ok := snap[g]; ok {
			out[g] = offsets
		}
	}
	return out
}

// replacePrecondition puts pc in place of the precondition with the same name, or appends it.
func replacePrecondition(pcs []reconcile.PreconditionResult, pc reconcile.PreconditionResult) []reconcile.PreconditionResult {
	out := make([]reconcile.PreconditionResult, 0, len(pcs)+1)
	replaced := false
	for _, p := range pcs {
		if p.Name == pc.Name {
			out = append(out, pc)
			replaced = true
			continue
		}
		out = append(out, p)
	}
	if !replaced {
		out = append(out, pc)
	}
	return out
}
