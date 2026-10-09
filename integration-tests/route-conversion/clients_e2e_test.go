//go:build e2e

package routeconversion

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// clientProc is one kafka-verifiable-* tool running as a subprocess of the
// test, its JSON lines going to logPath and its log4j output to errPath.
type clientProc struct {
	name    string
	logPath string
	errPath string
	cmd     *exec.Cmd
	done    chan error
	stopped bool
}

// startClient runs tool (on the runner image's PATH) with args.
func startClient(t *testing.T, dir, name, tool string, args ...string) *clientProc {
	t.Helper()
	p := &clientProc{name: name, logPath: filepath.Join(dir, name+".log"), errPath: filepath.Join(dir, name+".err"), done: make(chan error, 1)}
	stdout, err := os.Create(p.logPath)
	require.NoError(t, err)
	stderr, err := os.Create(p.errPath)
	require.NoError(t, err)
	p.cmd = exec.Command(tool, args...)
	p.cmd.Stdout, p.cmd.Stderr = stdout, stderr
	// Several JVMs share the runner pod; keep each one small.
	p.cmd.Env = append(os.Environ(), "KAFKA_HEAP_OPTS=-Xms64m -Xmx256m")
	require.NoErrorf(t, p.cmd.Start(), "start %s", name)
	go func() {
		p.done <- p.cmd.Wait()
		_ = stdout.Close()
		_ = stderr.Close()
	}()
	t.Logf("started %s: %s %s", name, tool, strings.Join(args, " "))
	return p
}

// stop sends SIGTERM (the tools close their client and log shutdown_complete)
// and waits up to 30s before killing. Idempotent.
func (p *clientProc) stop(t *testing.T) {
	t.Helper()
	if p.stopped {
		return
	}
	p.stopped = true
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(30 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
		t.Logf("WARN: %s did not stop on SIGTERM within 30s and was killed", p.name)
	}
}

// waitForLine waits until the client's log holds a line containing substr.
func (p *clientProc) waitForLine(t *testing.T, substr string, timeout time.Duration) {
	t.Helper()
	require.Eventuallyf(t, func() bool {
		raw, err := os.ReadFile(p.logPath)
		return err == nil && strings.Contains(string(raw), substr)
	}, timeout, time.Second, "%s never logged %q (see %s)", p.name, substr, p.errPath)
}

// destClientProps writes the client config for a client that talks to the
// destination directly (SASL_SSL PLAIN, the destination's own truststore) and
// returns its path. The credential comes from the pod env and the file stays
// in the test's folder inside the pod; run.sh copies it out with the reports,
// which are local and git-ignored.
func (e *env) destClientProps(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(destTruststorePassword)
	require.NoError(t, err, "read the destination truststore password")
	pw := strings.TrimSpace(string(raw))
	if i := strings.Index(pw, "="); i >= 0 { // CFK writes jksPassword=<password>
		pw = pw[i+1:]
	}
	props := fmt.Sprintf("security.protocol=SASL_SSL\nsasl.mechanism=PLAIN\n"+
		"sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username=\"%s\" password=\"%s\";\n"+
		"ssl.truststore.location=%s\nssl.truststore.password=%s\nssl.endpoint.identification.algorithm=\n",
		e.saslUser, e.saslPassword, destTruststore, pw)
	path := filepath.Join(dir, "dest-client.properties")
	require.NoError(t, os.WriteFile(path, []byte(props), 0o600))
	return path
}

// startConsumer runs a classic-protocol kafka-verifiable-consumer member of
// group on topic, reading from the earliest offset and logging every record.
func (e *env) startConsumer(t *testing.T, dir, bootstrap, props, group, topic, member string, autoCommit bool) *clientProc {
	t.Helper()
	args := []string{"--bootstrap-server", bootstrap, "--consumer.config", props, "--group-protocol", "classic",
		"--group-id", group, "--topic", topic, "--reset-policy", "earliest", "--verbose"}
	if autoCommit {
		args = append(args, "--enable-autocommit")
	}
	return startClient(t, dir, "consumer-"+group+"-"+member, "kafka-verifiable-consumer", args...)
}

// gatewayClientProps writes the client config for a client that talks to the
// gateway (TLS to the gateway's self-signed certificate; the gateway swaps in
// the destination credential) and returns its path.
func (e *env) gatewayClientProps(t *testing.T, dir string) string {
	t.Helper()
	props := fmt.Sprintf("security.protocol=SSL\nssl.truststore.location=%s\nssl.truststore.password=%s\n",
		gatewayTruststore, gatewayTruststorePassword)
	path := filepath.Join(dir, "gateway-client.properties")
	require.NoError(t, os.WriteFile(path, []byte(props), 0o600))
	return path
}

// producerRate is each producer's throughput, in records per second.
const producerRate = 20

// startProducer runs a kafka-verifiable-producer on topic through the gateway:
// the default (idempotent) producer, acks=all, retries never exhausted within
// the run, values "<prefix>.<n>".
func (e *env) startProducer(t *testing.T, dir, topic string, prefix int) *clientProc {
	t.Helper()
	props := filepath.Join(dir, "producer-"+topic+".properties")
	require.NoError(t, os.WriteFile(props, []byte(fmt.Sprintf(
		"security.protocol=SSL\nssl.truststore.location=%s\nssl.truststore.password=%s\n"+
			"acks=all\nretries=2147483647\ndelivery.timeout.ms=300000\n",
		gatewayTruststore, gatewayTruststorePassword)), 0o600))
	return startClient(t, dir, "producer-"+topic, "kafka-verifiable-producer",
		"--bootstrap-server", e.gatewayBootstrap, "--producer.config", props, "--topic", topic,
		"--throughput", fmt.Sprint(producerRate), "--max-messages", "-1", "--value-prefix", fmt.Sprint(prefix))
}
