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
	"sort"
	"strings"
	"time"

	svclink "github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/reconcile"
)

// ModeSource is the Config.Mode value selecting the source-initiated topology.
// Any other value (including "") selects destination-initiated.
const ModeSource = "source"

// source is the subset of the live source this reconciler reads.
// *migrate.KafkaSourceReader satisfies this interface.
type source interface {
	ClusterID(ctx context.Context) (string, error)
}

// target is the subset of targets.Target this reconciler uses.
type target interface {
	ClusterID(ctx context.Context) (string, error)
	GetClusterLink(ctx context.Context, name string) (*svclink.ClusterLink, error)
	CreateClusterLink(ctx context.Context, name string, req svclink.CreateClusterLinkRequest) error
	GetClusterLinkConfigs(ctx context.Context, name string) (map[string]string, error)
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
	// This probes source reachability; Plan reads the source cluster id again,
	// but KafkaSourceReader memoizes it, so the second read reuses this
	// connection's result rather than opening another admin connection.
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

// linkPayload is the create-time data for one link step: the request plus which
// side owns it (source REST vs destination). Meaningful only for ActionCreate.
type linkPayload struct {
	req      *svclink.CreateClusterLinkRequest // non-nil only when Action==Create
	onSource bool                              // true → apply via srcLinkTgt; false → via tgt
}

// step / plan reuse the generic reconcile types so Changes()/Empty() are shared,
// not re-implemented here. A plan carries 1 step (destination mode) or up to 2
// (source-initiated). Apply is bespoke (fail-fast + per-side routing), so it is
// implemented on the Reconciler rather than via reconcile.ApplyContinueOnError.
type (
	step = reconcile.Step[linkPayload]
	plan = reconcile.StepPlan[linkPayload]
)

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
		return plan{Steps: []step{{
			Change: reconcile.Change{Action: reconcile.ActionCreate, Summary: summary,
				Detail: fmt.Sprintf("source %s", sourceID)},
			Payload: linkPayload{req: req},
		}}}, nil
	}

	// A link can exist but be unhealthy (e.g. source creds rotated/revoked or the
	// source went down after a once-ACTIVE link was created). Report that as drift
	// (report-only, §8.6) rather than "Present" — otherwise a re-apply reports a
	// green link while no data replicates. Checked before source-id/config so a
	// dead link surfaces regardless.
	if !linkHealthy(actual) {
		return plan{Steps: []step{{Change: reconcile.Change{Action: reconcile.ActionDrift, Summary: summary,
			Detail: unhealthyLinkDetail(actual)}}}}, nil
	}

	if actual.SourceClusterID == "" || actual.SourceClusterID == sourceID {
		// Present: the link matches the desired source, OR the target did not
		// report a source id (older CP) so we cannot prove drift — treat as
		// present rather than fabricate drift (both are non-mutating anyway).
		if detail := r.configDrift(ctx, r.tgt, r.cfg.LinkName); detail != "" {
			return plan{Steps: []step{{Change: reconcile.Change{Action: reconcile.ActionDrift, Summary: summary, Detail: detail}}}}, nil
		}
		return plan{Steps: []step{{Change: reconcile.Change{Action: reconcile.ActionPresent, Summary: summary}}}}, nil
	}

	return plan{Steps: []step{{Change: reconcile.Change{Action: reconcile.ActionDrift, Summary: summary,
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

	// Destination side first. The INBOUND link must also carry the link configs —
	// in particular cluster.link.prefix: mirror topics are created on the
	// migration-dest (this INBOUND link), and cp-server enforces mirror renaming
	// on the link that hosts the mirror. Without the prefix here, a prefixed
	// mirror-create is rejected (error 40035 "Topic renaming for mirroring not yet
	// supported"). The OUTBOUND link also carries the configs (below) so the
	// prefix can be read from the source side per the mirrorTopics wiring.
	switch {
	case destActual == nil:
		steps = append(steps, step{
			Change: reconcile.Change{Action: reconcile.ActionCreate, Summary: destSummary,
				Detail: fmt.Sprintf("INBOUND, source %s", sourceID)},
			Payload: linkPayload{
				req: &svclink.CreateClusterLinkRequest{
					LinkMode:        "DESTINATION",
					ConnectionMode:  "INBOUND",
					SourceClusterID: sourceID,
					Configs:         r.cfg.Configs,
				},
				onSource: false,
			},
		})
	case !linkHealthy(destActual):
		steps = append(steps, step{Change: reconcile.Change{Action: reconcile.ActionDrift, Summary: destSummary, Detail: unhealthyLinkDetail(destActual)}})
	default:
		steps = append(steps, step{Change: reconcile.Change{Action: reconcile.ActionPresent, Summary: destSummary}})
	}

	// Source side second.
	if srcActual == nil {
		tls, err := r.cfg.DestAuth.LoadTLS()
		if err != nil {
			return nil, fmt.Errorf("loading source→destination TLS material: %w", err)
		}
		steps = append(steps, step{
			Change: reconcile.Change{Action: reconcile.ActionCreate, Summary: srcSummary,
				Detail: "OUTBOUND to destination"},
			Payload: linkPayload{
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
			},
		})
	} else {
		change := reconcile.Change{Action: reconcile.ActionPresent, Summary: srcSummary}
		if !linkHealthy(srcActual) {
			change = reconcile.Change{Action: reconcile.ActionDrift, Summary: srcSummary, Detail: unhealthyLinkDetail(srcActual)}
		} else if detail := r.configDrift(ctx, r.srcLinkTgt, r.cfg.LinkName); detail != "" {
			change = reconcile.Change{Action: reconcile.ActionDrift, Summary: srcSummary, Detail: detail}
		}
		steps = append(steps, step{Change: change})
	}

	return plan{Steps: steps}, nil
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

// configDrift returns a non-empty detail string when any desired config key
// differs from the link's live value. Only desired keys are compared (the link
// returns every default; unmanaged keys are ignored). A read error is treated as
// "no drift": a transient read must not fabricate drift, and source-identity
// drift (checked separately) is the authoritative signal.
func (r *Reconciler) configDrift(ctx context.Context, tgt target, name string) string {
	if len(r.cfg.Configs) == 0 {
		return ""
	}
	live, err := tgt.GetClusterLinkConfigs(ctx, name)
	if err != nil {
		return ""
	}
	keys := make([]string, 0, len(r.cfg.Configs))
	for k := range r.cfg.Configs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var diffs []string
	for _, k := range keys {
		if live[k] != r.cfg.Configs[k] {
			diffs = append(diffs, fmt.Sprintf("%s: link has %q, manifest expects %q", k, live[k], r.cfg.Configs[k]))
		}
	}
	return strings.Join(diffs, "; ")
}

// linkHealthy reports whether an existing link is healthy (Present) rather than
// drift. We key off link_error — the field whose purpose is to report why a link
// is broken — NOT link_state: link_state has transient/healthy values (a link
// passes through pre-ACTIVE states before settling, and PAUSED is a deliberate
// cutover step), so flagging "state != ACTIVE" would fabricate drift on healthy
// links.
//
// link_error is the Kafka ClusterLinkError enum rendered as a string. Its
// zero/healthy value is "NO_ERROR" (cp-server 8.x emits exactly that for a
// healthy link); some surfaces omit the field entirely (""). BOTH mean healthy.
// Any other value (e.g. AUTHENTICATION_ERROR, SOURCE_UNREACHABLE) means the
// broker reports the link as broken — report-only drift (§8.6), since a re-apply
// otherwise shows a green link while no data flows.
func linkHealthy(l *svclink.ClusterLink) bool {
	return l.LinkError == "" || l.LinkError == "NO_ERROR"
}

// unhealthyLinkDetail describes a link that exists but reports a broker error.
func unhealthyLinkDetail(l *svclink.ClusterLink) string {
	if l.LinkState != "" {
		return fmt.Sprintf("link exists but reports an error (state %q): %s", l.LinkState, l.LinkError)
	}
	return fmt.Sprintf("link exists but reports an error: %s", l.LinkError)
}

func (r *Reconciler) Apply(ctx context.Context, p reconcile.Plan) (reconcile.Outcome, error) {
	cp, ok := p.(plan)
	if !ok {
		return reconcile.Outcome{}, fmt.Errorf("clusterlink: unexpected plan type %T", p)
	}

	var out reconcile.Outcome
	for _, s := range cp.Steps {
		switch s.Change.Action {
		case reconcile.ActionCreate:
			tgt := r.tgt
			if s.Payload.onSource {
				tgt = r.srcLinkTgt
			}
			if err := r.createClusterLink(ctx, tgt, *s.Payload.req, s.Payload.onSource); err != nil {
				// Return what we created before the failure so the caller can see
				// partial progress; the engine surfaces the error.
				return out, fmt.Errorf("creating cluster link: %w", err)
			}
			out.Created = append(out.Created, s.Change)
		case reconcile.ActionPresent:
			out.Present = append(out.Present, s.Change)
		default: // ActionDrift — report only, never override (§8.6)
			out.Drift = append(out.Drift, s.Change)
		}
	}
	return out, nil
}

// linkPropagationRetry* bound the retry of the source-side (OUTBOUND) create
// against the INBOUND-propagation race (see createClusterLink). Package vars so
// tests can shrink them.
var (
	linkPropagationRetryTimeout = 30 * time.Second
	linkPropagationRetryBackoff = 500 * time.Millisecond
)

// linkNotPropagated is the substring cp-server returns when the source-side
// OUTBOUND link is created before the just-created INBOUND link has propagated
// to the destination ("...the destination cluster does not have a link named X").
const linkNotPropagated = "does not have a link named"

// createClusterLink creates a link, retrying ONLY the source-side OUTBOUND create
// against the INBOUND-propagation race. A single source-initiated `apply` creates
// the INBOUND link then immediately the OUTBOUND link, whose validation connects
// to the destination and checks the INBOUND link is present; cp-server propagates
// that link asynchronously, so the OUTBOUND create can momentarily 400. We retry
// that specific transient (source side only) with backoff until it propagates, so
// a single `kcp migrate apply` is reliable rather than requiring a manual re-run.
// All other errors — and the destination-side create — fail immediately.
func (r *Reconciler) createClusterLink(ctx context.Context, tgt target, req svclink.CreateClusterLinkRequest, onSource bool) error {
	deadline := time.Now().Add(linkPropagationRetryTimeout)
	backoff := linkPropagationRetryBackoff
	for {
		err := tgt.CreateClusterLink(ctx, r.cfg.LinkName, req)
		if err == nil {
			return nil
		}
		if !onSource || !strings.Contains(err.Error(), linkNotPropagated) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 4*time.Second {
			backoff *= 2
		}
	}
}
