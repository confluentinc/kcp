package apply

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/manifest"
	migrate "github.com/confluentinc/kcp/internal/migrate"
	macls "github.com/confluentinc/kcp/internal/migrate/acls"
	iamservice "github.com/confluentinc/kcp/internal/services/iam"
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
			"  target:\n    type: confluent-platform\n    clusterCredentials: "+targetCreds+"\n    kafka:\n      restEndpoint: "+srvURL+"\n"+
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
	require.Contains(t, out, "1 unchanged")
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
			"  target:\n    type: confluent-platform\n    clusterCredentials: "+targetCreds+"\n    kafka:\n      restEndpoint: "+destSrv.URL+"\n"+
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
	// List cluster links (mirror-collision detection) — none, so no collision.
	mux.HandleFunc("/kafka/v3/clusters/dest-1/links", func(w http.ResponseWriter, _ *http.Request) {
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
		"  target:\n    type: confluent-platform\n    clusterCredentials: ${TARGET_CREDS}\n    kafka:\n      restEndpoint: ${SRV}\n" +
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
		"  target:\n    type: confluent-platform\n    clusterCredentials: ${TARGET_CREDS}\n    kafka:\n      restEndpoint: ${SRV}\n" +
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
		"  target:\n    type: confluent-platform\n    clusterCredentials: ${TARGET_CREDS}\n    kafka:\n      restEndpoint: ${SRV}\n"
	_, _, err := runManifest(t, srv.URL, spec, topicSource{id: "src-1"}, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "spec.clusterLink, spec.topics and/or spec.acls is required")
}

// runWithSourceCreds writes a manifest with the given source.type and a source
// credentials file with the given body (used for both spec.source and the
// clusterLink.source), stubs the live read, and runs apply. Returns the error.
func runWithSourceCreds(t *testing.T, sourceType, sourceCredsBody string) error {
	t.Helper()
	dir := t.TempDir()
	targetCreds := filepath.Join(dir, "target.yaml")
	require.NoError(t, os.WriteFile(targetCreds, []byte("basic:\n  username: admin\n  password: admin-secret\n"), 0600))
	sourceCreds := filepath.Join(dir, "source.yaml")
	require.NoError(t, os.WriteFile(sourceCreds, []byte(sourceCredsBody), 0600))
	mf := filepath.Join(dir, "migration.yaml")
	require.NoError(t, os.WriteFile(mf, []byte(
		"apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: t\nspec:\n"+
			"  source:\n    type: "+sourceType+"\n    bootstrapServers: [\"source:29092\"]\n    credentials: "+sourceCreds+"\n"+
			"  target:\n    type: confluent-platform\n    clusterCredentials: "+targetCreds+"\n    kafka:\n      restEndpoint: http://127.0.0.1:1\n"+
			"  clusterLink:\n    name: src-to-dest\n    source:\n      bootstrapServers: [\"source:29092\"]\n      credentials: "+sourceCreds+"\n"), 0600))

	old := newSourceReader
	newSourceReader = func(types.KafkaSourceConn) migrate.Source { return staticSource("src-1") }
	t.Cleanup(func() { newSourceReader = old })
	cmd := NewMigrateApplyCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"-f", mf})
	return cmd.Execute()
}

func TestApply_IAM_RejectedForApacheKafka(t *testing.T) {
	err := runWithSourceCreds(t, "apache-kafka", "iam: { region: us-east-1 }\n")
	require.Error(t, err)
	require.Contains(t, err.Error(), "iam auth requires spec.source.type: msk")
}

func TestApply_IAM_RejectedInLinkCreds(t *testing.T) {
	// source.type msk so the source-read check passes; the link creds (same file)
	// must still be rejected because a link cannot speak IAM.
	err := runWithSourceCreds(t, "msk", "iam: { region: us-east-1 }\n")
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot authenticate a cluster link")
}

func TestApply_ConfluentCloudTarget_CreatesLinkOnLkcPath(t *testing.T) {
	lkc := "lkc-abc123"
	createHit := false
	mux := http.NewServeMux()
	// CC has no list endpoint; if apply calls it, that's a bug.
	mux.HandleFunc("/kafka/v3/clusters", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("apply must not call GET /kafka/v3/clusters for a CC target")
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/kafka/v3/clusters/"+lkc+"/links/src-to-cc", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound) // link absent -> to create
	})
	mux.HandleFunc("/kafka/v3/clusters/"+lkc+"/links/", func(w http.ResponseWriter, _ *http.Request) {
		createHit = true
		w.WriteHeader(http.StatusCreated)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	targetCreds := filepath.Join(dir, "t.yaml")
	require.NoError(t, os.WriteFile(targetCreds, []byte("api_key: k\napi_secret: s\n"), 0600))
	sourceCreds := filepath.Join(dir, "s.yaml")
	require.NoError(t, os.WriteFile(sourceCreds, []byte("unauthenticated_plaintext: {}\n"), 0600))
	mf := filepath.Join(dir, "m.yaml")
	require.NoError(t, os.WriteFile(mf, []byte(
		"apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: t\nspec:\n"+
			"  source:\n    type: apache-kafka\n    bootstrapServers: [\"source:29092\"]\n    credentials: "+sourceCreds+"\n"+
			"  target:\n    type: confluent-cloud\n    clusterId: "+lkc+"\n    clusterCredentials: "+targetCreds+"\n    kafka:\n      restEndpoint: "+srv.URL+"\n"+
			"  clusterLink:\n    name: src-to-cc\n    source:\n      bootstrapServers: [\"source:29092\"]\n      credentials: "+sourceCreds+"\n"), 0600))

	old := newSourceReader
	newSourceReader = func(types.KafkaSourceConn) migrate.Source { return staticSource("src-1") }
	t.Cleanup(func() { newSourceReader = old })
	cmd := NewMigrateApplyCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs([]string{"-f", mf})
	require.NoError(t, cmd.Execute(), errb.String())
	require.True(t, createHit, "expected a link create on the lkc-scoped path")
	require.Contains(t, out.String(), "1 created")
}

func TestResolveLinkConfigs_DefaultsApplied(t *testing.T) {
	cl := &manifest.ClusterLink{Name: "l", Prefix: "p."}
	got, err := cl.ResolvedLinkConfigs()
	require.NoError(t, err)
	require.Equal(t, "p.", got["cluster.link.prefix"])
	require.Equal(t, "true", got["consumer.offset.sync.enable"])
}

// fakeACLSourceAdmin is a sourceACLLister double returning a fixed native ACL
// set, so the ACL pipeline reads a real principal without a live connection.
type fakeACLSourceAdmin struct{ acls []sarama.ResourceAcls }

func (f fakeACLSourceAdmin) ListAcls() ([]sarama.ResourceAcls, error) { return f.acls, nil }
func (f fakeACLSourceAdmin) Close() error                             { return nil }

// oneReadACL is a single native ACL: User:app, Allow Read on topic "orders",
// host "*". Its sarama enums stringify to the canonical titlecase forms
// ReadNativeACLs/NormalizeForCC expect.
func oneReadACL(principal string) []sarama.ResourceAcls {
	return []sarama.ResourceAcls{{
		Resource: sarama.Resource{
			ResourceType:        sarama.AclResourceTopic,
			ResourceName:        "orders",
			ResourcePatternType: sarama.AclPatternLiteral,
		},
		Acls: []*sarama.Acl{{
			Principal:      principal,
			Host:           "*",
			Operation:      sarama.AclOperationRead,
			PermissionType: sarama.AclPermissionAllow,
		}},
	}}
}

// aclCaptureTarget is a stub Confluent Cloud target serving the IAM v2
// service-accounts API and the Kafka REST v3 ACL API, capturing POST counts
// and the ACL create body.
type aclCaptureTarget struct {
	srv         *httptest.Server
	saPosts     int64
	aclPosts    int64
	lastACLBody map[string]any
	createdSAID string
}

func startACLCaptureTarget(t *testing.T, clusterID string) *aclCaptureTarget {
	t.Helper()
	c := &aclCaptureTarget{createdSAID: "sa-created1"}
	mux := http.NewServeMux()

	// IAM v2 service accounts: GET (find by display_name → none) / POST (create).
	mux.HandleFunc("/iam/v2/service-accounts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			atomic.AddInt64(&c.saPosts, 1)
			var body struct {
				DisplayName string `json:"display_name"`
				Description string `json:"description"`
			}
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"` + c.createdSAID + `","display_name":"` + body.DisplayName + `","description":"` + body.Description + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`)) // no existing account → plan a create
	})

	// Legacy /service_accounts (numeric-id -> "sa-" resource-id map): the acls
	// reconciler uses this to normalize read-back principals. Return the SA this
	// stub "creates" so NumericToResourceID has a realistic entry (the product
	// calls this whenever the CC target's cloud creds are in hand).
	mux.HandleFunc("/service_accounts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"users":[{"id":100200300,"resource_id":"` + c.createdSAID + `","service_account":true}],"page_info":{"next_page_token":""}}`))
	})

	// Kafka REST v3 ACLs: GET (list → none) / POST (create).
	mux.HandleFunc("/kafka/v3/clusters/"+clusterID+"/acls", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			atomic.AddInt64(&c.aclPosts, 1)
			b, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(b, &body)
			c.lastACLBody = body
			w.WriteHeader(http.StatusCreated)
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	c.srv = httptest.NewServer(mux)
	t.Cleanup(c.srv.Close)
	return c
}

// runACLApply writes a CC-target manifest with serviceAccounts.autoCreate and
// acls.include ["*"], stubs the source ACL read + IAM base URL, and runs apply.
func runACLApply(t *testing.T, tgt *aclCaptureTarget, clusterID string, dryRun bool) (stdout, stderr string, err error) {
	t.Helper()
	dir := t.TempDir()
	targetCreds := filepath.Join(dir, "target.yaml")
	require.NoError(t, os.WriteFile(targetCreds, []byte("api_key: k\napi_secret: s\n"), 0600))
	// cloudCredentials is required when serviceAccounts.autoCreate is set (IAM v2
	// needs a Cloud/Global API key); the stub target ignores the auth, so any key works.
	cloudCreds := filepath.Join(dir, "cloud.yaml")
	require.NoError(t, os.WriteFile(cloudCreds, []byte("api_key: ck\napi_secret: cs\n"), 0600))
	sourceCreds := filepath.Join(dir, "source.yaml")
	require.NoError(t, os.WriteFile(sourceCreds, []byte("unauthenticated_plaintext: {}\n"), 0600))
	mf := filepath.Join(dir, "migration.yaml")
	require.NoError(t, os.WriteFile(mf, []byte(
		"apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: t\nspec:\n"+
			"  source:\n    type: apache-kafka\n    bootstrapServers: [\"source:29092\"]\n    credentials: "+sourceCreds+"\n"+
			"  target:\n    type: confluent-cloud\n    clusterId: "+clusterID+"\n    clusterCredentials: "+targetCreds+"\n    cloudCredentials: "+cloudCreds+"\n    kafka:\n      restEndpoint: "+tgt.srv.URL+"\n"+
			"  serviceAccounts:\n    autoCreate: true\n"+
			"  acls:\n    include: [\"*\"]\n"), 0600))

	oldReader := newSourceReader
	newSourceReader = func(types.KafkaSourceConn) migrate.Source { return staticSource("src-1") }
	oldLister := newSourceACLLister
	newSourceACLLister = func(types.KafkaSourceConn) (sourceACLLister, error) {
		return fakeACLSourceAdmin{acls: oneReadACL("User:app")}, nil
	}
	oldBase := ccIAMBaseURL
	ccIAMBaseURL = tgt.srv.URL
	t.Cleanup(func() {
		newSourceReader = oldReader
		newSourceACLLister = oldLister
		ccIAMBaseURL = oldBase
	})

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

// Dry-run: the plan lists the service account + ACL and mutates nothing.
func TestApply_ACLs_DryRun_PlansNoMutation(t *testing.T) {
	tgt := startACLCaptureTarget(t, "lkc-acl1")
	out, errOut, err := runACLApply(t, tgt, "lkc-acl1", true)
	require.NoError(t, err, "stderr: %s", errOut)
	require.Contains(t, out, "== serviceAccounts (Planned) ==")
	require.Contains(t, out, `service account for principal "User:app"`)
	require.Contains(t, out, "== acls (Planned) ==")
	require.Contains(t, out, `ACL for`)
	require.Contains(t, out, "orders")
	require.Equal(t, int64(0), tgt.saPosts, "dry-run must not create service accounts")
	require.Equal(t, int64(0), tgt.aclPosts, "dry-run must not create ACLs")
}

// Apply: a service account is created, then an ACL with the principal rewritten
// to the created User:sa- identity.
func TestApply_ACLs_Apply_CreatesSAThenRewrittenACL(t *testing.T) {
	tgt := startACLCaptureTarget(t, "lkc-acl1")
	out, errOut, err := runACLApply(t, tgt, "lkc-acl1", false)
	require.NoError(t, err, "stderr: %s", errOut)

	require.Equal(t, int64(1), tgt.saPosts, "expected one service-account create")
	require.Equal(t, int64(1), tgt.aclPosts, "expected one ACL create")
	require.NotNil(t, tgt.lastACLBody)
	require.Equal(t, "User:"+tgt.createdSAID, tgt.lastACLBody["principal"], "ACL principal must be rewritten to the created service account")
	require.Equal(t, "orders", tgt.lastACLBody["resource_name"])
	require.Contains(t, out, "1 created")
}

// runACLApplyMSK is like runACLApply but uses an MSK source and an optional
// spec.acls.unprotectedTopicPolicy, to exercise checkUnprotectedTopics.
func runACLApplyMSK(t *testing.T, tgt *aclCaptureTarget, clusterID, policy string) (stdout, stderr, logs string, err error) {
	t.Helper()
	dir := t.TempDir()
	targetCreds := filepath.Join(dir, "target.yaml")
	require.NoError(t, os.WriteFile(targetCreds, []byte("api_key: k\napi_secret: s\n"), 0600))
	// cloudCredentials is required when serviceAccounts.autoCreate is set (IAM v2
	// needs a Cloud/Global API key); the stub target ignores the auth, so any key works.
	cloudCreds := filepath.Join(dir, "cloud.yaml")
	require.NoError(t, os.WriteFile(cloudCreds, []byte("api_key: ck\napi_secret: cs\n"), 0600))
	sourceCreds := filepath.Join(dir, "source.yaml")
	require.NoError(t, os.WriteFile(sourceCreds, []byte("unauthenticated_plaintext: {}\n"), 0600))
	policyLine := ""
	if policy != "" {
		policyLine = "    unprotectedTopicPolicy: " + policy + "\n"
	}
	mf := filepath.Join(dir, "migration.yaml")
	require.NoError(t, os.WriteFile(mf, []byte(
		"apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: t\nspec:\n"+
			"  source:\n    type: msk\n    bootstrapServers: [\"source:29092\"]\n    credentials: "+sourceCreds+"\n"+
			"  target:\n    type: confluent-cloud\n    clusterId: "+clusterID+"\n    clusterCredentials: "+targetCreds+"\n    cloudCredentials: "+cloudCreds+"\n    kafka:\n      restEndpoint: "+tgt.srv.URL+"\n"+
			"  serviceAccounts:\n    autoCreate: true\n"+
			"  acls:\n    include: [\"*\"]\n"+policyLine), 0600))

	oldReader := newSourceReader
	newSourceReader = func(types.KafkaSourceConn) migrate.Source { return staticSource("src-1") }
	oldLister := newSourceACLLister
	newSourceACLLister = func(types.KafkaSourceConn) (sourceACLLister, error) {
		return fakeACLSourceAdmin{acls: oneReadACL("User:app")}, nil
	}
	oldBase := ccIAMBaseURL
	ccIAMBaseURL = tgt.srv.URL
	t.Cleanup(func() {
		newSourceReader = oldReader
		newSourceACLLister = oldLister
		ccIAMBaseURL = oldBase
	})

	cmd := NewMigrateApplyCmd()
	var outBuf, errBuf, logBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"-f", mf})

	// The command's diagnostics (world-open caveat, drops) now go through slog,
	// not cmd stdout. Capture the default logger into logBuf for the duration of
	// this run so tests can assert the caveat surfaced at WARN.
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(prev)

	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), logBuf.String(), err
}

// unprotectedTopicPolicy: fail on an MSK source must fail closed: world-open
// detection is not implemented, so the requested hard stop cannot be honored,
// and apply must refuse rather than silently proceeding. No SA/ACL mutation
// should happen.
func TestApply_ACLs_UnprotectedTopicPolicyFail_MSK_ReturnsError(t *testing.T) {
	tgt := startACLCaptureTarget(t, "lkc-acl-fail")
	_, _, logs, err := runACLApplyMSK(t, tgt, "lkc-acl-fail", manifest.UnprotectedTopicPolicyFail)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not yet implemented")
	require.Contains(t, logs, "WARN") // caveat surfaced via slog.Warn
	require.Contains(t, logs, "not yet enforced")
	require.Equal(t, int64(0), tgt.saPosts, "fail-closed must not create service accounts")
	require.Equal(t, int64(0), tgt.aclPosts, "fail-closed must not create ACLs")
}

// unprotectedTopicPolicy: warn (and the default/unset case) on an MSK source
// must still emit the unenforced-detection caveat, but proceed with apply.
func TestApply_ACLs_UnprotectedTopicPolicyWarn_MSK_ProceedsWithWarning(t *testing.T) {
	tgt := startACLCaptureTarget(t, "lkc-acl-warn")
	_, _, logs, err := runACLApplyMSK(t, tgt, "lkc-acl-warn", manifest.UnprotectedTopicPolicyWarn)
	require.NoError(t, err, "logs: %s", logs)
	require.Contains(t, logs, "WARN")
	require.Contains(t, logs, "not yet enforced")
	require.Equal(t, int64(1), tgt.saPosts)
	require.Equal(t, int64(1), tgt.aclPosts)
}

func TestApply_ACLs_UnprotectedTopicPolicyUnset_MSK_ProceedsWithWarning(t *testing.T) {
	tgt := startACLCaptureTarget(t, "lkc-acl-unset")
	_, _, logs, err := runACLApplyMSK(t, tgt, "lkc-acl-unset", "")
	require.NoError(t, err, "logs: %s", logs)
	require.Contains(t, logs, "WARN")
	require.Contains(t, logs, "not yet enforced")
	require.Equal(t, int64(1), tgt.saPosts)
	require.Equal(t, int64(1), tgt.aclPosts)
}

// testClusterArn/testPrincipalArn are a fixed MSK cluster ARN + IAM role ARN
// pair used across the IAM-plane tests below. principalFromArn (see
// iam_translate.go) derives "User:AppRole" from testPrincipalArn.
const (
	testClusterArn   = "arn:aws:kafka:us-east-1:111122223333:cluster/mymsk/abc-5"
	testPrincipalArn = "arn:aws:iam::111122223333:role/AppRole"
)

// runACLApplyIAM is like runACLApplyMSK but adds spec.acls.iam wiring: iamYAML
// is the raw "iam:" sub-block (indented to sit under "acls:"), nativeACLs
// seeds the fake source ACL lister, and fetcher stubs newIAMFetcher — so the
// IAM read path is exercised with no AWS credential chain. It is a thin
// wrapper over runACLApplyIAMFull for the (still very common) explicit-mode,
// no-verify callers, which need neither the enumerator nor the effective-
// access-checker fake.
func runACLApplyIAM(t *testing.T, tgt *aclCaptureTarget, clusterID, iamYAML string, nativeACLs []sarama.ResourceAcls, fetcher macls.PrincipalPolicyFetcher, dryRun bool) (stdout, stderr, logs string, err error) {
	return runACLApplyIAMFull(t, tgt, clusterID, iamYAML, nativeACLs, fetcher, nil, nil, dryRun)
}

// runACLApplyIAMFull is runACLApplyIAM plus fakes for the discoverAllRoles
// (newRolePolicyEnumerator) and verifyEffectiveAccess (newEffectiveAccessChecker)
// seams, so both Phase 1B slice 2 modes are exercisable with no AWS
// credential chain. A nil enumerator/checker is fine when the manifest under
// test doesn't exercise that mode — buildACLReconcilers never calls the
// corresponding seam in that case.
func runACLApplyIAMFull(t *testing.T, tgt *aclCaptureTarget, clusterID, iamYAML string, nativeACLs []sarama.ResourceAcls, fetcher macls.PrincipalPolicyFetcher, enumerator macls.RolePolicyEnumerator, checker macls.EffectiveAccessChecker, dryRun bool) (stdout, stderr, logs string, err error) {
	t.Helper()
	dir := t.TempDir()
	targetCreds := filepath.Join(dir, "target.yaml")
	require.NoError(t, os.WriteFile(targetCreds, []byte("api_key: k\napi_secret: s\n"), 0600))
	cloudCreds := filepath.Join(dir, "cloud.yaml")
	require.NoError(t, os.WriteFile(cloudCreds, []byte("api_key: ck\napi_secret: cs\n"), 0600))
	sourceCreds := filepath.Join(dir, "source.yaml")
	require.NoError(t, os.WriteFile(sourceCreds, []byte("unauthenticated_plaintext: {}\n"), 0600))
	mf := filepath.Join(dir, "migration.yaml")
	require.NoError(t, os.WriteFile(mf, []byte(
		"apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: t\nspec:\n"+
			"  source:\n    type: msk\n    bootstrapServers: [\"source:29092\"]\n    credentials: "+sourceCreds+"\n"+
			"  target:\n    type: confluent-cloud\n    clusterId: "+clusterID+"\n    clusterCredentials: "+targetCreds+"\n    cloudCredentials: "+cloudCreds+"\n    kafka:\n      restEndpoint: "+tgt.srv.URL+"\n"+
			"  serviceAccounts:\n    autoCreate: true\n"+
			"  acls:\n    include: [\"*\"]\n"+iamYAML), 0600))

	oldReader := newSourceReader
	newSourceReader = func(types.KafkaSourceConn) migrate.Source { return staticSource("src-1") }
	oldLister := newSourceACLLister
	newSourceACLLister = func(types.KafkaSourceConn) (sourceACLLister, error) {
		return fakeACLSourceAdmin{acls: nativeACLs}, nil
	}
	oldFetcher := newIAMFetcher
	newIAMFetcher = func() (macls.PrincipalPolicyFetcher, error) { return fetcher, nil }
	oldEnumerator := newRolePolicyEnumerator
	newRolePolicyEnumerator = func() (macls.RolePolicyEnumerator, error) { return enumerator, nil }
	oldChecker := newEffectiveAccessChecker
	newEffectiveAccessChecker = func() (macls.EffectiveAccessChecker, error) { return checker, nil }
	oldBase := ccIAMBaseURL
	ccIAMBaseURL = tgt.srv.URL
	t.Cleanup(func() {
		newSourceReader = oldReader
		newSourceACLLister = oldLister
		newIAMFetcher = oldFetcher
		newRolePolicyEnumerator = oldEnumerator
		newEffectiveAccessChecker = oldChecker
		ccIAMBaseURL = oldBase
	})

	cmd := NewMigrateApplyCmd()
	var outBuf, errBuf, logBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	args := []string{"-f", mf}
	if dryRun {
		args = append(args, "--dry-run")
	}
	cmd.SetArgs(args)

	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(prev)

	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), logBuf.String(), err
}

// iamFetcherOne returns a fixed newIAMFetcher-compatible fake that always
// returns the same PrincipalPolicies (one inline policy document) regardless
// of which principalArn ReadIAMACLs calls it with.
func iamFetcherOne(action, resourceArn string) macls.PrincipalPolicyFetcher {
	return func(ctx context.Context, arn string) (*iamservice.PrincipalPolicies, error) {
		return &iamservice.PrincipalPolicies{
			PrincipalArn: arn, PrincipalName: "AppRole", PrincipalType: "role",
			InlinePolicies: []iamservice.InlinePolicy{{PolicyName: "p", PolicyDocument: map[string]any{
				"Statement": []any{map[string]any{"Effect": "Allow", "Action": action, "Resource": resourceArn}},
			}}},
		}, nil
	}
}

// Dry-run union: native ACLs (User:app, Read, "orders") and IAM-derived ACLs
// (User:AppRole, Write, "payments" — a distinct principal/resource so this
// test observes pure union, not the dedupe case) must both appear in the
// planned acls set, and dry-run must mutate nothing.
func TestApply_ACLs_IAM_UnionDryRun(t *testing.T) {
	tgt := startACLCaptureTarget(t, "lkc-acl-iam1")
	iamYAML := "    iam:\n      clusterArn: " + testClusterArn + "\n      principalArns: [\"" + testPrincipalArn + "\"]\n"
	fetcher := iamFetcherOne("kafka-cluster:WriteData", "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/payments")

	out, _, logs, err := runACLApplyIAM(t, tgt, "lkc-acl-iam1", iamYAML, oneReadACL("User:app"), fetcher, true)
	require.NoError(t, err, "logs: %s", logs)

	require.Contains(t, out, "orders", "native ACL must appear in the plan")
	require.Contains(t, out, "payments", "IAM-derived ACL must appear in the plan (union)")
	require.Equal(t, int64(0), tgt.saPosts, "dry-run must not create service accounts")
	require.Equal(t, int64(0), tgt.aclPosts, "dry-run must not create ACLs")
}

// The "effective access not verified" caveat must be emitted at WARN whenever
// spec.acls.iam is set (explicit-principals mode), regardless of dry-run.
func TestApply_ACLs_IAM_WarnLabelEmitted(t *testing.T) {
	tgt := startACLCaptureTarget(t, "lkc-acl-iam3")
	iamYAML := "    iam:\n      clusterArn: " + testClusterArn + "\n      principalArns: [\"" + testPrincipalArn + "\"]\n"
	fetcher := iamFetcherOne("kafka-cluster:WriteData", "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/payments")

	_, _, logs, err := runACLApplyIAM(t, tgt, "lkc-acl-iam3", iamYAML, oneReadACL("User:app"), fetcher, true)
	require.NoError(t, err, "logs: %s", logs)
	require.Contains(t, logs, "WARN")
	require.Contains(t, logs, "effective access")
	require.Contains(t, logs, "not verified")
}

// discoverAllRolesResourceArn is the in-cluster resource the enumeration
// tests below grant kafka-cluster:ReadData on; excludedRoleResourceArn is a
// DIFFERENT in-cluster resource an excluded role's policy grants — if
// isExcludedIAMRole/GatherEnumerated ever regressed and let the excluded
// role's grant through, this resource name would leak into the plan and the
// tests would catch it.
const (
	discoverAllRolesResourceArn = "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/orders"
	excludedRoleResourceArn     = "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/secret-topic"
)

// fakeEnumeratedRole builds one iamservice.PrincipalPolicies with a single
// inline-policy Allow statement, for use as a fake newRolePolicyEnumerator
// response.
func fakeEnumeratedRole(principalArn, principalName, action, resourceArn string) iamservice.PrincipalPolicies {
	return iamservice.PrincipalPolicies{
		PrincipalArn: principalArn, PrincipalName: principalName, PrincipalType: "role",
		InlinePolicies: []iamservice.InlinePolicy{{PolicyName: "p", PolicyDocument: map[string]any{
			"Statement": []any{map[string]any{"Effect": "Allow", "Action": action, "Resource": resourceArn}},
		}}},
	}
}

// spec.acls.iam.discoverAllRoles now enumerates the account's IAM roles (via
// newRolePolicyEnumerator) instead of refusing with "not yet implemented"
// (the Phase 1B slice 1 guard is gone): a normal in-cluster workload role's
// grant must appear in the plan, while an AWS service-linked role's grant —
// excluded by isExcludedIAMRole/GatherEnumerated — must contribute nothing,
// even though enumerate() returns it right alongside the workload role. This
// also confirms discoverAllRoles no longer errors.
func TestApply_ACLs_IAM_DiscoverAllRoles_EnumeratesAndExcludes(t *testing.T) {
	tgt := startACLCaptureTarget(t, "lkc-acl-iam4")
	iamYAML := "    iam:\n      clusterArn: " + testClusterArn + "\n      discoverAllRoles: true\n"

	enumerator := func(ctx context.Context) ([]iamservice.PrincipalPolicies, error) {
		return []iamservice.PrincipalPolicies{
			fakeEnumeratedRole(testPrincipalArn, "AppRole", "kafka-cluster:ReadData", discoverAllRolesResourceArn),
			fakeEnumeratedRole(
				"arn:aws:iam::111122223333:role/aws-service-role/kafka.amazonaws.com/AWSServiceRoleForKafka",
				"AWSServiceRoleForKafka", "kafka-cluster:*", excludedRoleResourceArn,
			),
		}, nil
	}

	out, _, logs, err := runACLApplyIAMFull(t, tgt, "lkc-acl-iam4", iamYAML, nil, nil, enumerator, nil, true)
	require.NoError(t, err, "logs: %s", logs)
	require.Contains(t, out, "orders", "the enumerated in-cluster workload role's ACL must appear in the plan")
	require.NotContains(t, out, "secret-topic", "the excluded service-linked role must contribute nothing")
	require.Equal(t, int64(0), tgt.saPosts, "dry-run must not create service accounts")
	require.Equal(t, int64(0), tgt.aclPosts, "dry-run must not create ACLs")
}

// spec.acls.iam.verifyEffectiveAccess now filters gathered grants through
// newEffectiveAccessChecker instead of refusing with "not yet implemented"
// (the Phase 1B slice 1 guard is gone): a grant the checker reports as NOT
// effectively allowed must be dropped before translation. Verification does
// NOT suppress the WARN entirely: SimulatePrincipalPolicy only covers
// identity policies + permission boundaries, never SCPs, so verify-ON emits
// its own honest SCP-caveat WARN instead of the verify-OFF "not verified"
// wording (which would now be inaccurate — verification DID run).
func TestApply_ACLs_IAM_VerifyEffectiveAccess_DropsDeniedAndEmitsSCPCaveat(t *testing.T) {
	tgt := startACLCaptureTarget(t, "lkc-acl-iam5")
	iamYAML := "    iam:\n      clusterArn: " + testClusterArn + "\n      principalArns: [\"" + testPrincipalArn + "\"]\n      verifyEffectiveAccess: true\n"
	resourceArn := "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/payments"
	fetcher := iamFetcherOne("kafka-cluster:WriteData", resourceArn)
	// The checker denies every pair it's asked about — the identity policy
	// nominally grants WriteData/payments, but effective access says no.
	checker := func(ctx context.Context, principalArn string, actions, resources []string) (map[string]bool, error) {
		return map[string]bool{}, nil
	}

	out, _, logs, err := runACLApplyIAMFull(t, tgt, "lkc-acl-iam5", iamYAML, nil, fetcher, nil, checker, true)
	require.NoError(t, err, "logs: %s", logs)
	require.NotContains(t, out, "payments", "a denied grant must be dropped by verifyEffectiveAccess before translation")
	require.Contains(t, logs, "SCP", "verify-ON must emit the SCP-not-evaluated caveat")
	require.NotContains(t, logs, "not verified", "verify-ON must not repeat the verify-OFF wording once verification actually ran")
	require.Equal(t, int64(0), tgt.saPosts)
	require.Equal(t, int64(0), tgt.aclPosts)
}

// The positive counterpart to the drop case above: a grant the checker
// reports as effectively allowed must survive verifyEffectiveAccess and
// still translate/appear in the plan — verification is a filter, not a
// blanket suppressor. The SCP-caveat WARN still fires (it documents a
// limitation of verification itself, independent of any single grant's
// outcome).
func TestApply_ACLs_IAM_VerifyEffectiveAccess_KeepsAllowedGrant(t *testing.T) {
	tgt := startACLCaptureTarget(t, "lkc-acl-iam5b")
	iamYAML := "    iam:\n      clusterArn: " + testClusterArn + "\n      principalArns: [\"" + testPrincipalArn + "\"]\n      verifyEffectiveAccess: true\n"
	resourceArn := "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/payments"
	fetcher := iamFetcherOne("kafka-cluster:WriteData", resourceArn)
	checker := func(ctx context.Context, principalArn string, actions, resources []string) (map[string]bool, error) {
		allowed := make(map[string]bool)
		for _, a := range actions {
			for _, r := range resources {
				allowed[a+"|"+r] = true
			}
		}
		return allowed, nil
	}

	out, _, logs, err := runACLApplyIAMFull(t, tgt, "lkc-acl-iam5b", iamYAML, nil, fetcher, nil, checker, true)
	require.NoError(t, err, "logs: %s", logs)
	require.Contains(t, out, "payments", "an effectively-allowed grant must survive verifyEffectiveAccess")
	require.Contains(t, logs, "SCP", "verify-ON must emit the SCP-not-evaluated caveat")
	require.NotContains(t, logs, "not verified", "verify-ON must not repeat the verify-OFF wording once verification actually ran")
}

// Cross-plane dedupe (fixed — see task-6-report.md "Fix: dedupe desired ACL
// set"): native and IAM both grant the IDENTICAL types.Acls tuple for the
// same principal (User:AppRole via testPrincipalArn) — Allow Read on Topic
// "orders" — via two independent planes. Per the Phase 1B design ("union +
// dedupe onto the target"), buildACLReconcilers now dedupes the normalized
// Desired set (dedupeACLs in cmd_migrate_apply.go) before it reaches either
// distinctPrincipals or the acls reconciler, so this identical tuple must be
// created exactly once, not once per plane.
func TestApply_ACLs_IAM_IdenticalNativeAndIAMTuple_DedupedToSingleCreate(t *testing.T) {
	tgt := startACLCaptureTarget(t, "lkc-acl-iam2")
	iamYAML := "    iam:\n      clusterArn: " + testClusterArn + "\n      principalArns: [\"" + testPrincipalArn + "\"]\n"
	fetcher := iamFetcherOne("kafka-cluster:ReadData", "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/orders")

	// Native fake ACL for the SAME principal ("User:AppRole", matching
	// principalFromArn(testPrincipalArn)) grants the SAME tuple.
	_, _, logs, err := runACLApplyIAM(t, tgt, "lkc-acl-iam2", iamYAML, oneReadACL("User:AppRole"), fetcher, false)
	require.NoError(t, err, "logs: %s", logs)

	require.Equal(t, int64(1), tgt.saPosts, "one service account for the single distinct principal")
	require.Equal(t, int64(1), tgt.aclPosts, "identical native+IAM ACL tuple must be deduped to a single create")
}

// Within-plane dedupe: a single IAM principal whose identity policy has TWO
// statements that each grant the SAME (op, resource) — e.g. an inline policy
// and a managed policy both saying "Allow kafka-cluster:ReadData on
// topic/.../orders" — must still resolve to exactly one ACL create.
// ReadIAMACLs translates every matching statement independently, so without
// dedupeACLs this within-plane duplicate would double-create exactly like
// the cross-plane case above.
func TestApply_ACLs_IAM_TwoStatementsSameOpResource_DedupedToSingleCreate(t *testing.T) {
	tgt := startACLCaptureTarget(t, "lkc-acl-iam6")
	iamYAML := "    iam:\n      clusterArn: " + testClusterArn + "\n      principalArns: [\"" + testPrincipalArn + "\"]\n"
	resourceArn := "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/orders"
	fetcher := func(ctx context.Context, arn string) (*iamservice.PrincipalPolicies, error) {
		return &iamservice.PrincipalPolicies{
			PrincipalArn: arn, PrincipalName: "AppRole", PrincipalType: "role",
			InlinePolicies: []iamservice.InlinePolicy{
				{PolicyName: "p1", PolicyDocument: map[string]any{
					"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:ReadData", "Resource": resourceArn}},
				}},
				{PolicyName: "p2", PolicyDocument: map[string]any{
					"Statement": []any{map[string]any{"Effect": "Allow", "Action": "kafka-cluster:ReadData", "Resource": resourceArn}},
				}},
			},
		}, nil
	}

	// No native ACLs at all — this isolates the within-IAM-plane duplicate
	// from the cross-plane case.
	_, _, logs, err := runACLApplyIAM(t, tgt, "lkc-acl-iam6", iamYAML, nil, fetcher, false)
	require.NoError(t, err, "logs: %s", logs)

	require.Equal(t, int64(1), tgt.saPosts, "one service account for the single distinct principal")
	require.Equal(t, int64(1), tgt.aclPosts, "two IAM statements granting the same op/resource must be deduped to a single create")
}

// A typo'd clusterArn or principalArn is easy to make and, previously, silent:
// spec.acls.iam.principalArns is non-empty but every statement's resource ARN
// is scoped to a DIFFERENT cluster, so ReadIAMACLs returns zero ACLs with no
// error. buildACLReconcilers must surface this with a WARN rather than
// quietly proceeding as if the operator asked for nothing (Finding 2(b) /
// task-7).
func TestApply_ACLs_IAM_ZeroMatch_WarnsOperator(t *testing.T) {
	tgt := startACLCaptureTarget(t, "lkc-acl-iam7")
	iamYAML := "    iam:\n      clusterArn: " + testClusterArn + "\n      principalArns: [\"" + testPrincipalArn + "\"]\n"
	// Grant is scoped to a DIFFERENT cluster (name "OTHER", uuid "zzz-9") than
	// testClusterArn ("mymsk"/"abc-5") -> clusterArnMatches excludes it ->
	// translateStatements/ReadIAMACLs yields an empty slice.
	fetcher := iamFetcherOne("kafka-cluster:ReadData", "arn:aws:kafka:us-east-1:111122223333:topic/OTHER/zzz-9/x")

	_, _, logs, err := runACLApplyIAM(t, tgt, "lkc-acl-iam7", iamYAML, nil, fetcher, true)
	require.NoError(t, err, "logs: %s", logs)
	require.Contains(t, logs, "WARN")
	require.Contains(t, logs, "matched zero ACLs")
	require.Equal(t, int64(0), tgt.saPosts)
	require.Equal(t, int64(0), tgt.aclPosts)
}

// The zero-match warning must NOT fire when the IAM read legitimately
// produces ACLs (the "happy path" already covered by
// TestApply_ACLs_IAM_UnionDryRun) — this is the negative counterpart so the
// new warning can't regress into firing unconditionally.
func TestApply_ACLs_IAM_NonZeroMatch_NoZeroMatchWarning(t *testing.T) {
	tgt := startACLCaptureTarget(t, "lkc-acl-iam8")
	iamYAML := "    iam:\n      clusterArn: " + testClusterArn + "\n      principalArns: [\"" + testPrincipalArn + "\"]\n"
	fetcher := iamFetcherOne("kafka-cluster:WriteData", "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/payments")

	_, _, logs, err := runACLApplyIAM(t, tgt, "lkc-acl-iam8", iamYAML, nil, fetcher, true)
	require.NoError(t, err, "logs: %s", logs)
	require.NotContains(t, logs, "matched zero ACLs")
}

func TestEnsureMSKScramMechanism(t *testing.T) {
	scram := func(mech string) types.KafkaSourceConn {
		return types.KafkaSourceConn{AuthMethod: types.AuthMethodConfig{
			SASLScram: &types.SASLScramConfig{Use: true, Username: "u", Password: "p", Mechanism: mech}}}
	}
	// MSK source + SCRAM must be SHA-512 (MSK is SHA-512-only).
	require.Error(t, ensureMSKScramMechanism(scram("SHA256"), manifest.SourceMSK, "spec.source.credentials"))
	require.NoError(t, ensureMSKScramMechanism(scram("SHA512"), manifest.SourceMSK, "f"))
	require.NoError(t, ensureMSKScramMechanism(scram("SCRAM-SHA-512"), manifest.SourceMSK, "f"))
	// Non-MSK source: no SHA-512 constraint.
	require.NoError(t, ensureMSKScramMechanism(scram("SHA256"), manifest.SourceApacheKafka, "f"))
	// MSK + non-SCRAM (IAM): not subject to the SCRAM mechanism rule.
	iam := types.KafkaSourceConn{AuthMethod: types.AuthMethodConfig{IAM: &types.IAMConfig{Use: true, Region: "us-east-1"}}}
	require.NoError(t, ensureMSKScramMechanism(iam, manifest.SourceMSK, "f"))
}

// TestSplitResourceArnsForSimulation exercises the pure partition helper
// newEffectiveAccessChecker's inner func uses to keep the bare "*" wildcard
// out of the same SimulatePrincipalPolicy ResourceArns list as any specific
// ARN — AWS's SimulatePrincipalPolicy rejects a ResourceArns list that mixes
// the two ("you cannot include both * and individual resources in the
// resource list"). Only the LITERAL "*" string is special; an ARN that
// merely CONTAINS a "*" wildcard segment (e.g. a topic-name prefix wildcard)
// is an individual resource and belongs in the specifics bucket, not split
// out on its own.
func TestSplitResourceArnsForSimulation(t *testing.T) {
	const (
		specificA = "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/topic-a"
		specificB = "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/topic-b"
	)

	t.Run("bare wildcard plus specifics splits into two buckets", func(t *testing.T) {
		got := splitResourceArnsForSimulation([]string{"*", specificA, specificB})
		require.Equal(t, [][]string{{"*"}, {specificA, specificB}}, got)
	})

	t.Run("no bare wildcard is a single bucket of all resources", func(t *testing.T) {
		got := splitResourceArnsForSimulation([]string{specificA})
		require.Equal(t, [][]string{{specificA}}, got)
	})

	t.Run("bare wildcard alone is a single bucket", func(t *testing.T) {
		got := splitResourceArnsForSimulation([]string{"*"})
		require.Equal(t, [][]string{{"*"}}, got)
	})

	t.Run("empty input yields no buckets", func(t *testing.T) {
		require.Empty(t, splitResourceArnsForSimulation(nil))
		require.Empty(t, splitResourceArnsForSimulation([]string{}))
	})

	t.Run("a wildcard-containing ARN is an individual resource, not the bare wildcard", func(t *testing.T) {
		wildcardTopicArn := "arn:aws:kafka:us-east-1:111122223333:topic/mymsk/abc-5/foo-*"
		got := splitResourceArnsForSimulation([]string{"*", wildcardTopicArn})
		require.Equal(t, [][]string{{"*"}, {wildcardTopicArn}}, got)
	})
}
