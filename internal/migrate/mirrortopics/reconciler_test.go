package mirrortopics

import (
	"context"
	"strings"
	"testing"
	"time"

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

// TestPlanApply_SpecialCharTopicName verifies a dotted source topic name flows
// through unchanged: the mirror name is prefix+source ("mt.orders.created.v2")
// and the create carries the original source topic ("orders.created.v2").
func TestPlanApply_SpecialCharTopicName(t *testing.T) {
	src := &fakeSource{topics: []string{"orders.created.v2"}}
	tgt := &fakeLinkTarget{
		clusterID: "dest-1",
		configs:   map[string]string{linkConfigPrefix: "mt."},
	}
	r := New(Config{LinkName: "lk", Include: []string{"*"}}, src, tgt, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	got := summaryActions(p.Changes())
	if act, ok := got[`mirror topic "mt.orders.created.v2"`]; !ok || act != reconcile.ActionCreate {
		t.Errorf("mt.orders.created.v2: want Create, got %v (present=%v); all=%v", act, ok, got)
	}

	if _, err := r.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(tgt.created) != 1 {
		t.Fatalf("want 1 created, got %d", len(tgt.created))
	}
	if tgt.created[0].mirror != "mt.orders.created.v2" || tgt.created[0].src != "orders.created.v2" {
		t.Errorf("special-char name not passed through verbatim, got %+v", tgt.created[0])
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }

// retryLinkTarget fails GetClusterLinkConfigs with the given error for the first
// failN calls, then returns configs. It satisfies linkTarget for the prefix-read
// retry tests; ListTopics et al. are not exercised here.
type retryLinkTarget struct {
	fakeLinkTarget
	failN   int
	failErr error
	calls   int
}

func (f *retryLinkTarget) GetClusterLinkConfigs(ctx context.Context, name string) (map[string]string, error) {
	f.calls++
	if f.calls <= f.failN {
		return nil, f.failErr
	}
	return f.configs, nil
}

// TestPlan_PrefixReadRetriesOnNotExist proves the source-initiated prefix read
// tolerates the transient "link does not exist" 404 that occurs immediately after
// the OUTBOUND link is created: the read is retried until the link config becomes
// readable, so a single apply succeeds without a manual re-run.
func TestPlan_PrefixReadRetriesOnNotExist(t *testing.T) {
	prefixReadRetryBackoff = time.Millisecond
	prefixReadRetryTimeout = 2 * time.Second
	defer func() {
		prefixReadRetryBackoff = 500 * time.Millisecond
		prefixReadRetryTimeout = 30 * time.Second
	}()

	src := &fakeSource{topics: []string{"orders"}}
	tgt := &retryLinkTarget{
		fakeLinkTarget: fakeLinkTarget{clusterID: "src-1", configs: map[string]string{linkConfigPrefix: "sp-"}},
		failN:          3,
		failErr:        errStub("unexpected status code 404: {\"error_code\":404,\"message\":\"The cluster link doesn't exist: Cluster link 'lk' does not exist.\"}"),
	}
	r := New(Config{LinkName: "lk", Include: []string{"*"}}, src, tgt, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if tgt.calls != 4 {
		t.Errorf("want 4 prefix-read attempts (3 transient + 1 success), got %d", tgt.calls)
	}
	got := summaryActions(p.Changes())
	if act, ok := got[`mirror topic "sp-orders"`]; !ok || act != reconcile.ActionCreate {
		t.Errorf("sp-orders: want Create with retried prefix, got %v (present=%v)", act, ok)
	}
}

// TestPlan_PrefixReadFailsFastOnOtherError proves a non-transient prefix-read
// error (not "does not exist") is returned immediately without retrying.
func TestPlan_PrefixReadFailsFastOnOtherError(t *testing.T) {
	prefixReadRetryBackoff = time.Millisecond
	prefixReadRetryTimeout = 2 * time.Second
	defer func() {
		prefixReadRetryBackoff = 500 * time.Millisecond
		prefixReadRetryTimeout = 30 * time.Second
	}()

	src := &fakeSource{topics: []string{"orders"}}
	tgt := &retryLinkTarget{
		fakeLinkTarget: fakeLinkTarget{clusterID: "src-1", configs: map[string]string{linkConfigPrefix: "sp-"}},
		failN:          99,
		failErr:        errStub("unexpected status code 401: unauthorized"),
	}
	r := New(Config{LinkName: "lk", Include: []string{"*"}}, src, tgt, tgt)

	if _, err := r.Plan(context.Background()); err == nil {
		t.Fatalf("Plan: want error, got nil")
	}
	if tgt.calls != 1 {
		t.Errorf("want exactly 1 attempt (fail-fast on non-transient error), got %d", tgt.calls)
	}
}
