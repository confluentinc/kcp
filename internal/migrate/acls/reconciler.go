// Package acls (this file) reconciles spec.acls: it creates every desired ACL
// on the Confluent Cloud target that is not already present, rewriting each
// ACL's source principal to the target identity resolved by the
// serviceAccounts reconciler first. This is the WRITE stage of ACL migration
// — it must run after normalization (normalize.go) and after serviceAccounts
// has populated its ResolvedMap. Additive — never alters or deletes.
package acls

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/confluentinc/kcp/internal/services/reconcile"
	"github.com/confluentinc/kcp/internal/types"
)

// Config configures the acls reconciler.
type Config struct {
	// Desired is the normalized ACL set to reconcile onto the target, with
	// principals still in SOURCE form (e.g. "User:app1").
	Desired []types.Acls
	// ResolvedPrincipals is a getter for the source-principal -> target
	// Confluent Cloud identity map (e.g. "User:app1" -> "User:sa-abc123") — the
	// serviceAccounts reconciler's ResolvedMap. It is called at the TOP of Plan
	// (not at construction) so it observes whatever the serviceAccounts
	// reconciler has resolved by then: real ids once its Apply has run (apply
	// mode), or its Plan-time placeholders (dry-run). A desired ACL whose
	// principal has no entry was warn-skipped upstream (e.g. "User:*" /
	// "User:ANONYMOUS") and is skipped here too: no create, no error.
	ResolvedPrincipals func() map[string]string
	// NumericToResourceID maps a Confluent Cloud service account's internal
	// NUMERIC id (as a string, e.g. "1267635") to its "sa-" resource id (e.g.
	// "sa-abc123"). It is used to NORMALIZE existing-ACL principals before
	// diffing: Confluent Cloud accepts an ACL created with principal
	// "User:sa-abc123" but returns it on read-back as the numeric id
	// ("User:1267635"), so without this rewrite the desired set (which uses
	// "User:sa-...") never matches an existing ACL and every ACL is re-created
	// on each apply (an idempotency/drift bug). Empty or nil disables
	// normalization (existing principals are diffed verbatim — the prior
	// behavior). A principal whose numeric id is absent from this map (another
	// org's service accounts/users) is left unchanged: this reconciler is
	// strictly additive and never touches ACLs it did not author.
	NumericToResourceID map[string]string
	// Client talks to the Confluent Cloud Kafka REST v3 ACL API, entirely in
	// canonical types.Acls form.
	Client ACLClient
}

// Reconciler implements reconcile.Reconciler for spec.acls.
type Reconciler struct {
	cfg Config
}

// New creates a Reconciler from cfg.
func New(cfg Config) *Reconciler {
	return &Reconciler{cfg: cfg}
}

func (r *Reconciler) Name() string { return "acls" }

// CheckPreconditions is a no-op: ACLClient is a thin REST wrapper with no
// dedicated reachability probe of its own — the first real call (List, made
// from Plan) surfaces any connectivity problem.
func (r *Reconciler) CheckPreconditions(ctx context.Context) error {
	return nil
}

type step = reconcile.Step[types.Acls]

// summaryFor renders the reporting line for one (already principal-rewritten)
// ACL.
func summaryFor(a types.Acls) string {
	return fmt.Sprintf("%s %s ACL for %s on %s %q", a.PermissionType, a.Operation, a.Principal, a.ResourceType, a.ResourceName)
}

// Plan resolves each desired ACL's target principal via PrincipalMap (skipping
// any whose principal was warn-skipped upstream), then diffs the rewritten
// desired set against every ACL currently on the target: an exact tuple match
// is ActionPresent, otherwise ActionCreate. Extra ACLs that exist on the
// target but are not in the desired set are never represented here — this
// reconciler is strictly additive and can only grow the target's ACL set.
func (r *Reconciler) Plan(ctx context.Context) (reconcile.Plan, error) {
	// Resolve principals lazily: in apply mode the serviceAccounts reconciler's
	// Apply has already populated real ids by now; in dry-run it has only
	// placeholders. Reading at the top of Plan (not at construction) is what
	// makes that late-binding work.
	principalMap := map[string]string{}
	if r.cfg.ResolvedPrincipals != nil {
		principalMap = r.cfg.ResolvedPrincipals()
	}

	existing, err := r.cfg.Client.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing target acls: %w", err)
	}
	existingSet := make(map[types.Acls]struct{}, len(existing))
	for _, a := range existing {
		a.Principal = r.normalizeExistingPrincipal(a.Principal)
		existingSet[a] = struct{}{}
	}

	steps := make([]step, 0, len(r.cfg.Desired))
	for _, a := range r.cfg.Desired {
		mapped, ok := principalMap[a.Principal]
		if !ok {
			slog.Debug("⏭️ skipping ACL: source principal has no resolved target identity", "principal", a.Principal)
			continue
		}
		a.Principal = mapped

		summary := summaryFor(a)
		if _, present := existingSet[a]; present {
			steps = append(steps, step{Change: reconcile.Change{Action: reconcile.ActionPresent, Summary: summary}})
			continue
		}
		steps = append(steps, step{
			Change:  reconcile.Change{Action: reconcile.ActionCreate, Summary: summary},
			Payload: a,
		})
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].Change.Summary < steps[j].Change.Summary })

	return reconcile.StepPlan[types.Acls]{Steps: steps}, nil
}

// normalizeExistingPrincipal rewrites a read-back "User:<numeric>" principal
// to "User:sa-..." when <numeric> is a known service-account numeric id (see
// Config.NumericToResourceID for why Confluent Cloud reports sa- principals as
// numeric ids). Any principal that is not of the "User:<numeric>" shape, or
// whose numeric id is absent from the map, is returned unchanged — the map is
// empty in dry-run and for non-Confluent-Cloud targets, in which case this is a
// no-op (prior behavior).
func (r *Reconciler) normalizeExistingPrincipal(principal string) string {
	if len(r.cfg.NumericToResourceID) == 0 {
		return principal
	}
	numeric, ok := strings.CutPrefix(principal, "User:")
	if !ok {
		return principal
	}
	if resourceID, ok := r.cfg.NumericToResourceID[numeric]; ok {
		return "User:" + resourceID
	}
	return principal
}

// Apply creates each missing ACL, continuing past per-ACL failures (collected
// in Outcome.Failed). Never deletes.
func (r *Reconciler) Apply(ctx context.Context, p reconcile.Plan) (reconcile.Outcome, error) {
	return reconcile.ApplyContinueOnError(ctx, p, "acl(s)", r.cfg.Client.Create)
}
