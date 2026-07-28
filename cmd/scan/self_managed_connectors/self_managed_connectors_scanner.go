package self_managed_connectors

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/redact"
	"github.com/confluentinc/kcp/internal/services/jmx"
	prometheussvc "github.com/confluentinc/kcp/internal/services/prometheus"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
)

type ConnectAPIClient interface {
	ListConnectors() ([]string, error)
	GetConnectorConfig(name string) (map[string]any, error)
	GetConnectorStatus(name string) (map[string]any, error)
}

type HTTPConnectClient struct {
	baseURL    string
	httpClient *http.Client
	authMethod types.ConnectAuthMethod
	saslAuth   types.ConnectSaslScramAuth
}

type SelfManagedConnectorsScannerOpts struct {
	StateFile      string
	State          *types.State
	ConnectRestURL string
	SourceType     types.SourceType
	ClusterArn     string
	ClusterID      string
	AuthMethod     types.ConnectAuthMethod
	SaslScramAuth  types.ConnectSaslScramAuth
	TlsAuth        types.ConnectTlsAuth

	MetricsSource       string
	MetricsClusterCreds *types.OSKClusterAuth
	MetricsDuration     string
	MetricsInterval     string
	MetricsRange        string
}

type SelfManagedConnectorsScanner struct {
	StateFile  string
	State      *types.State
	SourceType types.SourceType
	ClusterArn string
	ClusterID  string
	client     ConnectAPIClient

	metricsSource       string
	metricsClusterCreds *types.OSKClusterAuth
	metricsDuration     string
	metricsInterval     string
	metricsRange        string
}

func NewSelfManagedConnectorsScanner(opts SelfManagedConnectorsScannerOpts) (*SelfManagedConnectorsScanner, error) {
	httpClient, err := createHTTPClient(opts.AuthMethod, opts.TlsAuth)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	connectClient := &HTTPConnectClient{
		baseURL:    opts.ConnectRestURL,
		httpClient: httpClient,
		authMethod: opts.AuthMethod,
		saslAuth:   opts.SaslScramAuth,
	}

	return &SelfManagedConnectorsScanner{
		StateFile:           opts.StateFile,
		State:               opts.State,
		SourceType:          opts.SourceType,
		ClusterArn:          opts.ClusterArn,
		ClusterID:           opts.ClusterID,
		client:              connectClient,
		metricsSource:       opts.MetricsSource,
		metricsClusterCreds: opts.MetricsClusterCreds,
		metricsDuration:     opts.MetricsDuration,
		metricsInterval:     opts.MetricsInterval,
		metricsRange:        opts.MetricsRange,
	}, nil
}

func createHTTPClient(authMethod types.ConnectAuthMethod, tlsAuth types.ConnectTlsAuth) (*http.Client, error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	// Only configure TLS if using TLS client certificate authentication
	if authMethod == types.ConnectAuthMethodTls {
		caCert, err := os.ReadFile(tlsAuth.CACert)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}

		clientCert, err := tls.LoadX509KeyPair(tlsAuth.ClientCert, tlsAuth.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate and key: %w", err)
		}

		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:      caCertPool,
				Certificates: []tls.Certificate{clientCert},
			},
		}
	}

	return client, nil
}

func (s *SelfManagedConnectorsScanner) Run() error {
	if s.client == nil {
		return fmt.Errorf("connect API client not initialized")
	}

	clusterName := utils.GetClusterDisplayName(s.SourceType, s.ClusterArn, s.ClusterID)
	fmt.Printf("🚀 Starting self-managed connector scan for cluster %s\n", clusterName)
	slog.Info("🔍 scanning self-managed connectors", "cluster", clusterName)

	connectorNames, err := s.client.ListConnectors()
	if err != nil {
		return fmt.Errorf("failed to list connectors: %v", err)
	}

	fmt.Printf("  🔍 Found %d connectors\n", len(connectorNames))
	slog.Info("🔍 found connectors", "count", len(connectorNames))

	if len(connectorNames) == 0 {
		fmt.Printf("  ⏭️  No connectors found for cluster %s, skipping\n", clusterName)
		slog.Info("⏭️ no connectors found; skipping", "cluster", clusterName)
		return nil
	}

	connectors := []types.SelfManagedConnector{}
	totalRedacted := 0
	for _, name := range connectorNames {
		connector, redactedCount, err := s.getConnectorDetails(name)
		if err != nil {
			slog.Warn(fmt.Sprintf("⚠️ failed to get connector details for connector %s: %v", name, err))
			continue
		}
		totalRedacted += redactedCount
		connectors = append(connectors, connector)
	}

	fmt.Printf("  ✅ Successfully retrieved connector details for %d connectors\n", len(connectors))
	if totalRedacted > 0 {
		// Counts only — never the redacted keys or values.
		slog.Info("redacted sensitive connector config fields", "redacted_fields", totalRedacted, "connectors", len(connectors))
	}

	if err := s.updateStateWithConnectors(connectors); err != nil {
		return fmt.Errorf("failed to update state: %v", err)
	}

	// Metrics collection is best-effort: a failure warns and continues so the
	// already-scanned connectors are always persisted (KB 003 — graceful
	// discovery errors). Runs without --metrics skip this entirely.
	if s.metricsSource != "" {
		slog.Info("collecting Connect worker metrics", "source", s.metricsSource, "cluster", clusterName)
		metrics, err := s.collectConnectMetrics(context.Background())
		if err != nil {
			slog.Warn("Connect metrics collection failed; connectors persisted without metrics", "source", s.metricsSource, "error", err)
			fmt.Printf("  ⚠️  Connect metrics collection failed; connectors persisted without metrics\n")
		} else if err := s.updateStateWithConnectMetrics(metrics); err != nil {
			slog.Warn("failed to attach Connect metrics to state; connectors persisted without metrics", "error", err)
			fmt.Printf("  ⚠️  Could not attach Connect metrics; connectors persisted without metrics\n")
		} else {
			fmt.Printf("  📊 Collected %d Connect metric data points\n", len(metrics.Metrics))
		}
	}

	if err := s.State.PersistStateFile(s.StateFile); err != nil {
		return fmt.Errorf("failed to save state file: %v", err)
	}

	fmt.Printf("✅ Self-managed connector scan complete for cluster %s\n", clusterName)
	slog.Info("✅ self-managed connector scan complete", "cluster", clusterName, "connectors", len(connectors))
	return nil
}

// getConnectorDetails fetches a connector's config and status. The config is
// redacted (sensitive values replaced) before it is stored on the connector, so
// raw secrets never enter the persisted state. The connector's worker_id (when
// present) is captured as ConnectHost for per-host grouping in the UI. Returns
// the connector, the number of redacted fields, and any error.
func (s *SelfManagedConnectorsScanner) getConnectorDetails(name string) (types.SelfManagedConnector, int, error) {
	slog.Debug("🔍 fetching connector details", "connector", name)
	connector := types.SelfManagedConnector{
		Name: name,
	}

	config, err := s.client.GetConnectorConfig(name)
	if err != nil {
		return connector, 0, fmt.Errorf("failed to get config: %w", err)
	}
	redactedConfig, redactedCount := redact.RedactAnyMap(config)
	connector.Config = redactedConfig

	status, err := s.client.GetConnectorStatus(name)
	if err != nil {
		slog.Warn(fmt.Sprintf("⚠️ failed to get connector status for connector %s: %v", name, err))
	} else {
		if connectorStatus, ok := status["connector"].(map[string]any); ok {
			if stateStr, ok := connectorStatus["state"].(string); ok {
				connector.State = stateStr
			}
			if workerID, ok := connectorStatus["worker_id"].(string); ok {
				connector.ConnectHost = workerID
			}
		}
	}

	return connector, redactedCount, nil
}

func (c *HTTPConnectClient) ListConnectors() ([]string, error) {
	url := fmt.Sprintf("%s/connectors", c.baseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.addAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var connectorNames []string
	if err := json.NewDecoder(resp.Body).Decode(&connectorNames); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return connectorNames, nil
}

func (c *HTTPConnectClient) GetConnectorConfig(name string) (map[string]any, error) {
	url := fmt.Sprintf("%s/connectors/%s/config", c.baseURL, name)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.addAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var config map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return config, nil
}

func (c *HTTPConnectClient) GetConnectorStatus(name string) (map[string]any, error) {
	url := fmt.Sprintf("%s/connectors/%s/status", c.baseURL, name)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.addAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var status map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return status, nil
}

// addAuthHeaders adds basic authentication for SASL/SCRAM Connect clusters on the
// list/status/config endpoints.
func (c *HTTPConnectClient) addAuthHeaders(req *http.Request) {
	if c.authMethod == types.ConnectAuthMethodSaslScram {
		req.SetBasicAuth(c.saslAuth.Username, c.saslAuth.Password)
	}
}

// resolveKafkaAdminInfo returns a mutable pointer to the KafkaAdminClientInformation
// of the cluster this scan targets, routed by source type. The returned pointer
// addresses the cluster's record inside the state, so mutations persist.
func (s *SelfManagedConnectorsScanner) resolveKafkaAdminInfo() (*types.KafkaAdminClientInformation, error) {
	switch s.SourceType {
	case types.SourceTypeMSK:
		cluster, err := s.State.GetClusterByArn(s.ClusterArn)
		if err != nil {
			return nil, err
		}
		return &cluster.KafkaAdminClientInformation, nil
	case types.SourceTypeOSK:
		cluster, err := s.State.GetOSKClusterByID(s.ClusterID)
		if err != nil {
			return nil, err
		}
		return &cluster.KafkaAdminClientInformation, nil
	default:
		return nil, fmt.Errorf("unsupported source type: %s", s.SourceType)
	}
}

func (s *SelfManagedConnectorsScanner) updateStateWithConnectors(connectors []types.SelfManagedConnector) error {
	info, err := s.resolveKafkaAdminInfo()
	if err != nil {
		return err
	}

	info.SetSelfManagedConnectors(connectors)
	fmt.Printf("✅ Updated cluster %s with self-managed connector information\n", utils.GetClusterDisplayName(s.SourceType, s.ClusterArn, s.ClusterID))

	return nil
}

// updateStateWithConnectMetrics attaches collected Connect worker metrics to the
// connectors object for the cluster this scan targets (MSK or OSK, routed by
// source type). It requires the connectors object to already exist so the
// metrics have something to hang off; otherwise it returns a clear error.
func (s *SelfManagedConnectorsScanner) updateStateWithConnectMetrics(metrics *types.ConnectClusterMetrics) error {
	info, err := s.resolveKafkaAdminInfo()
	if err != nil {
		return err
	}

	if info.SelfManagedConnectors == nil {
		return fmt.Errorf("no self-managed connectors in state for cluster %s", utils.GetClusterDisplayName(s.SourceType, s.ClusterArn, s.ClusterID))
	}

	info.SelfManagedConnectors.Metrics = metrics
	return nil
}

// collectConnectMetrics dispatches to the configured metrics backend. The
// services and clients are the same ones the cluster-scan path uses; only the
// metric/query definitions (ConnectMetricDefinitions / ConnectQueryDefinitions)
// differ. Credentials come from the resolved cluster entry and are never logged
// or persisted.
func (s *SelfManagedConnectorsScanner) collectConnectMetrics(ctx context.Context) (*types.ConnectClusterMetrics, error) {
	if s.metricsClusterCreds == nil {
		return nil, fmt.Errorf("no cluster credentials resolved for metrics collection")
	}

	switch s.metricsSource {
	case "jolokia":
		return s.collectConnectJolokiaMetrics(ctx, *s.metricsClusterCreds)
	case "prometheus":
		return s.collectConnectPrometheusMetrics(ctx, *s.metricsClusterCreds)
	default:
		return nil, fmt.Errorf("unsupported metrics source: %s", s.metricsSource)
	}
}

// toConnectClusterMetrics maps the shared collector output into the
// Connect-specific envelope. It is the single boundary where the broker-shaped
// ProcessedClusterMetrics is narrowed to Connect-meaningful fields: the broker
// metadata and region/cluster_arn are dropped, and the producing backend
// (jolokia|prometheus) is recorded as metrics_source. The shared JMX/Prometheus
// services are left untouched so the broker cluster-scan path is unaffected.
func toConnectClusterMetrics(pcm *types.ProcessedClusterMetrics, source types.MetricBackend) *types.ConnectClusterMetrics {
	if pcm == nil {
		return nil
	}
	return &types.ConnectClusterMetrics{
		Metadata: types.ConnectMetricMetadata{
			StartDate:     pcm.Metadata.StartDate,
			EndDate:       pcm.Metadata.EndDate,
			Period:        pcm.Metadata.Period,
			MetricsSource: source,
		},
		Metrics:    pcm.Metrics,
		Aggregates: pcm.Aggregates,
		QueryInfo:  pcm.QueryInfo,
	}
}

func (s *SelfManagedConnectorsScanner) collectConnectJolokiaMetrics(ctx context.Context, creds types.OSKClusterAuth) (*types.ConnectClusterMetrics, error) {
	if !creds.HasJolokiaConfig() {
		return nil, fmt.Errorf("no jolokia configuration in credentials for cluster %s", creds.ID)
	}

	// Validation already enforced these parse; ignore the errors here.
	duration, _ := time.ParseDuration(s.metricsDuration)
	interval, _ := time.ParseDuration(s.metricsInterval)

	slog.Info("collecting Connect Jolokia metrics", "cluster", creds.ID, "duration", duration, "interval", interval)

	var jolokiaOpts []client.JolokiaOption
	if creds.Jolokia.Auth != nil {
		jolokiaOpts = append(jolokiaOpts, client.WithJolokiaBasicAuth(creds.Jolokia.Auth.Username, creds.Jolokia.Auth.Password))
	}
	if creds.Jolokia.TLS != nil {
		jolokiaOpts = append(jolokiaOpts, client.WithJolokiaTLS(creds.Jolokia.TLS.CACert, creds.Jolokia.TLS.InsecureSkipVerify))
	}

	jmxService := jmx.NewJMXService(creds.Jolokia.Endpoints, jmx.ConnectMetricDefinitions(), "worker", jolokiaOpts...)
	pcm, err := jmxService.CollectOverDuration(ctx, duration, interval)
	if err != nil {
		return nil, err
	}
	return toConnectClusterMetrics(pcm, types.MetricBackendJolokia), nil
}

func (s *SelfManagedConnectorsScanner) collectConnectPrometheusMetrics(ctx context.Context, creds types.OSKClusterAuth) (*types.ConnectClusterMetrics, error) {
	if !creds.HasPrometheusConfig() {
		return nil, fmt.Errorf("no prometheus configuration in credentials for cluster %s", creds.ID)
	}

	queryRange, _ := utils.ParseDurationDays(s.metricsRange)

	slog.Info("collecting Connect Prometheus metrics", "cluster", creds.ID, "range", s.metricsRange)

	var promOpts []client.PrometheusOption
	if creds.Prometheus.Auth != nil {
		promOpts = append(promOpts, client.WithPrometheusBasicAuth(creds.Prometheus.Auth.Username, creds.Prometheus.Auth.Password))
	}
	if creds.Prometheus.TLS != nil {
		promOpts = append(promOpts, client.WithPrometheusTLS(creds.Prometheus.TLS.CACert, creds.Prometheus.TLS.InsecureSkipVerify))
	}

	var labels map[string]string
	if creds.Prometheus.Filter != nil {
		labels = creds.Prometheus.Filter.Labels
	}

	promClient := client.NewPrometheusClient(creds.Prometheus.URL, promOpts...)
	promService := prometheussvc.NewPrometheusService(promClient, prometheussvc.ConnectQueryDefinitions(), labels)
	pcm, err := promService.CollectMetrics(ctx, queryRange)
	if err != nil {
		return nil, err
	}
	return toConnectClusterMetrics(pcm, types.MetricBackendPrometheus), nil
}
