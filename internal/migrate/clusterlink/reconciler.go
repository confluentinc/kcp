// Package clusterlink implements the cluster-link Reconciler: it reconciles the
// desired link (derived from the live source + manifest) against the actual
// link on the target, additively (§8.2, §8.6).
//
// Two link topologies are supported:
//
//   - Destination-initiated (default, Mode==""): ONE link object on the
//     destination's REST with link.mode=DESTINATION (no connection.mode),
//     carrying the source address + link→source auth + source_cluster_id.
//
//   - Source-initiated (Mode==ClusterLinkModeSource): TWO link objects sharing
//     one link name. The DESTINATION-side object is created first
//     (link.mode=DESTINATION, connection.mode=INBOUND, source_cluster_id set, no
//     bootstrap/auth). The SOURCE-side object is created second on the source's
//     REST (link.mode=SOURCE, connection.mode=OUTBOUND, carrying the
//     destination address + source→destination connection auth, no
//     source_cluster_id). Healthy state is ACTIVE on both sides.
package clusterlink

import (
	"context"
	"fmt"

	svclink "github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/reconcile"
)

// ModeSource is the Config.Mode value selecting the source-initiated topology.
// Any other value (including "") selects destination-initiated.
const ModeSource = "source"

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

// Config is the desired link, derived from the manifest + credentials.
type Config struct {
	LinkName string
	// Mode selects the link topology: "" / anything-but-"source" is
	// destination-initiated; ModeSource is source-initiated.
	Mode string

	// --- destination-initiated and SOURCE-side-of-source-initiated share these
	// for the link→source connection (destination mode) ---
	SourceBootstrapServers []string
	Auth                   LinkAuth          // link→source auth (from LinkAuthFromSource)
	Configs                map[string]string // spec.clusterLink.configs

	// --- source-initiated only: the SOURCE-side link's connection back to the
	// destination ---
	DestBootstrapServers []string // destination address the source link dials
	DestAuth             LinkAuth // source→destination auth (from destinationCredentials)
}

func (c Config) sourceInitiated() bool { return c.Mode == ModeSource }

// Reconciler reconciles the cluster link section of a migration manifest.
// Construct it with New (destination) or NewSourceInitiated (source); it
// implements reconcile.Reconciler.
type Reconciler struct {
	cfg Config
	src source
	tgt target // destination REST in both modes
	// srcLinkTgt is the source cluster's REST endpoint, used only in
	// source-initiated mode to create/read the SOURCE-side link object.
	srcLinkTgt target
}

// New creates a destination-initiated Reconciler (one link on tgt).
func New(cfg Config, src source, tgt target) *Reconciler {
	return &Reconciler{cfg: cfg, src: src, tgt: tgt}
}

// NewSourceInitiated creates a source-initiated Reconciler. destTgt is the
// destination REST (DESTINATION-side link, created first); srcLinkTgt is the
// source REST (SOURCE-side link, created second). cfg.Mode must be ModeSource.
func NewSourceInitiated(cfg Config, src source, destTgt, srcLinkTgt target) *Reconciler {
	return &Reconciler{cfg: cfg, src: src, tgt: destTgt, srcLinkTgt: srcLinkTgt}
}

func (r *Reconciler) Name() string { return "clusterLink" }

// CheckPreconditions confirms the endpoints are reachable (target + source
// cluster id reads). In source-initiated mode the source REST is also probed.
// Cluster link has no upstream manifest precondition — it is itself the
// precondition for mirror topics (§8.1).
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
	if r.cfg.sourceInitiated() {
		if r.srcLinkTgt == nil {
			return fmt.Errorf("source-initiated mode requires a source REST target")
		}
		if _, err := r.srcLinkTgt.ClusterID(ctx); err != nil {
			return fmt.Errorf("source REST not reachable: %w", err)
		}
	}
	return nil
}

// step is one ordered create-or-noop in a plan, bound to the client that owns it.
type step struct {
	change   reconcile.Change
	req      *svclink.CreateClusterLinkRequest // non-nil only when change.Action==Create
	onSource bool                              // true → apply via srcLinkTgt; false → via tgt
}

// plan is the concrete reconcile.Plan carrying an ordered list of steps
// (1 for destination mode, up to 2 for source-initiated mode).
type plan struct {
	steps []step
}

func (p plan) Changes() []reconcile.Change {
	out := make([]reconcile.Change, len(p.steps))
	for i, s := range p.steps {
		out[i] = s.change
	}
	return out
}

// Empty reports true when no step is a create (Present and Drift are no-ops for Apply).
func (p plan) Empty() bool {
	for _, s := range p.steps {
		if s.change.Action == reconcile.ActionCreate {
			return false
		}
	}
	return true
}

func (r *Reconciler) Plan(ctx context.Context) (reconcile.Plan, error) {
	sourceID, err := r.src.ClusterID(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading source cluster id: %w", err)
	}
	if r.cfg.sourceInitiated() {
		return r.planSourceInitiated(ctx, sourceID)
	}
	return r.planDestination(ctx, sourceID)
}

// planDestination builds the single-link destination-initiated plan (unchanged
// behaviour: Create when absent, Present when matching/unknown source, Drift
// when the link points at a different source).
func (r *Reconciler) planDestination(ctx context.Context, sourceID string) (reconcile.Plan, error) {
	actual, err := r.tgt.GetClusterLink(ctx, r.cfg.LinkName)
	if err != nil {
		return nil, fmt.Errorf("reading target cluster link: %w", err)
	}

	summary := fmt.Sprintf("cluster link %q", r.cfg.LinkName)

	if actual == nil {
		req, err := r.destinationLinkRequest(sourceID)
		if err != nil {
			return nil, err
		}
		return plan{steps: []step{{
			change: reconcile.Change{Action: reconcile.ActionCreate, Summary: summary,
				Detail: fmt.Sprintf("source %s", sourceID)},
			req: req,
		}}}, nil
	}

	if actual.SourceClusterID == "" || actual.SourceClusterID == sourceID {
		// Present: the link matches the desired source, OR the target did not
		// report a source id (older CP) so we cannot prove drift — treat as
		// present rather than fabricate drift (both are non-mutating anyway).
		return plan{steps: []step{{change: reconcile.Change{Action: reconcile.ActionPresent, Summary: summary}}}}, nil
	}

	return plan{steps: []step{{change: reconcile.Change{Action: reconcile.ActionDrift, Summary: summary,
		Detail: fmt.Sprintf("exists but points at source %s, manifest expects %s", actual.SourceClusterID, sourceID)}}}}, nil
}

// planSourceInitiated builds the two-link plan: read BOTH sides and create
// whichever is absent, destination-first. A present side maps to Present; this
// mode does not synthesise drift (both-present → all Present).
func (r *Reconciler) planSourceInitiated(ctx context.Context, sourceID string) (reconcile.Plan, error) {
	destActual, err := r.tgt.GetClusterLink(ctx, r.cfg.LinkName)
	if err != nil {
		return nil, fmt.Errorf("reading destination cluster link: %w", err)
	}
	srcActual, err := r.srcLinkTgt.GetClusterLink(ctx, r.cfg.LinkName)
	if err != nil {
		return nil, fmt.Errorf("reading source cluster link: %w", err)
	}

	destSummary := fmt.Sprintf("cluster link %q (destination side)", r.cfg.LinkName)
	srcSummary := fmt.Sprintf("cluster link %q (source side)", r.cfg.LinkName)

	steps := make([]step, 0, 2)

	// Destination side first.
	if destActual == nil {
		steps = append(steps, step{
			change: reconcile.Change{Action: reconcile.ActionCreate, Summary: destSummary,
				Detail: fmt.Sprintf("INBOUND, source %s", sourceID)},
			req: &svclink.CreateClusterLinkRequest{
				LinkMode:        "DESTINATION",
				ConnectionMode:  "INBOUND",
				SourceClusterID: sourceID,
			},
			onSource: false,
		})
	} else {
		steps = append(steps, step{change: reconcile.Change{Action: reconcile.ActionPresent, Summary: destSummary}})
	}

	// Source side second.
	if srcActual == nil {
		tls, err := r.cfg.DestAuth.LoadTLS()
		if err != nil {
			return nil, fmt.Errorf("loading source→destination TLS material: %w", err)
		}
		steps = append(steps, step{
			change: reconcile.Change{Action: reconcile.ActionCreate, Summary: srcSummary,
				Detail: "OUTBOUND to destination"},
			req: &svclink.CreateClusterLinkRequest{
				LinkMode:               "SOURCE",
				ConnectionMode:         "OUTBOUND",
				SourceBootstrapServers: r.cfg.DestBootstrapServers,
				SecurityProtocol:       r.cfg.DestAuth.SecurityProtocol,
				SaslMechanism:          r.cfg.DestAuth.SaslMechanism,
				SaslJaasConfig:         r.cfg.DestAuth.SaslJaasConfig,
				SourceTLS:              tls,
				Configs:                r.cfg.Configs,
			},
			onSource: true,
		})
	} else {
		steps = append(steps, step{change: reconcile.Change{Action: reconcile.ActionPresent, Summary: srcSummary}})
	}

	return plan{steps: steps}, nil
}

// destinationLinkRequest builds the create request for a destination-initiated
// link (carries source address + link→source auth + source_cluster_id).
func (r *Reconciler) destinationLinkRequest(sourceID string) (*svclink.CreateClusterLinkRequest, error) {
	tls, err := r.cfg.Auth.LoadTLS()
	if err != nil {
		return nil, fmt.Errorf("loading source TLS material: %w", err)
	}
	return &svclink.CreateClusterLinkRequest{
		SourceClusterID:        sourceID,
		SourceBootstrapServers: r.cfg.SourceBootstrapServers,
		SecurityProtocol:       r.cfg.Auth.SecurityProtocol,
		SaslMechanism:          r.cfg.Auth.SaslMechanism,
		SaslJaasConfig:         r.cfg.Auth.SaslJaasConfig,
		SourceTLS:              tls,
		Configs:                r.cfg.Configs,
	}, nil
}

func (r *Reconciler) Apply(ctx context.Context, p reconcile.Plan) (reconcile.Outcome, error) {
	cp, ok := p.(plan)
	if !ok {
		return reconcile.Outcome{}, fmt.Errorf("clusterlink: unexpected plan type %T", p)
	}

	var out reconcile.Outcome
	for _, s := range cp.steps {
		switch s.change.Action {
		case reconcile.ActionCreate:
			tgt := r.tgt
			if s.onSource {
				tgt = r.srcLinkTgt
			}
			if err := tgt.CreateClusterLink(ctx, r.cfg.LinkName, *s.req); err != nil {
				// Return what we created before the failure so the caller can see
				// partial progress; the engine surfaces the error.
				return out, fmt.Errorf("creating cluster link: %w", err)
			}
			out.Created = append(out.Created, s.change)
		case reconcile.ActionPresent:
			out.Present = append(out.Present, s.change)
		default: // ActionDrift — report only, never override (§8.6)
			out.Drift = append(out.Drift, s.change)
		}
	}
	return out, nil
}
