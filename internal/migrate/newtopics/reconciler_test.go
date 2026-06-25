package newtopics

import (
	"context"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/migrate"
	svclink "github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/reconcile"
)

// fakeSource scripts the live source topic list and per-topic specs.
type fakeSource struct {
	topics    []string
	listErr   error
	specs     []migrate.TopicSpec
	describeE error
}

func (f *fakeSource) ListTopics(ctx context.Context) ([]string, error) {
	return f.topics, f.listErr
}

func (f *fakeSource) DescribeTopics(ctx context.Context, names []string) ([]migrate.TopicSpec, error) {
	return f.specs, f.describeE
}

// fakeTarget scripts the target endpoint. It records CreateTopic requests and
// can fail specific topics. It does NOT implement partitionCounter.
type fakeTarget struct {
	clusterID    string
	clusterIDErr error
	topics       []string
	topicsErr    error
	createErr    map[string]error // keyed by topic name
	created      []svclink.CreateTopicRequest
}

func (f *fakeTarget) ClusterID(ctx context.Context) (string, error) {
	return f.clusterID, f.clusterIDErr
}

func (f *fakeTarget) ListTopics(ctx context.Context) ([]string, error) {
	return f.topics, f.topicsErr
}

func (f *fakeTarget) CreateTopic(ctx context.Context, req svclink.CreateTopicRequest) error {
	if err := f.createErr[req.Name]; err != nil {
		return err
	}
	f.created = append(f.created, req)
	return nil
}

// fakePCTarget is a fakeTarget that ALSO implements partitionCounter so drift
// is exercised. partitions maps topic name → live partition count on target.
type fakePCTarget struct {
	fakeTarget
	partitions map[string]int
	pcErr      map[string]error
}

func (f *fakePCTarget) PartitionCount(ctx context.Context, topic string) (int, error) {
	if err := f.pcErr[topic]; err != nil {
		return 0, err
	}
	return f.partitions[topic], nil
}

// summaryActions maps each change's Summary to its Action for assertions.
func summaryActions(changes []reconcile.Change) map[string]reconcile.Action {
	out := make(map[string]reconcile.Action, len(changes))
	for _, c := range changes {
		out[c.Summary] = c.Action
	}
	return out
}

type errStub string

func (e errStub) Error() string { return string(e) }

// TestPlan_CreatePresentDrift covers all three actions with a partitionCounter
// target: orders absent → Create; events present & matches → Present; legacy
// present but partition count differs → Drift.
func TestPlan_CreatePresentDrift(t *testing.T) {
	src := &fakeSource{
		topics: []string{"orders", "events", "legacy"},
		specs: []migrate.TopicSpec{
			{Name: "orders", Partitions: 6, ReplicationFactor: 3, Configs: map[string]string{"retention.ms": "1000", "confluent.tier.enable": "true"}},
			{Name: "events", Partitions: 3, ReplicationFactor: 3},
			{Name: "legacy", Partitions: 12, ReplicationFactor: 3},
		},
	}
	tgt := &fakePCTarget{
		fakeTarget: fakeTarget{
			clusterID: "cc-1",
			topics:    []string{"events", "legacy"},
		},
		partitions: map[string]int{"events": 3, "legacy": 6},
	}
	r := New(Config{Include: []string{"*"}}, src, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	got := summaryActions(p.Changes())
	if act, ok := got[`topic "orders"`]; !ok || act != reconcile.ActionCreate {
		t.Errorf("orders: want Create, got %v (present=%v)", act, ok)
	}
	if act, ok := got[`topic "events"`]; !ok || act != reconcile.ActionPresent {
		t.Errorf("events: want Present, got %v (present=%v)", act, ok)
	}
	if act, ok := got[`topic "legacy"`]; !ok || act != reconcile.ActionDrift {
		t.Errorf("legacy: want Drift, got %v (present=%v)", act, ok)
	}
	if len(p.Changes()) != 3 {
		t.Errorf("want 3 changes, got %d", len(p.Changes()))
	}
	if p.Empty() {
		t.Errorf("plan with a Create should not be Empty")
	}
}

// TestPlanApply_ForwardsAllExplicitConfigs asserts the Create req for orders
// carries ALL explicitly-set source configs with no filtering: there is no
// skip-list, so both retention.ms and confluent.tier.enable are forwarded. If
// the target can't accept one, that surfaces as a per-topic create failure.
func TestPlanApply_ForwardsAllExplicitConfigs(t *testing.T) {
	src := &fakeSource{
		topics: []string{"orders"},
		specs: []migrate.TopicSpec{
			{Name: "orders", Partitions: 6, ReplicationFactor: 3, Configs: map[string]string{"retention.ms": "1000", "confluent.tier.enable": "true"}},
		},
	}
	tgt := &fakeTarget{clusterID: "cc-1"}
	r := New(Config{Include: []string{"*"}}, src, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if _, err := r.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(tgt.created) != 1 {
		t.Fatalf("want 1 created topic, got %d", len(tgt.created))
	}
	req := tgt.created[0]
	if req.Name != "orders" || req.Partitions != 6 || req.ReplicationFactor != 3 {
		t.Errorf("unexpected create req shape: %+v", req)
	}
	if got := req.Configs["retention.ms"]; got != "1000" {
		t.Errorf("retention.ms should be forwarded as %q, configs=%v", "1000", req.Configs)
	}
	if got := req.Configs["confluent.tier.enable"]; got != "true" {
		t.Errorf("confluent.tier.enable should be forwarded (no skip-list) as %q, configs=%v", "true", req.Configs)
	}
	if len(req.Configs) != 2 {
		t.Errorf("want exactly 2 forwarded configs, got %d: %v", len(req.Configs), req.Configs)
	}
}

// TestApply_ContinueOnError: two creates, one errors → Created 1, Failed 1, err.
func TestApply_ContinueOnError(t *testing.T) {
	src := &fakeSource{
		topics: []string{"a", "b"},
		specs: []migrate.TopicSpec{
			{Name: "a", Partitions: 1, ReplicationFactor: 1},
			{Name: "b", Partitions: 1, ReplicationFactor: 1},
		},
	}
	tgt := &fakeTarget{
		clusterID: "cc-1",
		createErr: map[string]error{"b": errStub("boom")},
	}
	r := New(Config{Include: []string{"*"}}, src, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	out, err := r.Apply(context.Background(), p)
	if err == nil {
		t.Fatalf("Apply: want error mentioning failure count, got nil")
	}
	if !strings.Contains(err.Error(), "failed to create") {
		t.Errorf("Apply error %q should mention failure count", err.Error())
	}
	if len(out.Created) != 1 {
		t.Errorf("want 1 created, got %d", len(out.Created))
	}
	if len(out.Failed) != 1 {
		t.Errorf("want 1 failed, got %d", len(out.Failed))
	}
	if len(tgt.created) != 1 || tgt.created[0].Name != "a" {
		t.Errorf("only 'a' should have been created, got %+v", tgt.created)
	}
}

// TestPlan_NoPartitionCounter: a target without partitionCounter reports an
// existing topic as Present even when partition counts would differ.
func TestPlan_NoPartitionCounter(t *testing.T) {
	src := &fakeSource{
		topics: []string{"legacy"},
		specs: []migrate.TopicSpec{
			{Name: "legacy", Partitions: 12, ReplicationFactor: 3},
		},
	}
	// fakeTarget does NOT implement partitionCounter.
	tgt := &fakeTarget{
		clusterID: "cc-1",
		topics:    []string{"legacy"}, // already present, with a different (unknown) part count
	}
	r := New(Config{Include: []string{"*"}}, src, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	got := summaryActions(p.Changes())
	if act, ok := got[`topic "legacy"`]; !ok || act != reconcile.ActionPresent {
		t.Errorf("legacy: want Present (no partitionCounter → no drift), got %v (present=%v)", act, ok)
	}
}

// TestPlan_PartitionCountError: when the partitionCounter read fails for an
// existing topic, the failure is non-fatal and the topic is reported Present
// (no drift fabricated from an unknown count).
func TestPlan_PartitionCountError(t *testing.T) {
	src := &fakeSource{
		topics: []string{"legacy"},
		specs: []migrate.TopicSpec{
			{Name: "legacy", Partitions: 12, ReplicationFactor: 3},
		},
	}
	tgt := &fakePCTarget{
		fakeTarget: fakeTarget{clusterID: "cc-1", topics: []string{"legacy"}},
		pcErr:      map[string]error{"legacy": errStub("count read failed")},
	}
	r := New(Config{Include: []string{"*"}}, src, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	got := summaryActions(p.Changes())
	if act, ok := got[`topic "legacy"`]; !ok || act != reconcile.ActionPresent {
		t.Errorf("legacy: want Present (partition count read errored → non-fatal), got %v (present=%v)", act, ok)
	}
}

// TestPlanApply_SpecialCharTopicName verifies a source topic with dot and
// hyphen separators ("events-2026.q1") absent on target flows verbatim into the
// CreateTopicRequest.Name.
func TestPlanApply_SpecialCharTopicName(t *testing.T) {
	src := &fakeSource{
		topics: []string{"events-2026.q1"},
		specs: []migrate.TopicSpec{
			{Name: "events-2026.q1", Partitions: 4, ReplicationFactor: 3},
		},
	}
	tgt := &fakeTarget{clusterID: "cc-1"} // topic absent on target
	r := New(Config{Include: []string{"*"}}, src, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	got := summaryActions(p.Changes())
	if act, ok := got[`topic "events-2026.q1"`]; !ok || act != reconcile.ActionCreate {
		t.Errorf("events-2026.q1: want Create, got %v (present=%v); all=%v", act, ok, got)
	}

	if _, err := r.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(tgt.created) != 1 {
		t.Fatalf("want 1 created, got %d", len(tgt.created))
	}
	if tgt.created[0].Name != "events-2026.q1" {
		t.Errorf("special-char name not passed through verbatim, got %q", tgt.created[0].Name)
	}
}

// TestPlan_EmptyDesiredSet: no topic matches the include glob → plan Empty.
func TestPlan_EmptyDesiredSet(t *testing.T) {
	src := &fakeSource{topics: []string{"orders", "_schemas"}}
	tgt := &fakeTarget{clusterID: "cc-1"}
	r := New(Config{Include: []string{"nomatch-*"}}, src, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(p.Changes()) != 0 {
		t.Errorf("want 0 changes, got %d", len(p.Changes()))
	}
	if !p.Empty() {
		t.Errorf("plan with no creates should be Empty")
	}
}
