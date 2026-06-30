package mirrortopics

import (
	"context"
	"strings"
	"testing"
	"time"

	svclink "github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/reconcile"
)

// retryTarget fails CreateMirrorTopic with failErr for its first failTimes calls,
// then succeeds; it counts attempts. Used to exercise createMirror's retry.
type retryTarget struct {
	fakeLinkTarget
	failTimes int
	failErr   error
	attempts  int
}

func (f *retryTarget) CreateMirrorTopic(ctx context.Context, name, sourceTopic, mirrorTopic string) error {
	f.attempts++
	if f.attempts <= f.failTimes {
		return f.failErr
	}
	return nil
}

// shrinkMirrorRetry shrinks the createMirror retry timing for fast tests and
// restores it on cleanup.
func shrinkMirrorRetry(t *testing.T) {
	to, bo := mirrorCreateRetryTimeout, mirrorCreateRetryBackoff
	t.Cleanup(func() { mirrorCreateRetryTimeout, mirrorCreateRetryBackoff = to, bo })
	mirrorCreateRetryTimeout, mirrorCreateRetryBackoff = 2*time.Second, time.Millisecond
}

// TestApply_RetriesLinkNotReadable: a mirror create that hits the transient
// "Unable to resolve cluster link information" (link just created, not yet
// readable) is retried until it succeeds — a single apply stays reliable.
func TestApply_RetriesLinkNotReadable(t *testing.T) {
	shrinkMirrorRetry(t)
	tgt := &retryTarget{
		fakeLinkTarget: fakeLinkTarget{clusterID: "dest-1", configs: map[string]string{linkConfigPrefix: "mt."}},
		failTimes:      3,
		failErr:        errStub("unexpected status code 404: {\"message\":\"Unable to resolve cluster link information for mt.orders\"}"),
	}
	r := New(Config{LinkName: "lk", Include: []string{"*"}}, &fakeSource{topics: []string{"orders"}}, tgt, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	out, err := r.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply: want success after retries, got %v", err)
	}
	if len(out.Created) != 1 || len(out.Failed) != 0 {
		t.Errorf("want 1 created / 0 failed, got %d created / %d failed", len(out.Created), len(out.Failed))
	}
	if tgt.attempts != 4 {
		t.Errorf("want 4 attempts (3 transient + 1 success), got %d", tgt.attempts)
	}
}

// TestApply_NonTransientFailsFast: a non-transient create error (e.g. a missing
// source topic) is NOT retried — it fails fast and is reported per-topic.
func TestApply_NonTransientFailsFast(t *testing.T) {
	shrinkMirrorRetry(t)
	tgt := &retryTarget{
		fakeLinkTarget: fakeLinkTarget{clusterID: "dest-1", configs: map[string]string{linkConfigPrefix: "mt."}},
		failTimes:      100, // would fail forever if retried
		failErr:        errStub("unexpected status code 404: {\"message\":\"source topic does not exist\"}"),
	}
	r := New(Config{LinkName: "lk", Include: []string{"*"}}, &fakeSource{topics: []string{"orders"}}, tgt, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	out, err := r.Apply(context.Background(), p)
	if err == nil {
		t.Fatalf("Apply: want error for non-transient failure")
	}
	if len(out.Failed) != 1 {
		t.Errorf("want 1 failed, got %d", len(out.Failed))
	}
	if tgt.attempts != 1 {
		t.Errorf("non-transient error must not be retried: want 1 attempt, got %d", tgt.attempts)
	}
}

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

	// Collision-detection scripting (all optional; nil → no collisions):
	topics        []string                         // ListTopics: every dest topic
	topicsErr     error                            //
	links         []string                         // ListClusterLinks: all links
	linksErr      error                            //
	mirrorsByLink map[string][]svclink.MirrorTopic // per-link mirrors; falls back to .mirrors when nil
}

func (f *fakeLinkTarget) ClusterID(ctx context.Context) (string, error) {
	return f.clusterID, f.clusterIDErr
}

func (f *fakeLinkTarget) GetClusterLinkConfigs(ctx context.Context, name string) (map[string]string, error) {
	return f.configs, f.configsErr
}

func (f *fakeLinkTarget) ListMirrorTopics(ctx context.Context, name string) ([]svclink.MirrorTopic, error) {
	if f.mirrorsErr != nil {
		return nil, f.mirrorsErr
	}
	if f.mirrorsByLink != nil {
		return f.mirrorsByLink[name], nil
	}
	return f.mirrors, nil
}

func (f *fakeLinkTarget) ListTopics(ctx context.Context) ([]string, error) {
	return f.topics, f.topicsErr
}

func (f *fakeLinkTarget) ListClusterLinks(ctx context.Context) ([]string, error) {
	return f.links, f.linksErr
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

func TestPlan_CollidesWithForeignMirror(t *testing.T) {
	// No prefix → mirror name == source "orders-1", already a mirror on a DIFFERENT
	// link ("other-link"). Plan must report drift naming that link, not a create.
	src := &fakeSource{topics: []string{"orders-1"}}
	tgt := &fakeLinkTarget{
		clusterID: "dest-1",
		links:     []string{"link-a", "other-link"},
		mirrorsByLink: map[string][]svclink.MirrorTopic{
			"other-link": {{MirrorTopicName: "orders-1", MirrorStatus: "ACTIVE"}},
		},
		topics: []string{"orders-1"}, // a mirror is also a topic on the dest
	}
	r := New(Config{LinkName: "link-a", Include: []string{"*"}}, src, tgt, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	changes := p.Changes()
	if len(changes) != 1 {
		t.Fatalf("want 1 change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Action != reconcile.ActionDrift {
		t.Errorf("want Drift, got %v", changes[0].Action)
	}
	if !strings.Contains(changes[0].Detail, `already a mirror on cluster link "other-link"`) {
		t.Errorf("detail should name the owning link, got %q", changes[0].Detail)
	}
	if !p.Empty() {
		t.Errorf("a drift-only plan must be Empty (creates nothing)")
	}
}

func TestPlan_CollidesWithPlainTopic(t *testing.T) {
	// No prefix → mirror name "orders-1" is already a PLAIN topic on the dest (not
	// a mirror on any link). Plan must report drift, not a create.
	src := &fakeSource{topics: []string{"orders-1"}}
	tgt := &fakeLinkTarget{
		clusterID: "dest-1",
		links:     []string{"link-a"}, // only our link, with no mirrors
		topics:    []string{"orders-1", "unrelated"},
	}
	r := New(Config{LinkName: "link-a", Include: []string{"*"}}, src, tgt, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	changes := p.Changes()
	if len(changes) != 1 {
		t.Fatalf("want 1 change, got %d", len(changes))
	}
	if changes[0].Action != reconcile.ActionDrift {
		t.Errorf("want Drift, got %v", changes[0].Action)
	}
	if !strings.Contains(changes[0].Detail, "plain (non-mirror) topic") {
		t.Errorf("detail should identify a plain topic, got %q", changes[0].Detail)
	}
}

func TestPlan_OwnMirrorNotFlaggedAsCollision(t *testing.T) {
	// Our own mirror is also a topic in ListTopics; the per-link status check must
	// win → Present, never a plain/foreign collision.
	src := &fakeSource{topics: []string{"orders-1"}}
	tgt := &fakeLinkTarget{
		clusterID: "dest-1",
		links:     []string{"link-a"},
		mirrorsByLink: map[string][]svclink.MirrorTopic{
			"link-a": {{MirrorTopicName: "orders-1", MirrorStatus: "ACTIVE"}},
		},
		topics: []string{"orders-1"},
	}
	r := New(Config{LinkName: "link-a", Include: []string{"*"}}, src, tgt, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if got := summaryActions(p.Changes())[`mirror topic "orders-1"`]; got != reconcile.ActionPresent {
		t.Errorf("own active mirror must be Present, got %v", got)
	}
}

func TestPlan_DriftOnInactiveMirror(t *testing.T) {
	// The manifest selects source "orders" (→ expected mirror "dc-orders"). The
	// mirror exists but is PAUSED (tampered with out-of-band) → Drift (report
	// only), not Present/Create.
	src := &fakeSource{topics: []string{"orders"}}
	tgt := &fakeLinkTarget{
		clusterID: "dest-1",
		configs:   map[string]string{linkConfigPrefix: "dc-"},
		mirrors:   []svclink.MirrorTopic{{MirrorTopicName: "dc-orders", SourceTopicName: "orders", MirrorStatus: "PAUSED"}},
	}
	r := New(Config{LinkName: "lk", Include: []string{"*"}}, src, tgt, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	changes := p.Changes()
	if len(changes) != 1 {
		t.Fatalf("want 1 change, got %d", len(changes))
	}
	c := changes[0]
	if c.Action != reconcile.ActionDrift {
		t.Errorf("want Drift, got %v", c.Action)
	}
	if !strings.Contains(c.Detail, `"PAUSED"`) || !strings.Contains(c.Detail, "expected ACTIVE") {
		t.Errorf("drift detail should name the live status and expected ACTIVE, got %q", c.Detail)
	}
	if !p.Empty() {
		t.Errorf("a drift-only plan has no creates, so it must be Empty")
	}

	// Apply records the drift report-only: no mirror created, drift surfaced.
	out, err := r.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(tgt.created) != 0 {
		t.Errorf("drift must never create a mirror, got %v", tgt.created)
	}
	if len(out.Drift) != 1 || len(out.Created) != 0 || len(out.Present) != 0 {
		t.Errorf("want 1 drift / 0 created / 0 present, got %d/%d/%d",
			len(out.Drift), len(out.Created), len(out.Present))
	}
}

func TestPlan_PresentWhenActiveOrUnknownStatus(t *testing.T) {
	// An ACTIVE mirror and a mirror whose status the broker didn't report (empty)
	// are both Present — the empty case must NOT be fabricated into drift.
	src := &fakeSource{topics: []string{"orders", "events"}}
	tgt := &fakeLinkTarget{
		clusterID: "dest-1",
		configs:   map[string]string{linkConfigPrefix: "dc-"},
		mirrors: []svclink.MirrorTopic{
			{MirrorTopicName: "dc-orders", SourceTopicName: "orders", MirrorStatus: "ACTIVE"},
			{MirrorTopicName: "dc-events", SourceTopicName: "events"}, // status unreported
		},
	}
	r := New(Config{LinkName: "lk", Include: []string{"*"}}, src, tgt, tgt)
	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	got := summaryActions(p.Changes())
	if act := got[`mirror topic "dc-orders"`]; act != reconcile.ActionPresent {
		t.Errorf("ACTIVE mirror → want Present, got %v", act)
	}
	if act := got[`mirror topic "dc-events"`]; act != reconcile.ActionPresent {
		t.Errorf("unknown-status mirror → want Present (no fabricated drift), got %v", act)
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

// TestPlan_LivePrefixWinsOverManifest proves that when the live link is readable
// its cluster.link.prefix is authoritative and overrides the manifest prefix
// (Config.Prefix) — correct in the drift edge-case where an immutable pre-existing
// link differs from an edited manifest.
func TestPlan_LivePrefixWinsOverManifest(t *testing.T) {
	src := &fakeSource{topics: []string{"orders"}}
	tgt := &fakeLinkTarget{
		clusterID: "dest-1",
		configs:   map[string]string{linkConfigPrefix: "live."},
	}
	r := New(Config{LinkName: "lk", Include: []string{"*"}, Prefix: "manifest."}, src, tgt, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	got := summaryActions(p.Changes())
	if act, ok := got[`mirror topic "live.orders"`]; !ok || act != reconcile.ActionCreate {
		t.Errorf("want Create for live.orders (live prefix wins), got %v (present=%v); all=%v", act, ok, got)
	}
	if _, ok := got[`mirror topic "manifest.orders"`]; ok {
		t.Errorf("manifest prefix must not be used when live link is readable")
	}
}

// TestPlan_FallsBackToManifestPrefixWhenLinkAbsent proves that when the live link
// does not exist yet (dry-run before the clusterLink reconciler creates it, or the
// brief post-create propagation window) the planner falls back to the manifest
// prefix — exactly what the link is/will-be created with — with no error.
func TestPlan_FallsBackToManifestPrefixWhenLinkAbsent(t *testing.T) {
	src := &fakeSource{topics: []string{"orders"}}
	tgt := &fakeLinkTarget{
		clusterID:  "dest-1",
		configsErr: errStub("unexpected status code 404: {\"error_code\":404,\"message\":\"The cluster link doesn't exist: Cluster link 'lk' does not exist.\"}"),
	}
	r := New(Config{LinkName: "lk", Include: []string{"*"}, Prefix: "mt."}, src, tgt, tgt)

	p, err := r.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: want no error (fallback to manifest prefix), got %v", err)
	}
	got := summaryActions(p.Changes())
	if act, ok := got[`mirror topic "mt.orders"`]; !ok || act != reconcile.ActionCreate {
		t.Errorf("want Create for mt.orders (manifest-prefix fallback), got %v (present=%v); all=%v", act, ok, got)
	}
}

// TestPlan_FailsFastOnOtherPrefixReadError proves a non-"does not exist"
// prefix-read error (e.g. 401) is returned immediately, with no fallback.
func TestPlan_FailsFastOnOtherPrefixReadError(t *testing.T) {
	src := &fakeSource{topics: []string{"orders"}}
	tgt := &fakeLinkTarget{
		clusterID:  "dest-1",
		configsErr: errStub("unexpected status code 401: unauthorized"),
	}
	r := New(Config{LinkName: "lk", Include: []string{"*"}, Prefix: "mt."}, src, tgt, tgt)

	if _, err := r.Plan(context.Background()); err == nil {
		t.Fatalf("Plan: want error (no fallback on non-\"does not exist\" error), got nil")
	}
}
