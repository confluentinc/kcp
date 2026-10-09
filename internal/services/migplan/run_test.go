package migplan

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gm(name, target string, tgs []manifest.TopicGroupEntry) *manifest.GatewayMigration {
	g := &manifest.GatewayMigration{}
	g.Spec.Route = manifest.Route{Name: name, TopicGroup: tgs, TargetStreamingDomain: target}
	return g
}

func strs(s ...string) *[]string { return &s }

func TestBuildReconcileInput(t *testing.T) {
	in, err := buildReconcileInput(gm("migration-route", "cc", []manifest.TopicGroupEntry{{
		Topics:        strs("a", "b"),
		TopicPatterns: strs("team-.*"),
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if in.Route != "migration-route" || in.TargetDomain != "cc" {
		t.Errorf("route/target = %q/%q", in.Route, in.TargetDomain)
	}
	if len(in.Topics) != 2 || len(in.TopicPatterns) != 1 {
		t.Errorf("topics/patterns = %v / %v", in.Topics, in.TopicPatterns)
	}
	if in.PauseConsumerOffsetSync {
		t.Error("PauseConsumerOffsetSync must be false when the manifest does not opt in")
	}
}

func TestBuildReconcileInput_CarriesOffsetSyncPause(t *testing.T) {
	for _, c := range []struct {
		baseline    string
		wantEnabled bool
	}{
		{manifest.OffsetSyncBaselineEnabled, true},
		{manifest.OffsetSyncBaselineDisabled, false},
	} {
		t.Run(c.baseline, func(t *testing.T) {
			g := gm("migration-route", "cc", []manifest.TopicGroupEntry{{Topics: strs("a")}})
			g.Spec.ClusterLink.PauseConsumerOffsetSync = true
			g.Spec.ClusterLink.ConsumerOffsetSyncBaseline = c.baseline
			in, err := buildReconcileInput(g)
			if err != nil {
				t.Fatal(err)
			}
			if !in.PauseConsumerOffsetSync {
				t.Error("PauseConsumerOffsetSync must carry the manifest's opt-in")
			}
			if in.OffsetSyncBaselineEnabled != c.wantEnabled {
				t.Errorf("OffsetSyncBaselineEnabled = %v, want %v", in.OffsetSyncBaselineEnabled, c.wantEnabled)
			}
		})
	}
}

func TestBuildReconcileInputValidation(t *testing.T) {
	cases := []struct {
		name   string
		route  string
		target string
		tgs    []manifest.TopicGroupEntry
	}{
		{"zero topicGroups", "r", "cc", nil},
		{"more than one topicGroup", "r", "cc", []manifest.TopicGroupEntry{
			{Topics: strs("a")},
			{Topics: strs("b")},
		}},
		{"missing route", "", "cc", []manifest.TopicGroupEntry{{Topics: strs("a")}}},
		{"missing targetStreamingDomain", "r", "", []manifest.TopicGroupEntry{{Topics: strs("a")}}},
		{"no topics or patterns", "r", "cc", []manifest.TopicGroupEntry{{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := buildReconcileInput(gm(c.route, c.target, c.tgs)); err == nil {
				t.Errorf("%s must error", c.name)
			}
		})
	}
}

func TestNewResult(t *testing.T) {
	// success: artifacts mapped, not refused, no reasons
	ok := newResult(&reconcile.Plan{
		Report: reconcile.Report{Migratable: []reconcile.TopicVerdict{{Topic: "a"}}},
		Artifacts: &reconcile.Artifacts{
			PromoteTopics:      []string{"a", "b"},
			AwaitStopped:       []string{"b"},
			FenceRules:         []byte("fence-yaml"),
			SwitchoverRules:    []byte("switch-yaml"),
			RollbackFenceRules: []byte("rollback-fence-yaml"),
			RollbackAllowed:    true,
			FencedAtStart:      true,
			MigrateTopics:      []string{"a", "b", "c"},
		},
		GatewayYAML: "gw-yaml",
		Mode:        "static",
	})
	if ok.Refused {
		t.Error("a plan with artifacts must not be Refused")
	}
	if ok.FenceYAML != "fence-yaml" || ok.SwitchoverYAML != "switch-yaml" || ok.RollbackFenceYAML != "rollback-fence-yaml" {
		t.Errorf("artifact strings = %q / %q / %q", ok.FenceYAML, ok.SwitchoverYAML, ok.RollbackFenceYAML)
	}
	if !ok.RollbackAllowed {
		t.Error("RollbackAllowed must mirror the plan's")
	}
	if !ok.FencedAtStart {
		t.Error("FencedAtStart must mirror the plan's")
	}
	if len(ok.MigrateTopics) != 3 {
		t.Errorf("MigrateTopics = %v, want the plan's [a b c]", ok.MigrateTopics)
	}
	if ok.GatewayYAML != "gw-yaml" {
		t.Errorf("GatewayYAML = %q, want gw-yaml", ok.GatewayYAML)
	}
	if len(ok.PromoteTopics) != 2 || len(ok.Reasons) != 0 {
		t.Errorf("topics=%v reasons=%v", ok.PromoteTopics, ok.Reasons)
	}
	if len(ok.AwaitStopped) != 1 || ok.AwaitStopped[0] != "b" {
		t.Errorf("AwaitStopped = %v, want [b] (the resume await subset must map onto the Result)", ok.AwaitStopped)
	}
	if ok.Mode != "static" {
		t.Errorf("Mode = %q, want static (must mirror plan.Mode)", ok.Mode)
	}
	if ok.RestoreOffsetSync {
		t.Error("RestoreOffsetSync must be false when the report does not owe a restore")
	}
	if ok.NothingToDo {
		t.Error("a plan with artifacts must not be NothingToDo")
	}

	// nothing to do: the plan's verdict is carried onto the Result as-is.
	noop := newResult(&reconcile.Plan{
		Report:      reconcile.Report{Unchanged: []reconcile.TopicVerdict{{Topic: "a"}}},
		NothingToDo: true,
		Mode:        "dynamic",
	})
	if !noop.NothingToDo || noop.Refused {
		t.Errorf("NothingToDo=%v Refused=%v, want true/false", noop.NothingToDo, noop.Refused)
	}

	// a restore owed with no topic left: no artifacts, but the verdict is carried.
	restore := newResult(&reconcile.Plan{
		Report: reconcile.Report{Unchanged: []reconcile.TopicVerdict{{Topic: "a"}}, RestoreOffsetSync: true},
		Mode:   "static",
	})
	if !restore.RestoreOffsetSync || restore.Refused {
		t.Errorf("RestoreOffsetSync=%v Refused=%v, want true/false", restore.RestoreOffsetSync, restore.Refused)
	}
	if restore.FenceYAML != "" || restore.SwitchoverYAML != "" || len(restore.PromoteTopics) != 0 {
		t.Error("a restore-only plan must carry no fence, switchover or promote work")
	}

	// refused: nil artifacts, Refused true, reasons from failed checks + fail-fast,
	// but the pulled gateway YAML is still carried (the pull precedes the checks).
	ref := newResult(&reconcile.Plan{
		Report: reconcile.Report{
			Preconditions: []reconcile.PreconditionResult{{Name: "route is dynamic", OK: false, Detail: "is static"}},
			FailFast:      []reconcile.TopicVerdict{{Topic: "x", Reason: "not on the cluster link"}},
		},
		GatewayYAML: "gw-yaml",
	})
	if !ref.Refused {
		t.Error("a refused plan must have Refused=true")
	}
	if ref.FenceYAML != "" || ref.RollbackFenceYAML != "" || ref.RollbackAllowed || len(ref.PromoteTopics) != 0 {
		t.Error("a refused plan must carry no artifacts")
	}
	if ref.GatewayYAML != "gw-yaml" {
		t.Errorf("a refused plan must still carry the pulled GatewayYAML, got %q", ref.GatewayYAML)
	}
	if len(ref.Reasons) != 2 {
		t.Fatalf("expected 2 reasons, got %v", ref.Reasons)
	}
	if ref.Reasons[0] != "route is dynamic: is static" || ref.Reasons[1] != "x: not on the cluster link" {
		t.Errorf("reasons = %v", ref.Reasons)
	}
}

// homeWithFakeKubeconfig points $HOME at a temp dir whose .kube/config names an
// unreachable API server, and marks the process as not in a pod, so a client
// built from the home kubeconfig fails to connect rather than to configure.
func homeWithFakeKubeconfig(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".kube"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".kube", "config"), []byte(`apiVersion: v1
kind: Config
clusters:
- name: fake
  cluster:
    server: https://127.0.0.1:1
contexts:
- name: fake
  context: {cluster: fake, user: fake}
current-context: fake
users:
- name: fake
  user: {token: fake}
`), 0o600))
	t.Setenv("HOME", home)
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
}

// TestBuildGatewaySource_UnsetKubeconfigOffAPodUsesTheHomeKubeconfig: with
// spec.gateway.kubeconfig unset outside a pod, reconcile's gateway pull uses
// ~/.kube/config — it reaches for the API server named there — instead of
// failing to build a client at all.
func TestBuildGatewaySource_UnsetKubeconfigOffAPodUsesTheHomeKubeconfig(t *testing.T) {
	homeWithFakeKubeconfig(t)
	g := &manifest.GatewayMigration{}
	g.Spec.Gateway.Namespace = "confluent"
	g.Spec.Gateway.CrName = "my-gateway"

	src, err := buildGatewaySource(g, "migration-route")
	require.NoError(t, err)
	_, err = src.Load(context.Background())

	require.Error(t, err, "the fake API server is unreachable")
	assert.NotContains(t, err.Error(), "no configuration has been provided", "the home kubeconfig must be used")
	assert.Contains(t, err.Error(), "127.0.0.1:1", "the client must call the API server named in ~/.kube/config")
}

// TestBuildSecretExistenceChecker_UnsetKubeconfigOffAPodUsesTheHomeKubeconfig:
// the same default for reconcile's staged-secret check.
func TestBuildSecretExistenceChecker_UnsetKubeconfigOffAPodUsesTheHomeKubeconfig(t *testing.T) {
	homeWithFakeKubeconfig(t)
	g := &manifest.GatewayMigration{}
	g.Spec.Gateway.Namespace = "confluent"

	_, err := buildSecretExistenceChecker(g)
	require.NoError(t, err, "building the checker from ~/.kube/config must succeed")
}

func TestBuildReconcileInput_Conversion(t *testing.T) {
	g := gm("migration-route", "cc", nil)
	g.Spec.Route.ConvertTo = manifest.RouteConvertToStatic
	g.Spec.Target.ClusterID = "lkc-123"

	in, err := buildReconcileInput(g)

	require.NoError(t, err)
	assert.Equal(t, reconcile.ReconcileInput{
		Route: "migration-route", TargetDomain: "cc", TargetClusterID: "lkc-123",
		ConvertTo: "static", OffsetSyncBaselineEnabled: true,
	}, in)
}

func TestBuildReconcileInput_ConversionStillNeedsRouteAndTarget(t *testing.T) {
	for _, c := range []struct{ route, target string }{{"", "cc"}, {"r", ""}} {
		g := gm(c.route, c.target, nil)
		g.Spec.Route.ConvertTo = manifest.RouteConvertToStatic
		_, err := buildReconcileInput(g)
		assert.Error(t, err, "route=%q target=%q", c.route, c.target)
	}
}

// targetGM builds a manifest whose destination Kafka leg resolves from a
// credentials file with the given body.
func targetGM(t *testing.T, credsBody string) *manifest.GatewayMigration {
	t.Helper()
	p := filepath.Join(t.TempDir(), "dest-kafka-creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte(credsBody), 0600))
	g := gm("migration-route", "cc", nil)
	g.Spec.Target.Kafka = &manifest.TargetKafka{
		BootstrapServers:   []string{"pkc-1.example:9092"},
		ClusterCredentials: manifest.CredentialsRef{Path: p},
	}
	return g
}

// A Confluent Cloud destination over sasl_plain with neither ca_cert nor an
// explicit tls signal still dials SASL_SSL: targetConn forces UseTLS.
func TestTargetConn_SASLPlainWithoutCACertForcesTLS(t *testing.T) {
	conn, err := targetConn(targetGM(t, "sasl_plain:\n  username: CC_KEY\n  password: CC_SECRET\n"))
	require.NoError(t, err)
	require.NotNil(t, conn.AuthMethod.SASLPlain)
	assert.True(t, conn.AuthMethod.SASLPlain.UseTLS, "a sasl_plain destination with no ca_cert and no tls must dial TLS")
	assert.Equal(t, []string{"pkc-1.example:9092"}, conn.BootstrapServers)
}

// A sasl_plain destination with a ca_cert already selects TLS through the CA;
// targetConn leaves it exactly as configured.
func TestTargetConn_SASLPlainWithCACertIsLeftAlone(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "dest-ca.pem")
	require.NoError(t, os.WriteFile(ca, []byte("pem"), 0600))

	conn, err := targetConn(targetGM(t, "sasl_plain:\n  username: CC_KEY\n  password: CC_SECRET\n  ca_cert: "+ca+"\n"))
	require.NoError(t, err)
	require.NotNil(t, conn.AuthMethod.SASLPlain)
	assert.Equal(t, ca, conn.AuthMethod.SASLPlain.CACert)
	assert.False(t, conn.AuthMethod.SASLPlain.UseTLS, "a ca_cert destination must not have UseTLS forced on")
}

func TestBuildReconcileInput_IsTheInternalBuilder(t *testing.T) {
	g := gm("migration-route", "cc", nil)
	g.Spec.Route.ConvertTo = manifest.RouteConvertToStatic

	got, err := BuildReconcileInput(g)
	require.NoError(t, err)
	want, err := buildReconcileInput(g)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestBuildConvertProviders_ABadManifestIsAnErrorAndOpensNothing(t *testing.T) {
	homeWithFakeKubeconfig(t)
	g := gm("migration-route", "cc", nil)
	g.Spec.Route.ConvertTo = manifest.RouteConvertToStatic
	g.Spec.Gateway.Namespace = "confluent"
	g.Spec.Gateway.CrName = "my-gateway"

	_, closer, err := BuildConvertProviders(g)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "spec.target.kafka: required")
	assert.Nil(t, closer, "nothing is handed back to close on failure")
}

func TestClosers_ClosesEveryOneInReverseAndJoinsTheErrors(t *testing.T) {
	var order []string
	mk := func(name string, err error) io.Closer {
		return closerFunc(func() error { order = append(order, name); return err })
	}
	boom := errors.New("boom")
	err := closers{mk("a", nil), mk("b", boom), mk("c", nil)}.Close()
	assert.Equal(t, []string{"c", "b", "a"}, order)
	assert.ErrorIs(t, err, boom)
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

// Decision 22: Reconcile's conversion path builds its providers through
// BuildConvertProviders, so a manifest that cannot build them fails both the
// same way, before any live read.
func TestReconcile_AConversionBuildsItsProvidersLikeVerifyFence(t *testing.T) {
	homeWithFakeKubeconfig(t)
	g := gm("migration-route", "cc", nil)
	g.Spec.Route.ConvertTo = manifest.RouteConvertToStatic
	g.Spec.Gateway.Namespace = "confluent"
	g.Spec.Gateway.CrName = "my-gateway"

	_, _, wantErr := BuildConvertProviders(g)
	require.Error(t, wantErr)

	_, err := Reconcile(context.Background(), g, WithOutput(io.Discard))

	require.Error(t, err)
	assert.Equal(t, wantErr.Error(), err.Error())
}
