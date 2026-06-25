// Package mirrortopics reconciles spec.topics (mode: mirror): it creates a
// mirror topic on the cluster link for each selected source topic that is not
// already mirrored. Additive — never alters or deletes.
package mirrortopics

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/migrate/topicselect"
	svclink "github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/reconcile"
)

const linkConfigPrefix = "cluster.link.prefix"

// prefixReadRetry* bound the retry of the link-prefix read against the
// link-propagation race (see readLinkPrefix). Package vars so tests can shrink
// them.
var (
	prefixReadRetryTimeout = 30 * time.Second
	prefixReadRetryBackoff = 500 * time.Millisecond
)

// linkNotExist is the substring cp-server returns when a freshly-created cluster
// link's config is read before the link object is consistently readable on that
// cluster's REST ("...Cluster link 'X' does not exist.").
const linkNotExist = "does not exist"

type source interface {
	ListTopics(ctx context.Context) ([]string, error)
}

type linkTarget interface {
	ClusterID(ctx context.Context) (string, error)
	GetClusterLinkConfigs(ctx context.Context, name string) (map[string]string, error)
	ListMirrorTopics(ctx context.Context, name string) ([]svclink.MirrorTopic, error)
	CreateMirrorTopic(ctx context.Context, name, sourceTopic, mirrorTopic string) error
}

type Config struct {
	LinkName string
	Include  []string
	Exclude  []string
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

type mirrorStep struct {
	change      reconcile.Change
	sourceTopic string
	mirrorTopic string
}

type plan struct{ steps []mirrorStep }

func (p plan) Changes() []reconcile.Change {
	out := make([]reconcile.Change, len(p.steps))
	for i, s := range p.steps {
		out[i] = s.change
	}
	return out
}
func (p plan) Empty() bool {
	for _, s := range p.steps {
		if s.change.Action == reconcile.ActionCreate {
			return false
		}
	}
	return true
}

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
	existing := map[string]struct{}{}
	for _, m := range mirrors {
		existing[m.MirrorTopicName] = struct{}{}
	}

	steps := make([]mirrorStep, 0, len(desired))
	for _, srcTopic := range desired {
		mirrorName := prefix + srcTopic
		summary := fmt.Sprintf("mirror topic %q", mirrorName)
		if _, ok := existing[mirrorName]; ok {
			steps = append(steps, mirrorStep{change: reconcile.Change{Action: reconcile.ActionPresent, Summary: summary}})
			continue
		}
		steps = append(steps, mirrorStep{
			change:      reconcile.Change{Action: reconcile.ActionCreate, Summary: summary, Detail: fmt.Sprintf("source %s", srcTopic)},
			sourceTopic: srcTopic, mirrorTopic: mirrorName,
		})
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].change.Summary < steps[j].change.Summary })
	return plan{steps: steps}, nil
}

// readLinkPrefix reads the cluster.link.prefix off the prefix-carrying link
// (the destination in destination mode, the source-side OUTBOUND link in source
// mode), retrying the transient "link does not exist" race. In source mode the
// clusterLink reconciler creates the OUTBOUND link immediately before this runs;
// cp-server returns 200 on that create but the link object's config read can 404
// for a brief window until it is consistently readable on that cluster's REST.
// We retry that specific transient with backoff (mirroring the OUTBOUND-create
// retry in the clusterlink reconciler) so a single source-initiated `kcp migrate
// apply` is reliable rather than requiring a manual re-run. All other errors fail
// immediately.
func (r *Reconciler) readLinkPrefix(ctx context.Context) (string, error) {
	deadline := time.Now().Add(prefixReadRetryTimeout)
	backoff := prefixReadRetryBackoff
	for {
		cfgs, err := r.prefixTgt.GetClusterLinkConfigs(ctx, r.cfg.LinkName)
		if err == nil {
			return cfgs[linkConfigPrefix], nil
		}
		if !strings.Contains(err.Error(), linkNotExist) || time.Now().After(deadline) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 4*time.Second {
			backoff *= 2
		}
	}
}

func (r *Reconciler) Apply(ctx context.Context, p reconcile.Plan) (reconcile.Outcome, error) {
	cp, ok := p.(plan)
	if !ok {
		return reconcile.Outcome{}, fmt.Errorf("mirrortopics: unexpected plan type %T", p)
	}
	var out reconcile.Outcome
	var failed int
	for _, s := range cp.steps {
		switch s.change.Action {
		case reconcile.ActionCreate:
			if err := r.tgt.CreateMirrorTopic(ctx, r.cfg.LinkName, s.sourceTopic, s.mirrorTopic); err != nil {
				failed++
				out.Failed = append(out.Failed, reconcile.Change{Action: reconcile.ActionCreate, Summary: s.change.Summary, Detail: err.Error()})
				continue
			}
			out.Created = append(out.Created, s.change)
		case reconcile.ActionPresent:
			out.Present = append(out.Present, s.change)
		default:
			// No Drift today: drift detection is deferred because MirrorTopic
			// exposes no source-topic-name field to compare against. This branch
			// is defensive (switch exhaustiveness) and currently unreachable.
			out.Drift = append(out.Drift, s.change)
		}
	}
	if failed > 0 {
		return out, fmt.Errorf("%d of %d mirror topic(s) failed to create", failed, failed+len(out.Created))
	}
	return out, nil
}
