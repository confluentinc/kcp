// Package clusterlink implements the cluster-link Reconciler: it reconciles the
// desired link (derived from the live source + manifest) against the actual
// link on the target, additively (§8.2, §8.6).
package clusterlink

import (
	"context"
	"fmt"

	svclink "github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/reconcile"
)

// source is the subset of the live source this reconciler reads.
// *migrate.OSKSourceReader satisfies this interface.
type source interface {
	ClusterID(ctx context.Context) (string, error)
}

// target is the subset of targets.Target this reconciler uses.
type target interface {
	ClusterID(ctx context.Context) (string, error)
	GetClusterLink(ctx context.Context, name string) (*svclink.ClusterLink, error)
	CreateClusterLink(ctx context.Context, name string, req svclink.CreateClusterLinkRequest) error
}

// Config is the desired link, derived from the manifest + source credentials.
type Config struct {
	LinkName               string
	SourceBootstrapServers []string
	SecurityProtocol       string            // Phase 1: always "PLAINTEXT"
	SaslMechanism          string            // Phase 2+
	SaslJaasConfig         string            // Phase 2+
	Configs                map[string]string // spec.clusterLink.configs
}

// Reconciler reconciles the cluster link section of a migration manifest.
// Construct it with New; it implements reconcile.Reconciler.
type Reconciler struct {
	cfg Config
	src source
	tgt target
}

// New creates a Reconciler that will reconcile cfg against the live source and target.
func New(cfg Config, src source, tgt target) *Reconciler {
	return &Reconciler{cfg: cfg, src: src, tgt: tgt}
}

func (r *Reconciler) Name() string { return "clusterLink" }

// CheckPreconditions confirms both endpoints are reachable (source cluster id
// read, target cluster id discovery). Cluster link has no upstream manifest
// precondition — it is itself the precondition for mirror topics (§8.1).
func (r *Reconciler) CheckPreconditions(ctx context.Context) error {
	if _, err := r.tgt.ClusterID(ctx); err != nil {
		return fmt.Errorf("target not reachable: %w", err)
	}
	// NOTE: Plan reads the source cluster id again. For Phase 1's single
	// reconciler the duplicate live read is acceptable; if more reconcilers are
	// added, cache it in OSKSourceReader rather than re-probing here.
	if _, err := r.src.ClusterID(ctx); err != nil {
		return fmt.Errorf("source not reachable: %w", err)
	}
	return nil
}

// plan is the concrete reconcile.Plan carrying the typed create request.
type plan struct {
	change reconcile.Change
	req    *svclink.CreateClusterLinkRequest // non-nil only when Action==Create
}

func (p plan) Changes() []reconcile.Change { return []reconcile.Change{p.change} }

// Empty reports true when the plan creates nothing (Present and Drift are no-ops for Apply).
func (p plan) Empty() bool { return p.change.Action != reconcile.ActionCreate }

func (r *Reconciler) Plan(ctx context.Context) (reconcile.Plan, error) {
	sourceID, err := r.src.ClusterID(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading source cluster id: %w", err)
	}

	actual, err := r.tgt.GetClusterLink(ctx, r.cfg.LinkName)
	if err != nil {
		return nil, fmt.Errorf("reading target cluster link: %w", err)
	}

	summary := fmt.Sprintf("cluster link %q", r.cfg.LinkName)

	if actual == nil {
		return plan{
			change: reconcile.Change{Action: reconcile.ActionCreate, Summary: summary,
				Detail: fmt.Sprintf("source %s", sourceID)},
			req: &svclink.CreateClusterLinkRequest{
				SourceClusterID:        sourceID,
				SourceBootstrapServers: r.cfg.SourceBootstrapServers,
				SecurityProtocol:       r.cfg.SecurityProtocol,
				SaslMechanism:          r.cfg.SaslMechanism,
				SaslJaasConfig:         r.cfg.SaslJaasConfig,
				Configs:                r.cfg.Configs,
			},
		}, nil
	}

	if actual.SourceClusterID == "" || actual.SourceClusterID == sourceID {
		// Present: the link matches the desired source, OR the target did not
		// report a source id (older CP) so we cannot prove drift — treat as
		// present rather than fabricate drift (both are non-mutating anyway).
		return plan{change: reconcile.Change{Action: reconcile.ActionPresent, Summary: summary}}, nil
	}

	return plan{change: reconcile.Change{Action: reconcile.ActionDrift, Summary: summary,
		Detail: fmt.Sprintf("exists but points at source %s, manifest expects %s", actual.SourceClusterID, sourceID)}}, nil
}

func (r *Reconciler) Apply(ctx context.Context, p reconcile.Plan) (reconcile.Outcome, error) {
	cp, ok := p.(plan)
	if !ok {
		return reconcile.Outcome{}, fmt.Errorf("clusterlink: unexpected plan type %T", p)
	}

	switch cp.change.Action {
	case reconcile.ActionCreate:
		if err := r.tgt.CreateClusterLink(ctx, r.cfg.LinkName, *cp.req); err != nil {
			return reconcile.Outcome{}, fmt.Errorf("creating cluster link: %w", err)
		}
		return reconcile.Outcome{Created: []reconcile.Change{cp.change}}, nil
	case reconcile.ActionPresent:
		return reconcile.Outcome{Present: []reconcile.Change{cp.change}}, nil
	default: // ActionDrift — report only, never override (§8.6)
		return reconcile.Outcome{Drift: []reconcile.Change{cp.change}}, nil
	}
}
