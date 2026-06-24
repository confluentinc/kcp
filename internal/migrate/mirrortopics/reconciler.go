// Package mirrortopics reconciles spec.topics (mode: mirror): it creates a
// mirror topic on the cluster link for each selected source topic that is not
// already mirrored. Additive — never alters or deletes.
package mirrortopics

import (
	"context"
	"fmt"
	"sort"

	"github.com/confluentinc/kcp/internal/migrate/topicselect"
	svclink "github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/reconcile"
)

const linkConfigPrefix = "cluster.link.prefix"

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
	cfgs, err := r.prefixTgt.GetClusterLinkConfigs(ctx, r.cfg.LinkName)
	if err != nil {
		return nil, fmt.Errorf("reading link prefix: %w", err)
	}
	prefix := cfgs[linkConfigPrefix]

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
			out.Drift = append(out.Drift, s.change)
		}
	}
	if failed > 0 {
		return out, fmt.Errorf("%d of %d mirror topic(s) failed to create", failed, failed+len(out.Created))
	}
	return out, nil
}
