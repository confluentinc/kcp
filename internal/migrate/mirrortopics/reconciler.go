// Package mirrortopics reconciles spec.topics (mode: mirror): it creates a
// mirror topic on the cluster link for each selected source topic that is not
// already mirrored. Additive — never alters or deletes.
package mirrortopics

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/migrate/topicselect"
	svclink "github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/reconcile"
)

const linkConfigPrefix = "cluster.link.prefix"

// linkNotExist is the substring cp-server returns when a cluster link's config
// is read before the link object exists/is consistently readable on that
// cluster's REST ("...Cluster link 'X' does not exist."). Seen in dry-run
// (before the clusterLink reconciler creates the link) and in the brief
// post-create propagation window.
const linkNotExist = "does not exist"

// linkNotReadable is the substring cp-server returns when a mirror is created on
// a just-created link before that link is consistently readable for mirror
// operations ("...Unable to resolve cluster link information for X"). The
// clusterLink reconciler creates the link immediately before mirrorTopics runs
// and cp-server propagates it asynchronously, so the first mirror create(s) can
// momentarily 404. createMirror retries this specific transient; all other
// errors (e.g. a missing source topic) fail immediately and are reported
// per-topic. (Distinct from linkNotExist, which is the config-read message.)
const linkNotReadable = "Unable to resolve cluster link information"

// mirrorCreateRetry* bound createMirror's retry of the link-propagation race.
// Package vars so tests can shrink them.
var (
	mirrorCreateRetryTimeout = 30 * time.Second
	mirrorCreateRetryBackoff = 500 * time.Millisecond
)

type source interface {
	ListTopics(ctx context.Context) ([]string, error)
}

type linkTarget interface {
	ClusterID(ctx context.Context) (string, error)
	GetClusterLinkConfigs(ctx context.Context, name string) (map[string]string, error)
	ListMirrorTopics(ctx context.Context, name string) ([]svclink.MirrorTopic, error)
	CreateMirrorTopic(ctx context.Context, name, sourceTopic, mirrorTopic string) error
	// ListTopics returns every topic on the cluster (incl. mirror topics) — used
	// to detect a mirror-name collision with an existing topic.
	ListTopics(ctx context.Context) ([]string, error)
	// ListClusterLinks returns every cluster link on the cluster — used to
	// classify a colliding name as another link's mirror vs a plain topic.
	ListClusterLinks(ctx context.Context) ([]string, error)
}

type Config struct {
	LinkName string
	Include  []string
	Exclude  []string
	// Prefix is the manifest clusterLink.prefix; used as the mirror-name prefix
	// when the live link is not yet readable (dry-run / pre-create). When the
	// live link is readable, its cluster.link.prefix wins.
	Prefix string
}

type Reconciler struct {
	cfg       Config
	src       source
	tgt       linkTarget // destination: hosts mirrors + receives CreateMirrorTopic
	prefixTgt linkTarget // link object carrying cluster.link.prefix (tgt in dest mode, source link in source mode)
}

func New(cfg Config, src source, tgt, prefixTgt linkTarget) *Reconciler {
	return &Reconciler{cfg: cfg, src: src, tgt: tgt, prefixTgt: prefixTgt}
}

func (r *Reconciler) Name() string { return "mirrorTopics" }

func (r *Reconciler) CheckPreconditions(ctx context.Context) error {
	if _, err := r.tgt.ClusterID(ctx); err != nil {
		return fmt.Errorf("destination not reachable: %w", err)
	}
	return nil
}

// mirrorPayload is the create-time data for one mirror (source + mirror name),
// used only for ActionCreate steps. Changes()/Empty() come from reconcile.StepPlan.
type mirrorPayload struct {
	sourceTopic string
	mirrorTopic string
}

type step = reconcile.Step[mirrorPayload]

func (r *Reconciler) Plan(ctx context.Context) (reconcile.Plan, error) {
	all, err := r.src.ListTopics(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing source topics: %w", err)
	}
	desired, err := topicselect.SelectTopics(all, r.cfg.Include, r.cfg.Exclude)
	if err != nil {
		return nil, fmt.Errorf("selecting topics: %w", err)
	}
	prefix, err := r.readLinkPrefix(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading link prefix: %w", err)
	}

	mirrors, err := r.tgt.ListMirrorTopics(ctx, r.cfg.LinkName)
	if err != nil {
		return nil, fmt.Errorf("listing mirror topics: %w", err)
	}
	// Map each existing mirror name to its live status, so the plan can tell a
	// healthy mirror (Present) from one that exists but is no longer actively
	// mirroring — e.g. paused/stopped/failed out-of-band (Drift).
	//
	// Note on a related state that is NOT detectable here: cp-server enforces
	// mirror_topic_name == prefix + source_topic_name (error 40035 on any other
	// name), so a mirror named prefix+S is guaranteed to mirror source S. A
	// "same name, different source" mismatch is therefore structurally
	// impossible and needs no check.
	existingStatus := map[string]string{}
	for _, m := range mirrors {
		existingStatus[m.MirrorTopicName] = m.MirrorStatus
	}

	// A desired mirror name may already be taken on the destination — by a plain
	// topic, or by a mirror on a DIFFERENT cluster link. Either way the create
	// would fail (40002 "already exists"), so detect it and report drift with the
	// cause instead of planning a doomed create.
	destTopics, err := r.tgt.ListTopics(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing destination topics: %w", err)
	}
	destTopicSet := make(map[string]struct{}, len(destTopics))
	for _, t := range destTopics {
		destTopicSet[t] = struct{}{}
	}
	foreignMirror, err := r.foreignMirrors(ctx)
	if err != nil {
		return nil, err
	}

	steps := make([]step, 0, len(desired))
	for _, srcTopic := range desired {
		mirrorName := prefix + srcTopic
		summary := fmt.Sprintf("mirror topic %q", mirrorName)
		status, isOurs := existingStatus[mirrorName]
		owner := foreignMirror[mirrorName]
		_, plainTopic := destTopicSet[mirrorName]
		switch {
		case isOurs && mirrorHealthy(status):
			steps = append(steps, step{Change: reconcile.Change{Action: reconcile.ActionPresent, Summary: summary}})
		case isOurs:
			// Our mirror exists but is not actively mirroring (paused/stopped/failed,
			// e.g. tampered with out-of-band). Report-only — never altered or
			// recreated (§8.6); remediation is a deliberate operator action.
			steps = append(steps, step{Change: reconcile.Change{
				Action:  reconcile.ActionDrift,
				Summary: summary,
				Detail:  fmt.Sprintf("present but mirror status is %q (expected %s)", status, svclink.MirrorStatusActive),
			}})
		case owner != "":
			// The name is already a mirror on ANOTHER link — the create would
			// collide (40002). Report-only drift naming the owning link.
			steps = append(steps, step{Change: reconcile.Change{
				Action:  reconcile.ActionDrift,
				Summary: summary,
				Detail:  fmt.Sprintf("a topic named %q is already a mirror on cluster link %q — cannot create it here; use a clusterLink.prefix or remove that link", mirrorName, owner),
			}})
		case plainTopic:
			// The name is taken by a plain (non-mirror) topic — the create would
			// collide (40002). Report-only drift.
			steps = append(steps, step{Change: reconcile.Change{
				Action:  reconcile.ActionDrift,
				Summary: summary,
				Detail:  fmt.Sprintf("a plain (non-mirror) topic named %q already exists on the destination — remove it or use a clusterLink.prefix", mirrorName),
			}})
		default:
			steps = append(steps, step{
				Change:  reconcile.Change{Action: reconcile.ActionCreate, Summary: summary, Detail: fmt.Sprintf("source %s", srcTopic)},
				Payload: mirrorPayload{sourceTopic: srcTopic, mirrorTopic: mirrorName},
			})
		}
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].Change.Summary < steps[j].Change.Summary })
	return reconcile.StepPlan[mirrorPayload]{Steps: steps}, nil
}

// foreignMirrors maps every mirror-topic name on the destination to the cluster
// link that owns it, EXCLUDING this reconciler's own link (whose mirrors are
// handled via the per-link status read above). Used to tell "name taken by
// another link's mirror" from "name taken by a plain topic".
func (r *Reconciler) foreignMirrors(ctx context.Context) (map[string]string, error) {
	links, err := r.tgt.ListClusterLinks(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing destination cluster links: %w", err)
	}
	owner := map[string]string{}
	for _, link := range links {
		// Our own link's mirrors are classified via existingStatus above.
		// (Empty-named UNMANAGED_SOURCE records are already filtered by ListClusterLinks.)
		if link == r.cfg.LinkName {
			continue
		}
		mirrors, err := r.tgt.ListMirrorTopics(ctx, link)
		if err != nil {
			// Tolerate a single unrelated link that can't be read (e.g. mid-teardown
			// or a permissions edge): it must not fail the whole plan. Worst case we
			// miss the owning-link attribution for a colliding name — the collision
			// itself is still caught by destTopicSet (mirror topics appear in
			// ListTopics), just reported as a plain-topic collision.
			slog.Warn("skipping cluster link whose mirror topics could not be listed",
				"link", link, "error", err)
			continue
		}
		for _, m := range mirrors {
			owner[m.MirrorTopicName] = link
		}
	}
	return owner, nil
}

// mirrorHealthy reports whether a present mirror's status counts as healthy
// (Present) rather than drift. ACTIVE is healthy; an empty status is treated as
// healthy too, so a read that omits the field never fabricates drift (matching
// the config-drift policy). Every other reported state (PAUSED, STOPPED,
// FAILED, …) means the mirror exists but is not actively mirroring → drift.
func mirrorHealthy(status string) bool {
	return status == "" || status == svclink.MirrorStatusActive
}

// readLinkPrefix returns the prefix to use for mirror names. It prefers the LIVE
// cluster.link.prefix off the prefix-carrying link (authoritative — and correct
// when an immutable pre-existing link differs from an edited manifest). When the
// link does not exist yet — dry-run before the clusterLink reconciler creates it,
// or the brief post-create propagation window — it falls back to the manifest
// prefix (Config.Prefix), which is exactly what the link is/will-be created with.
func (r *Reconciler) readLinkPrefix(ctx context.Context) (string, error) {
	cfgs, err := r.prefixTgt.GetClusterLinkConfigs(ctx, r.cfg.LinkName)
	if err == nil {
		return cfgs[linkConfigPrefix], nil
	}
	if strings.Contains(err.Error(), linkNotExist) {
		return r.cfg.Prefix, nil // link not yet readable → manifest prefix == its value
	}
	return "", err
}

// Apply creates each missing mirror topic, continuing past per-mirror failures
// (collected in Outcome.Failed). Drift is reported only, never recreated (§8.6).
func (r *Reconciler) Apply(ctx context.Context, p reconcile.Plan) (reconcile.Outcome, error) {
	return reconcile.ApplyContinueOnError(ctx, p, "mirror topic(s)", func(ctx context.Context, m mirrorPayload) error {
		return r.createMirror(ctx, m.sourceTopic, m.mirrorTopic)
	})
}

// createMirror creates one mirror topic, retrying ONLY the link-not-readable
// transient (the link was just created by the clusterLink reconciler and cp-server
// hasn't made it consistently readable for mirror ops yet). All other errors
// return immediately so genuine failures (e.g. a missing source topic) are
// reported per-topic without delay.
func (r *Reconciler) createMirror(ctx context.Context, sourceTopic, mirrorTopic string) error {
	deadline := time.Now().Add(mirrorCreateRetryTimeout)
	backoff := mirrorCreateRetryBackoff
	for {
		err := r.tgt.CreateMirrorTopic(ctx, r.cfg.LinkName, sourceTopic, mirrorTopic)
		if err == nil {
			return nil
		}
		if !strings.Contains(err.Error(), linkNotReadable) || time.Now().After(deadline) {
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
