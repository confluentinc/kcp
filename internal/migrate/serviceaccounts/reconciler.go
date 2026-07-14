// Package serviceaccounts (this file) reconciles spec.serviceAccounts: for
// every distinct source principal referenced by the surviving ACLs, it
// resolves a target Confluent Cloud identity id — via an explicit mapping
// override, an auto-created service account, or (unmappable principals)
// a warned skip. This is the PROVISION stage of ACL migration; it must run,
// and succeed, before the acls reconciler, which needs every principal
// already resolved to a CC identity id.
package serviceaccounts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/confluentinc/kcp/internal/services/reconcile"
)

// Config configures the serviceAccounts reconciler.
type Config struct {
	// AutoCreate, when true, finds-or-creates a service account (named via
	// DeriveDisplayName) for every principal that has no Mapping override and
	// isn't a warn-and-skip principal (User:* / User:ANONYMOUS).
	AutoCreate bool
	// Mapping overrides principal resolution: source principal -> a
	// pre-existing CC identity id (the bare "sa-"/"u-"/"pool-" id, without a
	// "User:" prefix). A present-but-empty value is treated as unmapped —
	// this lets a manifest record "no mapping" for a principal explicitly
	// (e.g. documenting that ANONYMOUS was deliberately left to warn-and-skip)
	// without the value being mistaken for a real override.
	Mapping map[string]string
	// Principals is the set of distinct source principals referenced by the
	// surviving (filtered, normalized) ACLs.
	Principals []string
	// Client talks to the Confluent Cloud IAM v2 service-accounts API.
	Client CCClient
}

// Reconciler implements reconcile.Reconciler for spec.serviceAccounts.
type Reconciler struct {
	cfg      Config
	resolved map[string]string
}

// New creates a Reconciler from cfg.
func New(cfg Config) *Reconciler {
	return &Reconciler{cfg: cfg}
}

func (r *Reconciler) Name() string { return "serviceAccounts" }

// CheckPreconditions is a no-op: CCClient (Task 4) is a thin REST wrapper
// with no dedicated reachability probe of its own — the first real call
// (FindByDisplayName, made from Plan) surfaces any connectivity problem.
func (r *Reconciler) CheckPreconditions(ctx context.Context) error {
	return nil
}

// ResolvedMap returns the live source-principal -> "User:<id>" map. Plan
// populates it (real ids for mapped/existing principals, placeholders for
// to-be-created ones); Apply overwrites the placeholders with real ids. It is
// safe to pass as the acls reconciler's ResolvedPrincipals getter: the
// returned map reflects whatever has resolved by the time it is called. Before
// Plan runs it is empty (but non-nil).
func (r *Reconciler) ResolvedMap() map[string]string {
	if r.resolved == nil {
		return map[string]string{}
	}
	return r.resolved
}

// provision is the payload for one principal's plan step. displayName and
// description are set only for ActionCreate steps (findOrCreate needs them).
// resolvedID is already known for mapped and found-existing principals — it
// lets Apply populate ResolvedMap for those without any further client call;
// it is empty for ActionCreate steps, filled in once the create completes.
type provision struct {
	principal   string
	displayName string
	description string
	resolvedID  string
}

// descriptionFor returns the audit/collision-check description recorded on
// every auto-created service account (design §6).
func descriptionFor(principal string) string {
	return "kcp:source-principal=" + principal
}

// placeholderFor is the resolved-map value recorded at Plan time for a
// principal whose service account would be CREATED (its real id is not known
// until Apply runs). It lets the acls reconciler produce a meaningful dry-run
// preview ("would create ACL for <principal>") instead of silently skipping
// the principal; Apply overwrites it with the real "User:sa-<id>".
func placeholderFor(displayName string) string {
	return "User:sa-(pending:" + displayName + ")"
}

// Plan resolves every principal in cfg.Principals to a CC identity, in one of
// four ways (design §6):
//   - an explicit Mapping entry wins, used verbatim, no client call;
//   - "User:*" / "User:ANONYMOUS" (unless mapped) warn-and-skip — no step;
//   - AutoCreate finds-or-creates a service account named
//     DeriveDisplayName(principal); a found account's description must match
//     the expected "kcp:source-principal=<principal>" or it's a naming
//     collision (hard error);
//   - otherwise (no mapping, AutoCreate disabled) it's a config gap
//     (hard error listing every such principal).
func (r *Reconciler) Plan(ctx context.Context) (reconcile.Plan, error) {
	principals := append([]string(nil), r.cfg.Principals...)
	sort.Strings(principals)

	// Populate the resolved map during Plan (as well as Apply): mapped and
	// already-existing principals get their real "User:sa-<id>"; principals
	// that would be auto-created get a deterministic placeholder (overwritten
	// with the real id by Apply). Skipped principals (User:* / User:ANONYMOUS)
	// stay OUT of the map. This lets the acls reconciler's Plan see meaningful
	// entries in dry-run, where Apply never runs.
	r.resolved = map[string]string{}

	var steps []reconcile.Step[provision]
	var errs []error

	for _, p := range principals {
		summary := fmt.Sprintf("service account for principal %q", p)

		if id, ok := r.cfg.Mapping[p]; ok && id != "" {
			r.resolved[p] = "User:" + id
			steps = append(steps, reconcile.Step[provision]{
				Change: reconcile.Change{Action: reconcile.ActionPresent, Summary: summary,
					Detail: fmt.Sprintf("mapped to User:%s", id)},
				Payload: provision{principal: p, resolvedID: id},
			})
			continue
		}

		if p == "User:*" || p == "User:ANONYMOUS" {
			slog.Warn("⚠️ source principal has no Confluent Cloud equivalent; its ACLs will be skipped", "principal", p)
			continue
		}

		if !r.cfg.AutoCreate {
			errs = append(errs, fmt.Errorf("no service-account mapping for principal %q", p))
			continue
		}

		displayName := DeriveDisplayName(p)
		description := descriptionFor(p)
		found, err := r.cfg.Client.FindByDisplayName(ctx, displayName)
		if err != nil {
			errs = append(errs, fmt.Errorf("looking up service account %q for principal %q: %w", displayName, p, err))
			continue
		}
		if found == nil {
			r.resolved[p] = placeholderFor(displayName)
			steps = append(steps, reconcile.Step[provision]{
				Change: reconcile.Change{Action: reconcile.ActionCreate, Summary: summary,
					Detail: fmt.Sprintf("display name %q", displayName)},
				Payload: provision{principal: p, displayName: displayName, description: description},
			})
			continue
		}
		if found.Description != description {
			// Collision backstop (design §6): a service account already owns this
			// derived display name, but for a DIFFERENT source principal (a freak
			// same-base+same-hash8 collision, or a name that pre-existed for an
			// unrelated reason). Reusing it would silently misattribute ACLs, so
			// this must fail loud rather than proceed.
			errs = append(errs, fmt.Errorf(
				"service account %q already exists but its description %q does not match the expected %q for principal %q (naming collision)",
				displayName, found.Description, description, p))
			continue
		}
		r.resolved[p] = "User:" + found.ID
		steps = append(steps, reconcile.Step[provision]{
			Change: reconcile.Change{Action: reconcile.ActionPresent, Summary: summary,
				Detail: fmt.Sprintf("existing service account %s", found.ID)},
			Payload: provision{principal: p, displayName: displayName, description: description, resolvedID: found.ID},
		})
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	// steps are already in Summary order: principals is sorted above, each
	// step's Summary embeds its principal verbatim, and Principals holds
	// distinct values, so no re-sort is needed here.
	return reconcile.StepPlan[provision]{Steps: steps}, nil
}

// Apply creates each planned service account, continuing past per-principal
// failures (collected in Outcome.Failed), and populates ResolvedMap for every
// principal in the plan: mapped and already-existing principals resolve
// immediately from the plan's payload; newly created ones resolve as each
// create completes.
func (r *Reconciler) Apply(ctx context.Context, p reconcile.Plan) (reconcile.Outcome, error) {
	r.resolved = map[string]string{}

	sp, ok := p.(reconcile.StepPlan[provision])
	if !ok {
		return reconcile.Outcome{}, fmt.Errorf("unexpected plan type %T", p)
	}
	for _, s := range sp.Steps {
		if s.Payload.resolvedID != "" {
			r.resolved[s.Payload.principal] = "User:" + s.Payload.resolvedID
		}
	}

	return reconcile.ApplyContinueOnError(ctx, p, "service account(s)", r.findOrCreate)
}

// findOrCreate creates the service account for pr and records the resulting
// id in ResolvedMap. It is the create func handed to
// reconcile.ApplyContinueOnError, which only invokes it for ActionCreate
// steps — principals already resolved in Plan (mapped or pre-existing) never
// reach here. cfg.Client.Create tolerates a concurrent 409 (falls back to the
// existing account), so a create that lost a race against another run still
// resolves correctly — but that fallback can just as easily return a
// DIFFERENT principal's existing account (a same-run name collision that
// Plan's FindByDisplayName check never saw, since the account didn't exist
// yet at plan time). The description check below re-applies the same
// collision backstop as Plan (§6) to that returned account, so the hole
// can't silently misattribute one principal's ACLs to another's account.
func (r *Reconciler) findOrCreate(ctx context.Context, pr provision) error {
	sa, err := r.cfg.Client.Create(ctx, pr.displayName, pr.description)
	if err != nil {
		return fmt.Errorf("creating service account %q for principal %q: %w", pr.displayName, pr.principal, err)
	}
	if sa.Description != pr.description {
		return fmt.Errorf(
			"service-account name collision: %q resolved to an account described %q, expected %q for principal %q",
			pr.displayName, sa.Description, pr.description, pr.principal)
	}
	r.resolved[pr.principal] = "User:" + sa.ID
	return nil
}
