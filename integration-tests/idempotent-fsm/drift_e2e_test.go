//go:build e2e

package idempotent_fsm_e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// TestDrift_StalledSwitchRerunIsRefused is the regression test for the
// Gateway drift bug (design: ~/claude-plans/kcp/2026-10-23-gateway-cr-pod-sync-design.md
// §8.1). Run 1 is killed after promote. The CFK operator is then stopped, so
// run 2's switchover patch lands on the CR but never reaches the pods: the
// switch times out waiting for the operator, past promote, so nothing rolls
// back. The CR now says switched while the pods still serve the fence, and
// clients can't produce. Before the pod check, a re-run read the CR alone,
// found nothing to do and exited 0. Now the pod check sees the operator hasn't
// caught up with the CR, so the dry-run and the re-run refuse, and the re-run
// makes no gateway write. Static only. Slice tbm-topic-001..005.
func TestDrift_StalledSwitchRerunIsRefused(t *testing.T) {
	e := newEnv()
	if e.mode != "static" {
		t.Skip("static routes only: the reproduction takes the AAO rollout-verification path")
	}
	ctx := context.Background()
	e.resetStaticRoute(t, ctx)
	topics := e.topicRange(1, 5)
	probe := topics[0]
	mani := e.writeManifest(t, "drift-stalled-switch", topics)
	e.saveManifest(t, mani)

	// The reset rolls the gateway pods, and an old pod may still serve the
	// previous test's switched route for a while, so retry until produce
	// reaches the source.
	var (
		acks     int
		firstErr error
	)
	deadline := time.Now().Add(3 * time.Minute)
	for {
		acks, firstErr = produceThroughGateway(t, e.gatewayBootstrap(), probe, 5)
		if acks >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Second)
	}
	require.GreaterOrEqualf(t, acks, 1, "the gateway must carry produce traffic before the migration, or the fenced-probe below proves nothing (first error: %v)", firstErr)
	e.snapshot(t, ctx, "before", "BEFORE (expect mirrors ACTIVE, route → source, produce through the gateway works)", topics)

	out1, err1 := e.runKCP(t, "kcp-run-1-interrupted.log", cpPromoted, "migration", "execute", "--migration-yaml", mani)
	require.Error(t, err1, "the run interrupted after promote must exit non-zero")
	require.Contains(t, out1, "kill-point", "the non-zero exit must be the kill point firing, not a real failure")
	cr := e.readCR(t, ctx)
	ms := e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Truef(t, e.isFenced(t, cr, tp), "%s must be fenced — the interrupt came after promote, before switch", tp)
		require.Equalf(t, "STOPPED", ms[tp], "%s must be STOPPED — the interrupt came after promote", tp)
	}
	e.snapshot(t, ctx, "after-promote", "AFTER interrupt at promoted (expect fenced, mirrors STOPPED)", topics)

	e.stopOperator(t, ctx)
	gen0, obs0 := e.crGenerations(t, ctx)
	require.Equal(t, gen0, obs0, "the operator must have observed the fenced CR before it stopped")
	depGen0, err := e.svc.GetGatewayDeploymentGeneration(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	uids0, err := e.svc.GetGatewayPodUIDs(ctx, e.namespace, e.gateway)
	require.NoError(t, err)

	out2, err2 := e.runKCP(t, "kcp-run-2-switch-stalls.log", "", "migration", "execute", "--migration-yaml", mani, "--rollout-timeout", "45s")
	require.Error(t, err2, "the switch must fail: the operator never observes it")
	require.Contains(t, out2, "Switchover gateway CR applied", "run 2 must reach the switch and patch the CR")
	require.Contains(t, out2, "to be observed by the operator", "the switch must fail waiting for the operator")
	require.NotContains(t, out2, "removing fence", "past promote there is no rollback")
	cr = e.readCR(t, ctx)
	for _, tp := range topics {
		require.Truef(t, e.isSwitchedToTarget(t, cr, tp), "%s must be switched in the CR", tp)
		require.Falsef(t, e.isFenced(t, cr, tp), "%s must be unfenced in the CR", tp)
	}
	gen1, obs1 := e.crGenerations(t, ctx)
	require.Greater(t, gen1, gen0, "the switch patch must bump the CR's generation")
	require.Equal(t, obs0, obs1, "the stopped operator must not observe the switch")
	depGen1, err := e.svc.GetGatewayDeploymentGeneration(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	require.Equal(t, depGen0, depGen1, "the gateway Deployment must be untouched")
	uids1, err := e.svc.GetGatewayPodUIDs(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	// No new pod, rather than the same set: a pod still terminating from run
	// 1's fence roll can be in uids0 and gone by now.
	for uid := range uids1 {
		_, ok := uids0[uid]
		require.Truef(t, ok, "gateway pod %s is new, but no pod may start while the operator is stopped: the pods must still hold the fenced config", uid)
	}
	e.snapshot(t, ctx, "after-stalled-switch", "AFTER the stalled switch (CR switched and unfenced; pods unchanged)", topics)

	acks, firstErr = produceThroughGateway(t, e.gatewayBootstrap(), probe, 5)
	require.Zerof(t, acks, "the pods must still serve the fence even though the CR says switched")
	t.Logf("\n🔍 DATA PLANE ▸ 0/5 produce attempts acked through %s; first error: %v", e.gatewayBootstrap(), firstErr)

	out3, err3 := e.runKCP(t, "kcp-run-3-dry-run.log", "", "migration", "execute", "--migration-yaml", mani, "--dry-run")
	require.Error(t, err3, "the dry-run must refuse: the pods may not serve the CR")
	require.Contains(t, out3, "gateway pods serve the CR", "the dry-run must name the failed pod check")
	require.Contains(t, out3, "the operator has not caught up with the CR", "the dry-run must say the operator is behind the CR")

	out4, err4 := e.runKCP(t, "kcp-run-4-rerun.log", "", "migration", "execute", "--migration-yaml", mani)
	require.Error(t, err4, "the re-run must refuse: the pods may not serve the CR")
	require.Contains(t, out4, "reconcile plan refused", "the re-run must exit on the refusal")
	require.NotContains(t, out4, fsmStartBanner, "no state machine may run on a refused plan")
	gen2, obs2 := e.crGenerations(t, ctx)
	require.Equal(t, gen1, gen2, "the re-run must make no gateway write")
	require.Equal(t, obs1, obs2, "the operator is still stopped")
	acks, firstErr = produceThroughGateway(t, e.gatewayBootstrap(), probe, 5)
	require.Zerof(t, acks, "clients are still fenced: the refusal changes nothing (first error: %v)", firstErr)
	e.snapshot(t, ctx, "after-rerun", "AFTER the refused re-run (CR switched; pods still fenced; nothing written)", topics)

	t.Logf("\n✅ RESULT: drift refused — the CR is switched at generation %d, the operator last observed generation %d, "+
		"and the dry-run and the re-run both refused without writing to the gateway.", gen1, obs1)
}

// TestDrift_TerminatingPodRefusesUntilSettled pins the pod check's refusal
// while a replaced gateway pod is still terminating (design §5.2). On a
// hot-reload-off gateway the fence's rollout reports complete while the old
// pod terminates for up to its grace period, and that pod may still serve
// the unfenced config. A dry-run straight after the fenced run must refuse
// with the rollout-in-progress hint; once the gateway settles, the same
// dry-run must pass. The test never promotes, but it still reserves its slice
// so its plan is deterministic. Static only. Slice tbm-topic-091..095.
func TestDrift_TerminatingPodRefusesUntilSettled(t *testing.T) {
	e := newEnv()
	if e.mode != "static" {
		t.Skip("static routes only: a hot-reload gateway replaces no pods")
	}
	ctx := context.Background()
	e.resetStaticRoute(t, ctx)
	topics := e.topicRange(91, 95)
	mani := e.writeManifest(t, "drift-terminating-pod", topics)
	e.saveManifest(t, mani)
	e.snapshot(t, ctx, "before", "BEFORE (expect mirrors ACTIVE, route → source)", topics)

	out1, err1 := e.runKCP(t, "kcp-run-1-interrupted.log", cpFenced, "migration", "execute", "--migration-yaml", mani)
	require.Error(t, err1, "the run interrupted after the fence must exit non-zero")
	require.Contains(t, out1, "kill-point", "the non-zero exit must be the kill point firing, not a real failure")

	out2, err2 := e.runKCPUnsettled(t, "kcp-run-2-dry-run-unsettled.log", "migration", "execute", "--migration-yaml", mani, "--dry-run")
	require.Error(t, err2, "the dry-run must refuse while a replaced gateway pod is terminating")
	require.Contains(t, out2, "is still terminating", "the dry-run must name the terminating pod")
	require.Contains(t, out2, "If it completes, re-run.", "the dry-run must carry the rollout-in-progress hint")
	e.snapshot(t, ctx, "after-fence", "AFTER interrupt at fenced (expect fenced, mirrors ACTIVE)", topics)

	e.waitForGatewaySettled(t, ctx)
	out3, err3 := e.runKCP(t, "kcp-run-3-dry-run-settled.log", "", "migration", "execute", "--migration-yaml", mani, "--dry-run")
	require.NoError(t, err3, "once the gateway settles the dry-run must pass")
	require.NotContains(t, out3, "is still terminating", "no gateway pod may be terminating once settled")

	t.Logf("\n✅ RESULT: a dry-run refused while a replaced gateway pod terminated, and passed once the gateway settled.")
}

// The CFK operator, as e2e_install_operator installs it
// (integration-tests/lib/cp-e2e-env.sh): Helm release confluent-operator, its
// pods labelled app=confluent-operator.
const (
	operatorDeployment = "confluent-operator"
	operatorPodLabel   = "app=confluent-operator"
)

// kubeClient is a clientset for the runner pod's service account.
func kubeClient(t *testing.T) kubernetes.Interface {
	t.Helper()
	cfg, err := rest.InClusterConfig()
	require.NoError(t, err, "in-cluster config (the suite runs in the runner pod)")
	cs, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err, "build the clientset")
	return cs
}

// scaleOperator sets the CFK operator Deployment's replicas.
func (e *env) scaleOperator(t *testing.T, ctx context.Context, cs kubernetes.Interface, replicas int32) {
	t.Helper()
	deps := cs.AppsV1().Deployments(e.namespace)
	scale, err := deps.GetScale(ctx, operatorDeployment, metav1.GetOptions{})
	require.NoError(t, err, "read the operator's scale")
	scale.Spec.Replicas = replicas
	_, err = deps.UpdateScale(ctx, operatorDeployment, scale, metav1.UpdateOptions{})
	require.NoErrorf(t, err, "scale the operator to %d", replicas)
}

// operatorPods lists the CFK operator's pods, terminating ones included. It
// returns the error rather than failing the test, because it's polled from
// require.Eventually, which runs its condition off the test goroutine.
func (e *env) operatorPods(ctx context.Context, cs kubernetes.Interface) ([]corev1.Pod, error) {
	pods, err := cs.CoreV1().Pods(e.namespace).List(ctx, metav1.ListOptions{LabelSelector: operatorPodLabel})
	if err != nil {
		return nil, err
	}
	return pods.Items, nil
}

// stopOperator scales the CFK operator to 0 and waits until none of its pods
// is listed, so not even a terminating pod can still reconcile a CR change.
// The gateway pods keep running, but no change to the gateway CR reaches
// them. When the test ends it scales the operator back to 1, waits for it to
// be Ready, then waits for it to accept the gateway CR, so the next test
// starts from a settled gateway.
func (e *env) stopOperator(t *testing.T, ctx context.Context) {
	t.Helper()
	cs := kubeClient(t)
	e.scaleOperator(t, ctx, cs, 0)
	t.Cleanup(func() {
		ctx := context.Background()
		e.scaleOperator(t, ctx, cs, 1)
		require.Eventually(t, func() bool {
			pods, err := e.operatorPods(ctx, cs)
			if err != nil {
				return false
			}
			for _, p := range pods {
				for _, c := range p.Status.Conditions {
					if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
						return true
					}
				}
			}
			return false
		}, 3*time.Minute, 2*time.Second, "the operator must come back Ready")
		require.NoError(t, e.svc.WaitForGatewayAccepted(ctx, e.namespace, e.gateway, 2*time.Second, 3*time.Minute),
			"the restored operator must accept the gateway CR")
		t.Logf("\n🧹 CFK OPERATOR RESTORED ▸ %s scaled back to 1 and the gateway CR accepted", operatorDeployment)
	})
	require.Eventually(t, func() bool {
		pods, err := e.operatorPods(ctx, cs)
		return err == nil && len(pods) == 0
	}, 2*time.Minute, 2*time.Second, "every operator pod must be gone")
	t.Logf("\n✍️  CFK OPERATOR STOPPED ▸ %s scaled to 0, no operator pod left: gateway CR changes no longer reach the pods", operatorDeployment)
}

// crGenerations reads the live gateway CR's metadata.generation and
// status.observedGeneration: the spec revision the CR holds, and the last one
// the operator observed.
func (e *env) crGenerations(t *testing.T, ctx context.Context) (generation, observed int64) {
	t.Helper()
	raw, err := e.svc.GetGatewayYAML(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	var cr struct {
		Metadata struct {
			Generation *int64 `yaml:"generation"`
		} `yaml:"metadata"`
		Status struct {
			ObservedGeneration *int64 `yaml:"observedGeneration"`
		} `yaml:"status"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &cr), "parse the gateway CR")
	require.NotNil(t, cr.Metadata.Generation, "the gateway CR must carry metadata.generation")
	require.NotNil(t, cr.Status.ObservedGeneration, "the gateway CR must carry status.observedGeneration")
	return *cr.Metadata.Generation, *cr.Status.ObservedGeneration
}

// gatewayBootstrap is the static route's client endpoint, as
// testdata/manifests/templates/gateway-static.yaml declares it.
func (e *env) gatewayBootstrap() string {
	return fmt.Sprintf("%s.%s.svc.cluster.local:9595", e.gateway, e.namespace)
}

// produceThroughGateway sends attempts messages to topic through the gateway
// and counts the acknowledged ones. It never fails the test: a producer that
// can't be created counts as no acks. firstErr is the first failure: after a
// few, sarama's circuit breaker fails later sends without reaching the gateway
// (ErrBreakerOpen). The config matches the migration-hot-reload suite's
// startProducerProbe: no retries, so a send is one attempt against whatever
// the gateway serves right now.
func produceThroughGateway(t *testing.T, bootstrap, topic string, attempts int) (acks int, firstErr error) {
	t.Helper()
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V3_6_0_0
	cfg.ClientID = "kcp-drift-probe"
	cfg.Producer.Return.Successes = true
	cfg.Producer.RequiredAcks = sarama.WaitForAll
	cfg.Producer.Retry.Max = 0
	cfg.Metadata.Retry.Max = 0
	cfg.Producer.Timeout = 3 * time.Second
	cfg.Metadata.Full = false
	cfg.Net.DialTimeout = 3 * time.Second
	cfg.Net.ReadTimeout = 3 * time.Second
	cfg.Net.WriteTimeout = 3 * time.Second

	p, err := sarama.NewSyncProducer([]string{bootstrap}, cfg)
	if err != nil {
		return 0, err
	}
	defer func() { _ = p.Close() }()
	for i := 0; i < attempts; i++ {
		_, _, err := p.SendMessage(&sarama.ProducerMessage{
			Topic: topic,
			Value: sarama.StringEncoder(time.Now().UTC().Format(time.RFC3339Nano)),
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		acks++
	}
	return acks, firstErr
}
