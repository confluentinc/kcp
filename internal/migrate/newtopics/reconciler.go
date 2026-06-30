// Package newtopics reconciles spec.topics (mode: new): it creates a plain topic
// on the target for each selected source topic, reproducing partition count, RF,
// and all explicitly-set (non-default) configs. If the target rejects a config it
// can't accept, that topic's create fails and is reported per-topic
// (continue-on-error) rather than guessed away up front. Additive.
package newtopics

import (
	"context"
	"fmt"
	"log/slog"
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
	Include []string
	Exclude []string
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

// step is a planned topic action; the payload is the create request, used only
// for ActionCreate. Changes()/Empty() come from reconcile.StepPlan.
type step = reconcile.Step[svclink.CreateTopicRequest]

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

	steps := make([]step, 0, len(desired))
	for _, name := range desired {
		// A selected topic absent from the describe result vanished from the source
		// between ListTopics and DescribeTopics (separate connections). Skip it with
		// a warning rather than reading the zero-value spec (which would emit a
		// 0-partition create, or fabricate partition drift against an existing target
		// topic). Keys off the map miss — not spec.Partitions == 0.
		spec, ok := specByName[name]
		if !ok {
			slog.Warn("source topic vanished between list and describe; skipping", "topic", name)
			continue
		}
		summary := fmt.Sprintf("topic %q", name)
		if _, ok := existing[name]; ok {
			if hasPC {
				actual, err := pc.PartitionCount(ctx, name)
				switch {
				case err != nil:
					// Non-fatal: a failed count read must not abort the plan. Report
					// the topic as present, but surface the failure so a swallowed
					// drift signal isn't silent.
					slog.Warn("could not read target partition count; treating topic as present without drift check",
						"topic", name, "error", err)
				case actual != spec.Partitions:
					steps = append(steps, step{Change: reconcile.Change{Action: reconcile.ActionDrift, Summary: summary,
						Detail: fmt.Sprintf("target has %d partitions, manifest expects %d", actual, spec.Partitions)}})
					continue
				}
			}
			steps = append(steps, step{Change: reconcile.Change{Action: reconcile.ActionPresent, Summary: summary}})
			continue
		}
		// Forward all explicitly-set source configs (DescribeTopics already returns
		// only non-default topic configs). If the target rejects one it can't
		// accept, that create fails and is reported per-topic (continue-on-error).
		configs := make(map[string]string, len(spec.Configs))
		for k, v := range spec.Configs {
			configs[k] = v
		}
		steps = append(steps, step{
			Change: reconcile.Change{Action: reconcile.ActionCreate, Summary: summary,
				Detail: fmt.Sprintf("%d partitions", spec.Partitions)},
			// Replication factor is intentionally not forwarded — the target cluster
			// applies its default (CC requires RF=3 and rejects other values).
			Payload: svclink.CreateTopicRequest{Name: name, Partitions: spec.Partitions, Configs: configs},
		})
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].Change.Summary < steps[j].Change.Summary })
	return reconcile.StepPlan[svclink.CreateTopicRequest]{Steps: steps}, nil
}

// Apply creates each selected topic on the target, continuing past per-topic
// failures (collected in Outcome.Failed). Drift is reported only, never altered.
func (r *Reconciler) Apply(ctx context.Context, p reconcile.Plan) (reconcile.Outcome, error) {
	return reconcile.ApplyContinueOnError(ctx, p, "topic(s)", r.tgt.CreateTopic)
}
