package apply

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/manifest"
	migrate "github.com/confluentinc/kcp/internal/migrate"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

type staticSource string

func (s staticSource) ClusterID(context.Context) (string, error) { return string(s), nil }

func (s staticSource) ListTopics(context.Context) ([]string, error) { return nil, nil }

func (s staticSource) DescribeTopics(context.Context, []string) ([]migrate.TopicSpec, error) {
	return nil, nil
}

// startStubTarget serves the minimal CP REST surface: list clusters + get/create link.
func startStubTarget(t *testing.T, linkExists bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/kafka/v3/clusters", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"cluster_id":"dest-1"}]}`))
	})
	mux.HandleFunc("/kafka/v3/clusters/dest-1/links/src-to-dest", func(w http.ResponseWriter, _ *http.Request) {
		if linkExists {
			_, _ = w.Write([]byte(`{"link_name":"src-to-dest","source_cluster_id":"src-1","link_state":"AVAILABLE"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/kafka/v3/clusters/dest-1/links/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	return httptest.NewServer(mux)
}

// run executes the apply command with a source whose ClusterID is faked via the
// newSourceReader package-level seam (see cmd implementation).
func run(t *testing.T, srvURL string, dryRun bool) (stdout, stderr string, err error) {
	t.Helper()
	dir := t.TempDir()
	targetCreds := filepath.Join(dir, "target.yaml")
	require.NoError(t, os.WriteFile(targetCreds, []byte("basic:\n  username: admin\n  password: admin-secret\n"), 0600))
	// Auth-only creds file: no bootstrap_servers (address is in the manifest).
	sourceCreds := filepath.Join(dir, "source.yaml")
	require.NoError(t, os.WriteFile(sourceCreds, []byte(
		"unauthenticated_plaintext: {}\n"), 0600))
	mf := filepath.Join(dir, "migration.yaml")
	require.NoError(t, os.WriteFile(mf, []byte(
		"apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: t\nspec:\n"+
			"  source:\n    type: apache-kafka\n    bootstrapServers: [\"source:29092\"]\n    credentials: "+sourceCreds+"\n"+
			"  target:\n    type: confluent-platform\n    credentials: "+targetCreds+"\n    kafka:\n      restEndpoint: "+srvURL+"\n"+
			"  clusterLink:\n    name: src-to-dest\n    source:\n      bootstrapServers: [\"source:29092\"]\n      credentials: "+sourceCreds+"\n"), 0600))

	old := newSourceReader
	newSourceReader = func(types.KafkaSourceConn) migrate.Source { return staticSource("src-1") }
	t.Cleanup(func() { newSourceReader = old })
	cmd := NewMigrateApplyCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	args := []string{"-f", mf}
	if dryRun {
		args = append(args, "--dry-run")
	}
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestApply_DryRun_PrintsPlanNoCreate(t *testing.T) {
	srv := startStubTarget(t, false)
	defer srv.Close()
	out, _, err := run(t, srv.URL, true)
	require.NoError(t, err)
	require.Contains(t, out, "cluster link \"src-to-dest\"")
	require.Contains(t, out, "Planned")
}

func TestApply_CreatesLink(t *testing.T) {
	srv := startStubTarget(t, false)
	defer srv.Close()
	out, _, err := run(t, srv.URL, false)
	require.NoError(t, err)
	require.Contains(t, out, "1 created")
}

func TestApply_AlreadyPresent(t *testing.T) {
	srv := startStubTarget(t, true)
	defer srv.Close()
	out, _, err := run(t, srv.URL, false)
	require.NoError(t, err)
	require.Contains(t, out, "1 already present")
}

// createCapture records the create requests seen by a stub link endpoint.
type createCapture struct {
	clusterID string
	bodies    []map[string]any
}

// startStubLinkEndpoint serves the minimal link REST surface (list clusters,
// get link → 404, create link → 201) and captures create bodies.
func startStubLinkEndpoint(t *testing.T, cap *createCapture) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/kafka/v3/clusters", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/kafka/v3/clusters" { // only the bare list
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"cluster_id":"` + cap.clusterID + `"}]}`))
	})
	// GET link → not found (so reconcile plans a create); POST create → 201.
	mux.HandleFunc("/kafka/v3/clusters/"+cap.clusterID+"/links/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(b, &body)
			cap.bodies = append(cap.bodies, body)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/kafka/v3/clusters/"+cap.clusterID+"/links/src-to-dest", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	return httptest.NewServer(mux)
}

func TestApply_SourceInitiated_CreatesBothSides(t *testing.T) {
	destCap := &createCapture{clusterID: "dest-1"}
	srcCap := &createCapture{clusterID: "src-rest-1"}
	destSrv := startStubLinkEndpoint(t, destCap)
	defer destSrv.Close()
	srcSrv := startStubLinkEndpoint(t, srcCap)
	defer srcSrv.Close()

	dir := t.TempDir()
	targetCreds := filepath.Join(dir, "target.yaml")
	require.NoError(t, os.WriteFile(targetCreds, []byte("basic:\n  username: admin\n  password: admin-secret\n"), 0600))
	srcRestCreds := filepath.Join(dir, "srcrest.yaml")
	require.NoError(t, os.WriteFile(srcRestCreds, []byte("basic:\n  username: src\n  password: src-secret\n"), 0600))
	// Auth-only creds files: no bootstrap_servers (addresses are in the manifest).
	sourceCreds := filepath.Join(dir, "source.yaml")
	require.NoError(t, os.WriteFile(sourceCreds, []byte(
		"unauthenticated_plaintext: {}\n"), 0600))
	destCreds := filepath.Join(dir, "dest.yaml")
	require.NoError(t, os.WriteFile(destCreds, []byte(
		"unauthenticated_plaintext: {}\n"), 0600))

	mf := filepath.Join(dir, "migration.yaml")
	require.NoError(t, os.WriteFile(mf, []byte(
		"apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: t\nspec:\n"+
			"  source:\n    type: confluent-platform\n    bootstrapServers: [\"source:29092\"]\n    credentials: "+sourceCreds+"\n"+
			"  target:\n    type: confluent-platform\n    credentials: "+targetCreds+"\n    kafka:\n      restEndpoint: "+destSrv.URL+"\n"+
			"  clusterLink:\n    name: src-to-dest\n    mode: source\n"+
			"    destination:\n      bootstrapServers: [\"dest:29092\"]\n      credentials: "+destCreds+"\n"+
			"    sourceRest:\n      endpoint: "+srcSrv.URL+"\n      credentials: "+srcRestCreds+"\n"), 0600))

	old := newSourceReader
	newSourceReader = func(types.KafkaSourceConn) migrate.Source { return staticSource("src-1") }
	t.Cleanup(func() { newSourceReader = old })

	cmd := NewMigrateApplyCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"-f", mf})
	require.NoError(t, cmd.Execute(), "stderr: %s", errBuf.String())

	out := outBuf.String()
	require.Contains(t, out, "2 created")

	// Destination side: created first, INBOUND, carries source_cluster_id, no bootstrap.
	require.Len(t, destCap.bodies, 1)
	destCfgs := configMap(destCap.bodies[0])
	require.Equal(t, "DESTINATION", destCfgs["link.mode"])
	require.Equal(t, "INBOUND", destCfgs["connection.mode"])
	require.Equal(t, "src-1", destCap.bodies[0]["source_cluster_id"])

	// Source side: OUTBOUND, dials the destination address, omits source_cluster_id.
	require.Len(t, srcCap.bodies, 1)
	srcCfgs := configMap(srcCap.bodies[0])
	require.Equal(t, "SOURCE", srcCfgs["link.mode"])
	require.Equal(t, "OUTBOUND", srcCfgs["connection.mode"])
	require.Equal(t, "dest:29092", srcCfgs["bootstrap.servers"])
	require.NotContains(t, srcCap.bodies[0], "source_cluster_id", "source-side link must omit source_cluster_id")
}

func configMap(body map[string]any) map[string]string {
	out := map[string]string{}
	raw, _ := body["configs"].([]any)
	for _, e := range raw {
		m := e.(map[string]any)
		out[m["name"].(string)] = m["value"].(string)
	}
	return out
}

// topicSource is a fake migrate.Source that reports a fixed cluster id and a
// fixed topic list, so the topic reconcilers plan a real create step.
type topicSource struct {
	id     string
	topics []string
}

func (s topicSource) ClusterID(context.Context) (string, error) { return s.id, nil }

func (s topicSource) ListTopics(context.Context) ([]string, error) { return s.topics, nil }

func (s topicSource) DescribeTopics(_ context.Context, names []string) ([]migrate.TopicSpec, error) {
	out := make([]migrate.TopicSpec, len(names))
	for i, n := range names {
		out[i] = migrate.TopicSpec{Name: n, Partitions: 3, ReplicationFactor: 3}
	}
	return out, nil
}

// startStubTopicTarget serves the CP REST surface needed by the topic
// reconcilers: list clusters, get/create link, list/create plain topics, and
// list/create mirror topics (plus the link configs read for the mirror prefix).
func startStubTopicTarget(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/kafka/v3/clusters", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/kafka/v3/clusters" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"cluster_id":"dest-1"}]}`))
	})
	// Plain topics: GET list (empty) / POST create → 201.
	mux.HandleFunc("/kafka/v3/clusters/dest-1/topics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	// Link configs (carries cluster.link.prefix) — empty prefix here.
	mux.HandleFunc("/kafka/v3/clusters/dest-1/links/src-to-dest/configs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	// Mirror topics: GET list (empty) / POST create → 201.
	mux.HandleFunc("/kafka/v3/clusters/dest-1/links/src-to-dest/mirrors", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	// Cluster-link get (for the clusterLink reconciler) → not found, so it plans a create.
	mux.HandleFunc("/kafka/v3/clusters/dest-1/links/src-to-dest", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	// Catch-all create-link POST (links/?link_name=...) → 201.
	mux.HandleFunc("/kafka/v3/clusters/dest-1/links/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	return httptest.NewServer(mux)
}

// runManifest writes the given manifest body + standard creds files and executes
// apply, stubbing newSourceReader with the supplied source.
func runManifest(t *testing.T, srvURL, specBody string, src migrate.Source, dryRun bool) (stdout, stderr string, err error) {
	t.Helper()
	dir := t.TempDir()
	targetCreds := filepath.Join(dir, "target.yaml")
	require.NoError(t, os.WriteFile(targetCreds, []byte("basic:\n  username: admin\n  password: admin-secret\n"), 0600))
	sourceCreds := filepath.Join(dir, "source.yaml")
	require.NoError(t, os.WriteFile(sourceCreds, []byte("unauthenticated_plaintext: {}\n"), 0600))
	mf := filepath.Join(dir, "migration.yaml")
	body := os.Expand(specBody, func(k string) string {
		switch k {
		case "SRV":
			return srvURL
		case "SOURCE_CREDS":
			return sourceCreds
		case "TARGET_CREDS":
			return targetCreds
		}
		return ""
	})
	require.NoError(t, os.WriteFile(mf, []byte(body), 0600))

	old := newSourceReader
	newSourceReader = func(types.KafkaSourceConn) migrate.Source { return src }
	t.Cleanup(func() { newSourceReader = old })

	cmd := NewMigrateApplyCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	args := []string{"-f", mf}
	if dryRun {
		args = append(args, "--dry-run")
	}
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// mode:mirror — the mirrorTopics reconciler is appended AFTER the clusterLink
// reconciler and runs against the target.
func TestApply_TopicsMirror_AppendsAfterClusterLink(t *testing.T) {
	srv := startStubTopicTarget(t)
	defer srv.Close()
	spec := "apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: t\nspec:\n" +
		"  source:\n    type: apache-kafka\n    bootstrapServers: [\"source:29092\"]\n    credentials: ${SOURCE_CREDS}\n" +
		"  target:\n    type: confluent-platform\n    credentials: ${TARGET_CREDS}\n    kafka:\n      restEndpoint: ${SRV}\n" +
		"  clusterLink:\n    name: src-to-dest\n    source:\n      bootstrapServers: [\"source:29092\"]\n      credentials: ${SOURCE_CREDS}\n" +
		"  topics:\n    mode: mirror\n    include: [\"orders\"]\n"
	out, errOut, err := runManifest(t, srv.URL, spec, topicSource{id: "src-1", topics: []string{"orders"}}, false)
	require.NoError(t, err, "stderr: %s", errOut)
	require.Contains(t, out, "cluster link \"src-to-dest\"")
	require.Contains(t, out, "== mirrorTopics")
	// clusterLink section is rendered before mirrorTopics (ordering precondition).
	require.Greater(t, strings.Index(out, "== mirrorTopics"), strings.Index(out, "cluster link"))
}

// mode:new — the newTopics reconciler runs with NO clusterLink and does not error.
func TestApply_TopicsNew_NoClusterLink(t *testing.T) {
	srv := startStubTopicTarget(t)
	defer srv.Close()
	spec := "apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: t\nspec:\n" +
		"  source:\n    type: apache-kafka\n    bootstrapServers: [\"source:29092\"]\n    credentials: ${SOURCE_CREDS}\n" +
		"  target:\n    type: confluent-platform\n    credentials: ${TARGET_CREDS}\n    kafka:\n      restEndpoint: ${SRV}\n" +
		"  topics:\n    mode: new\n    include: [\"orders\"]\n"
	out, errOut, err := runManifest(t, srv.URL, spec, topicSource{id: "src-1", topics: []string{"orders"}}, false)
	require.NoError(t, err, "stderr: %s", errOut)
	require.NotContains(t, errOut, "spec.clusterLink is required")
	require.Contains(t, out, "== newTopics")
	require.Contains(t, out, "1 created")
}

// Neither clusterLink nor topics → the reworded nothing-to-apply error.
func TestApply_NothingToApply(t *testing.T) {
	srv := startStubTopicTarget(t)
	defer srv.Close()
	spec := "apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: t\nspec:\n" +
		"  source:\n    type: apache-kafka\n    bootstrapServers: [\"source:29092\"]\n    credentials: ${SOURCE_CREDS}\n" +
		"  target:\n    type: confluent-platform\n    credentials: ${TARGET_CREDS}\n    kafka:\n      restEndpoint: ${SRV}\n"
	_, _, err := runManifest(t, srv.URL, spec, topicSource{id: "src-1"}, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "spec.clusterLink and/or spec.topics is required")
}

func TestResolveLinkConfigs_DefaultsApplied(t *testing.T) {
	cl := &manifest.ClusterLink{Name: "l", Prefix: "p."}
	got, err := resolveLinkConfigs(cl)
	require.NoError(t, err)
	require.Equal(t, "p.", got["cluster.link.prefix"])
	require.Equal(t, "true", got["consumer.offset.sync.enable"])
}
