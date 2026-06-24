package mirrortopics

import (
	"context"
	"strings"
	"testing"

	svclink "github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/reconcile"
)

// fakeSource scripts the live source topic list.
type fakeSource struct {
	topics []string
	err    error
}

func (f *fakeSource) ListTopics(ctx context.Context) ([]string, error) {
	return f.topics, f.err
}

// fakeLinkTarget scripts the destination/link endpoint. It records
// CreateMirrorTopic calls and can fail specific source topics.
type fakeLinkTarget struct {
	clusterID    string
	clusterIDErr error
	configs      map[string]string
	configsErr   error
	mirrors      []svclink.MirrorTopic
	mirrorsErr   error
	createErr    map[string]error // keyed by source topic
	created      []struct{ src, mirror string }
}

func (f *fakeLinkTarget) ClusterID(ctx context.Context) (string, error) {
	return f.clusterID, f.clusterIDErr
}

func (f *fakeLinkTarget) GetClusterLinkConfigs(ctx context.Context, name string) (map[string]string, error) {
	return f.configs, f.configsErr
}

func (f *fakeLinkTarget) ListMirrorTopics(ctx context.Context, name string) ([]svclink.MirrorTopic, error) {
	return f.mirrors, f.mirrorsErr
}

func (f *fakeLinkTarget) CreateMirrorTopic(ctx context.Context, name, sourceTopic, mirrorTopic string) error {
	if err := f.createErr[sourceTopic]; err != nil {
		return err
	}
	f.created = append(f.created, struct{ src, mirror string }{sourceTopic, mirrorTopic})
	return nil
}

// summaryActions maps each change's Summary to its Action for assertions.
func summaryActions(changes []reconcile.Change) map[string]reconcile.Action {
	out := make(map[string]reconcile.Action, len(changes))
	for _, c := range changes {
		out[c.Summary] = c.Action
	}
	return out
}

func TestPlan_CreatePresentInternalFilter(t *testing.T) {
	src := &fakeSource{topics: []string{"orders", "events", "_schemas"}}
	tgt := &fakeLinkTarget{
		clusterID: "dest-1",
		configs:   map[string]string{linkConfigPrefix: "dc-"},
		mirrors:   []svclink.MirrorTopic{{MirrorTopicName: "dc-events"}},
	}
	r := New(Config{LinkName: "lk", Include: []string{"*"}}, src, tgt, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	got := summaryActions(p.Changes())
	if act, ok := got[`mirror topic "dc-orders"`]; !ok || act != reconcile.ActionCreate {
		t.Errorf("dc-orders: want Create, got %v (present=%v)", act, ok)
	}
	if act, ok := got[`mirror topic "dc-events"`]; !ok || act != reconcile.ActionPresent {
		t.Errorf("dc-events: want Present, got %v (present=%v)", act, ok)
	}
	if _, ok := got[`mirror topic "dc-_schemas"`]; ok {
		t.Errorf("_schemas should be filtered out, but appears in plan")
	}
	if len(p.Changes()) != 2 {
		t.Errorf("want 2 changes, got %d", len(p.Changes()))
	}
	if p.Empty() {
		t.Errorf("plan with a Create should not be Empty")
	}
}

func TestPlan_NoPrefix(t *testing.T) {
	src := &fakeSource{topics: []string{"orders"}}
	tgt := &fakeLinkTarget{
		clusterID: "dest-1",
		configs:   map[string]string{}, // no prefix
	}
	r := New(Config{LinkName: "lk", Include: []string{"*"}}, src, tgt, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	got := summaryActions(p.Changes())
	if act, ok := got[`mirror topic "orders"`]; !ok || act != reconcile.ActionCreate {
		t.Errorf("orders: want Create with mirror name == source name, got %v (present=%v)", act, ok)
	}
}

func TestPlan_EmptyDesiredSet(t *testing.T) {
	// No topic matches the include glob (and internal topics are filtered) →
	// the plan has no changes and is Empty, so the engine skips Apply.
	src := &fakeSource{topics: []string{"orders", "_schemas"}}
	tgt := &fakeLinkTarget{clusterID: "dest-1", configs: map[string]string{linkConfigPrefix: "dc-"}}
	r := New(Config{LinkName: "lk", Include: []string{"nomatch-*"}}, src, tgt, tgt)

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

func TestApply_ContinueOnError(t *testing.T) {
	src := &fakeSource{topics: []string{"a", "b"}}
	tgt := &fakeLinkTarget{
		clusterID: "dest-1",
		configs:   map[string]string{},
		createErr: map[string]error{"b": errStub("boom")},
	}
	r := New(Config{LinkName: "lk", Include: []string{"*"}}, src, tgt, tgt)

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
	if len(tgt.created) != 1 || tgt.created[0].src != "a" {
		t.Errorf("only 'a' should have been created, got %+v", tgt.created)
	}
}

func TestPlanApply_PrefixTgtDistinctFromTgt(t *testing.T) {
	src := &fakeSource{topics: []string{"orders"}}
	// prefixTgt carries the prefix config; its mirrors/create must NOT be used.
	prefixTgt := &fakeLinkTarget{
		clusterID: "src-link",
		configs:   map[string]string{linkConfigPrefix: "pp-"},
		mirrors:   []svclink.MirrorTopic{{MirrorTopicName: "should-not-be-read"}},
	}
	// tgt hosts the mirrors and receives the create; its configs must NOT be read.
	tgt := &fakeLinkTarget{
		clusterID: "dest-1",
		configs:   map[string]string{linkConfigPrefix: "WRONG-"},
		mirrors:   nil,
	}
	r := New(Config{LinkName: "lk", Include: []string{"*"}}, src, tgt, prefixTgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	got := summaryActions(p.Changes())
	// prefix came from prefixTgt ("pp-"), not tgt ("WRONG-").
	if act, ok := got[`mirror topic "pp-orders"`]; !ok || act != reconcile.ActionCreate {
		t.Errorf("want Create for pp-orders (prefix read from prefixTgt), got %v (present=%v); all=%v", act, ok, got)
	}

	if _, err := r.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Create issued against tgt, not prefixTgt.
	if len(tgt.created) != 1 || tgt.created[0].mirror != "pp-orders" || tgt.created[0].src != "orders" {
		t.Errorf("create should target tgt with pp-orders/orders, got %+v", tgt.created)
	}
	if len(prefixTgt.created) != 0 {
		t.Errorf("prefixTgt must not receive creates, got %+v", prefixTgt.created)
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }
