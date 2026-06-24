// Package newtopics reconciles spec.topics (mode: new): it creates a plain topic
// on the target for each selected source topic, reproducing partition count, RF,
// and explicitly-set configs (minus a managed/read-only skip-list). Additive.
package newtopics

import (
	"context"
	"fmt"
	"sort"

	"github.com/confluentinc/kcp/internal/migrate"
	"github.com/confluentinc/kcp/internal/migrate/topicselect"
	svclink "github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/reconcile"
)

type source interface {
	ListTopics(ctx context.Context) ([]string, error)
	DescribeTopics(ctx context.Context, names []string) ([]migrate.TopicSpec, error)
}

type topicTarget interface {
	ClusterID(ctx context.Context) (string, error)
	ListTopics(ctx context.Context) ([]string, error)
	CreateTopic(ctx context.Context, req svclink.CreateTopicRequest) error
}

// partitionCounter is an OPTIONAL capability: when the target implements it,
// partition-count drift is reported for existing topics. When absent, existing
// topics are reported Present without a partition comparison.
type partitionCounter interface {
	PartitionCount(ctx context.Context, topic string) (int, error)
}

type Config struct {
	Include    []string
	Exclude    []string
	ConfigSkip map[string]struct{} // managed/read-only keys not to forward
}

// DefaultSkipList seeds clearly managed/broker keys; grows from integration findings.
func DefaultSkipList() map[string]struct{} {
	keys := []string{
		"confluent.tier.enable",
		"confluent.tier.local.hotset.ms",
		"confluent.tier.local.hotset.bytes",
		"leader.replication.throttled.replicas",
		"follower.replication.throttled.replicas",
	}
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		m[k] = struct{}{}
	}
	return m
}

type Reconciler struct {
	cfg Config
	src source
	tgt topicTarget
}

func New(cfg Config, src source, tgt topicTarget) *Reconciler {
	return &Reconciler{cfg: cfg, src: src, tgt: tgt}
}

func (r *Reconciler) Name() string { return "newTopics" }

func (r *Reconciler) CheckPreconditions(ctx context.Context) error {
	if _, err := r.tgt.ClusterID(ctx); err != nil {
		return fmt.Errorf("target not reachable: %w", err)
	}
	return nil
}

type topicStep struct {
	change reconcile.Change
	req    svclink.CreateTopicRequest // valid only for ActionCreate
}

type plan struct{ steps []topicStep }

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
	specs, err := r.src.DescribeTopics(ctx, desired)
	if err != nil {
		return nil, fmt.Errorf("describing source topics: %w", err)
	}
	specByName := map[string]migrate.TopicSpec{}
	for _, s := range specs {
		specByName[s.Name] = s
	}

	targetTopics, err := r.tgt.ListTopics(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing target topics: %w", err)
	}
	existing := map[string]struct{}{}
	for _, t := range targetTopics {
		existing[t] = struct{}{}
	}
	pc, hasPC := r.tgt.(partitionCounter)

	steps := make([]topicStep, 0, len(desired))
	for _, name := range desired {
		spec := specByName[name]
		summary := fmt.Sprintf("topic %q", name)
		if _, ok := existing[name]; ok {
			if hasPC {
				if actual, err := pc.PartitionCount(ctx, name); err == nil && actual != spec.Partitions {
					steps = append(steps, topicStep{change: reconcile.Change{Action: reconcile.ActionDrift, Summary: summary,
						Detail: fmt.Sprintf("target has %d partitions, manifest expects %d", actual, spec.Partitions)}})
					continue
				}
			}
			steps = append(steps, topicStep{change: reconcile.Change{Action: reconcile.ActionPresent, Summary: summary}})
			continue
		}
		configs := map[string]string{}
		for k, v := range spec.Configs {
			if _, skip := r.cfg.ConfigSkip[k]; skip {
				continue
			}
			configs[k] = v
		}
		steps = append(steps, topicStep{
			change: reconcile.Change{Action: reconcile.ActionCreate, Summary: summary,
				Detail: fmt.Sprintf("%d partitions", spec.Partitions)},
			req: svclink.CreateTopicRequest{Name: name, Partitions: spec.Partitions, ReplicationFactor: spec.ReplicationFactor, Configs: configs},
		})
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].change.Summary < steps[j].change.Summary })
	return plan{steps: steps}, nil
}

func (r *Reconciler) Apply(ctx context.Context, p reconcile.Plan) (reconcile.Outcome, error) {
	cp, ok := p.(plan)
	if !ok {
		return reconcile.Outcome{}, fmt.Errorf("newtopics: unexpected plan type %T", p)
	}
	var out reconcile.Outcome
	var failed int
	for _, s := range cp.steps {
		switch s.change.Action {
		case reconcile.ActionCreate:
			if err := r.tgt.CreateTopic(ctx, s.req); err != nil {
				failed++
				out.Failed = append(out.Failed, reconcile.Change{Action: reconcile.ActionCreate, Summary: s.change.Summary, Detail: err.Error()})
				continue
			}
			out.Created = append(out.Created, s.change)
		case reconcile.ActionPresent:
			out.Present = append(out.Present, s.change)
		default: // ActionDrift — report only, never alter (partitions can't be safely changed)
			out.Drift = append(out.Drift, s.change)
		}
	}
	if failed > 0 {
		return out, fmt.Errorf("%d of %d topic(s) failed to create", failed, failed+len(out.Created))
	}
	return out, nil
}
