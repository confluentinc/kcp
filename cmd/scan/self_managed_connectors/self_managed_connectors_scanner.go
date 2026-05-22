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
	jmx "github.com/confluentinc/kcp/internal/services/jmx"
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

	MetricsSource   string
	CredentialsFile string
	MetricsDuration string
	MetricsInterval string
	MetricsRange    string
}

type SelfManagedConnectorsScanner struct {
	StateFile  string
	State      *types.State
	SourceType types.SourceType
	ClusterArn string
	ClusterID  string
	client     ConnectAPIClient

	metricsSource   string
	credentialsFile string
	metricsDuration string
	metricsInterval string
	metricsRange    string
}

func NewSelfManagedConnectorsScanner(opts SelfManagedConnectorsScannerOpts) *SelfManagedConnectorsScanner {
	httpClient, err := createHTTPClient(opts.AuthMethod, opts.TlsAuth)
	if err != nil {
		slog.Error("failed to create HTTP client", "error", err)
	}

	connectClient := &HTTPConnectClient{
		baseURL:    opts.ConnectRestURL,
		httpClient: httpClient,
		authMethod: opts.AuthMethod,
		saslAuth:   opts.SaslScramAuth,
	}

	return &SelfManagedConnectorsScanner{
		StateFile:       opts.StateFile,
		State:           opts.State,
		SourceType:      opts.SourceType,
		ClusterArn:      opts.ClusterArn,
		ClusterID:       opts.ClusterID,
		client:          connectClient,
		metricsSource:   opts.MetricsSource,
		credentialsFile: opts.CredentialsFile,
		metricsDuration: opts.MetricsDuration,
		metricsInterval: opts.MetricsInterval,
		metricsRange:    opts.MetricsRange,
	}
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

	connectorNames, err := s.client.ListConnectors()
	if err != nil {
		return fmt.Errorf("failed to list connectors: %v", err)
	}

	fmt.Printf("  🔍 Found %d connectors\n", len(connectorNames))

	if len(connectorNames) == 0 {
		fmt.Printf("  ⏭️  No connectors found for cluster %s, skipping\n", clusterName)
		return nil
	}

	connectors := []types.SelfManagedConnector{}
	for _, name := range connectorNames {
		connector, err := s.getConnectorDetails(name)
		if err != nil {
			slog.Warn(fmt.Sprintf("⚠️ failed to get connector details for connector %s: %v", name, err))
			continue
		}
		connectors = append(connectors, connector)
	}

	fmt.Printf("  ✅ Successfully retrieved connector details for %d connectors\n", len(connectors))

	if err := s.updateStateWithConnectors(connectors); err != nil {
		return fmt.Errorf("failed to update state: %v", err)
	}

	if s.metricsSource != "" {
		slog.Info("collecting Connect metrics", "source", s.metricsSource, "cluster", clusterName)
		metrics, err := s.collectConnectMetrics(context.Background())
		if err != nil {
			slog.Warn("Connect metrics collection failed", "error", err)
		} else {
			if err := s.updateStateWithConnectMetrics(metrics); err != nil {
				slog.Warn("failed to update state with Connect metrics", "error", err)
			} else {
				slog.Info("collected Connect metrics", "data_points", len(metrics.Metrics), "cluster", clusterName)
			}
		}
	}

	if err := s.State.PersistStateFile(s.StateFile); err != nil {
		return fmt.Errorf("failed to save state file: %v", err)
	}

	slog.Info("self-managed connector scan complete", "cluster", clusterName)
	return nil
}

func (s *SelfManagedConnectorsScanner) getConnectorDetails(name string) (types.SelfManagedConnector, error) {
	connector := types.SelfManagedConnector{
		Name: name,
	}

	config, err := s.client.GetConnectorConfig(name)
	if err != nil {
		return connector, fmt.Errorf("failed to get config: %w", err)
	}
	connector.Config = config

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

	return connector, nil
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

// Adds basic authentication headers for SASL/SCRAM Connect clusters for the list/status/config endpoints.
func (c *HTTPConnectClient) addAuthHeaders(req *http.Request) {
	if c.authMethod == types.ConnectAuthMethodSaslScram {
		req.SetBasicAuth(c.saslAuth.Username, c.saslAuth.Password)
	}
}

func (s *SelfManagedConnectorsScanner) updateStateWithConnectors(connectors []types.SelfManagedConnector) error {
	clusterName := utils.GetClusterDisplayName(s.SourceType, s.ClusterArn, s.ClusterID)

	switch s.SourceType {
	case types.SourceTypeMSK:
		if s.State.MSKSources == nil {
			return fmt.Errorf("no MSK sources found in state file")
		}
		for i, region := range s.State.MSKSources.Regions {
			for j, cluster := range region.Clusters {
				if cluster.Arn == s.ClusterArn {
					s.State.MSKSources.Regions[i].Clusters[j].KafkaAdminClientInformation.SetSelfManagedConnectors(connectors)
					slog.Info(fmt.Sprintf("✅ updated cluster %s with self-managed connector information", clusterName))
					return nil
				}
			}
		}
		return fmt.Errorf("cluster with ARN %s not found in state file", s.ClusterArn)

	case types.SourceTypeOSK:
		if s.State.OSKSources == nil {
			return fmt.Errorf("no OSK sources found in state file")
		}
		for i, cluster := range s.State.OSKSources.Clusters {
			if cluster.ID == s.ClusterID {
				s.State.OSKSources.Clusters[i].KafkaAdminClientInformation.SetSelfManagedConnectors(connectors)
				slog.Info(fmt.Sprintf("✅ updated cluster %s with self-managed connector information", clusterName))
				return nil
			}
		}
		return fmt.Errorf("OSK cluster with ID %s not found in state file", s.ClusterID)

	default:
		return fmt.Errorf("unsupported source type: %s", s.SourceType)
	}
}

func (s *SelfManagedConnectorsScanner) collectConnectMetrics(ctx context.Context) (*types.ProcessedClusterMetrics, error) {
	creds, errs := types.NewOSKCredentialsFromFile(s.credentialsFile)
	if len(errs) > 0 {
		return nil, fmt.Errorf("failed to load credentials file: %v", errs)
	}

	// Find the cluster in credentials that matches our cluster ID
	clusterID := s.ClusterID
	if s.SourceType == types.SourceTypeMSK {
		clusterID = s.ClusterArn
	}

	var clusterCreds *types.OSKClusterAuth
	for i, c := range creds.Clusters {
		if c.ID == clusterID {
			clusterCreds = &creds.Clusters[i]
			break
		}
	}

	// If no exact match, use the first cluster in the credentials file
	if clusterCreds == nil {
		if len(creds.Clusters) == 0 {
			return nil, fmt.Errorf("no clusters found in credentials file")
		}
		clusterCreds = &creds.Clusters[0]
		slog.Info("using first cluster from credentials file for metrics", "cluster", clusterCreds.ID)
	}

	switch s.metricsSource {
	case "jolokia":
		return s.collectConnectJolokiaMetrics(ctx, *clusterCreds)
	case "prometheus":
		return s.collectConnectPrometheusMetrics(ctx, *clusterCreds)
	default:
		return nil, fmt.Errorf("unsupported metrics source: %s", s.metricsSource)
	}
}

func (s *SelfManagedConnectorsScanner) collectConnectJolokiaMetrics(ctx context.Context, clusterCreds types.OSKClusterAuth) (*types.ProcessedClusterMetrics, error) {
	if !clusterCreds.HasJolokiaConfig() {
		return nil, fmt.Errorf("no jolokia config in credentials for cluster %s", clusterCreds.ID)
	}

	duration, _ := time.ParseDuration(s.metricsDuration)
	interval, _ := time.ParseDuration(s.metricsInterval)

	slog.Info("collecting Connect Jolokia metrics", "cluster", clusterCreds.ID, "duration", duration, "interval", interval)

	var jolokiaOpts []client.JolokiaOption
	if clusterCreds.Jolokia.Auth != nil {
		jolokiaOpts = append(jolokiaOpts, client.WithJolokiaBasicAuth(clusterCreds.Jolokia.Auth.Username, clusterCreds.Jolokia.Auth.Password))
	}
	if clusterCreds.Jolokia.TLS != nil {
		jolokiaOpts = append(jolokiaOpts, client.WithJolokiaTLS(clusterCreds.Jolokia.TLS.CACert, clusterCreds.Jolokia.TLS.InsecureSkipVerify))
	}

	jmxService := jmx.NewJMXService(clusterCreds.Jolokia.Endpoints, jmx.ConnectMetricDefinitions(), jolokiaOpts...)
	return jmxService.CollectOverDuration(ctx, duration, interval)
}

func (s *SelfManagedConnectorsScanner) collectConnectPrometheusMetrics(ctx context.Context, clusterCreds types.OSKClusterAuth) (*types.ProcessedClusterMetrics, error) {
	if !clusterCreds.HasPrometheusConfig() {
		return nil, fmt.Errorf("no prometheus config in credentials for cluster %s", clusterCreds.ID)
	}

	queryRange, _ := utils.ParseDurationDays(s.metricsRange)

	slog.Info("collecting Connect Prometheus metrics", "cluster", clusterCreds.ID, "range", s.metricsRange)

	var promOpts []client.PrometheusOption
	if clusterCreds.Prometheus.Auth != nil {
		promOpts = append(promOpts, client.WithPrometheusBasicAuth(
			clusterCreds.Prometheus.Auth.Username,
			clusterCreds.Prometheus.Auth.Password,
		))
	}
	if clusterCreds.Prometheus.TLS != nil {
		promOpts = append(promOpts, client.WithPrometheusTLS(
			clusterCreds.Prometheus.TLS.CACert,
			clusterCreds.Prometheus.TLS.InsecureSkipVerify,
		))
	}

	var labels map[string]string
	if clusterCreds.Prometheus.Filter != nil {
		labels = clusterCreds.Prometheus.Filter.Labels
	}

	promClient := client.NewPrometheusClient(clusterCreds.Prometheus.URL, promOpts...)
	promService := prometheussvc.NewPrometheusService(promClient, prometheussvc.ConnectQueryDefinitions(), labels)
	return promService.CollectMetrics(ctx, queryRange)
}

func (s *SelfManagedConnectorsScanner) updateStateWithConnectMetrics(metrics *types.ProcessedClusterMetrics) error {
	switch s.SourceType {
	case types.SourceTypeMSK:
		if s.State.MSKSources == nil {
			return fmt.Errorf("no MSK sources found in state file")
		}
		for i, region := range s.State.MSKSources.Regions {
			for j, cluster := range region.Clusters {
				if cluster.Arn == s.ClusterArn {
					if s.State.MSKSources.Regions[i].Clusters[j].KafkaAdminClientInformation.SelfManagedConnectors == nil {
						return fmt.Errorf("no self-managed connectors in state for cluster %s", s.ClusterArn)
					}
					s.State.MSKSources.Regions[i].Clusters[j].KafkaAdminClientInformation.SelfManagedConnectors.Metrics = metrics
					return nil
				}
			}
		}
		return fmt.Errorf("cluster with ARN %s not found in state file", s.ClusterArn)

	case types.SourceTypeOSK:
		if s.State.OSKSources == nil {
			return fmt.Errorf("no OSK sources found in state file")
		}
		for i, cluster := range s.State.OSKSources.Clusters {
			if cluster.ID == s.ClusterID {
				if s.State.OSKSources.Clusters[i].KafkaAdminClientInformation.SelfManagedConnectors == nil {
					return fmt.Errorf("no self-managed connectors in state for cluster %s", s.ClusterID)
				}
				s.State.OSKSources.Clusters[i].KafkaAdminClientInformation.SelfManagedConnectors.Metrics = metrics
				return nil
			}
		}
		return fmt.Errorf("OSK cluster with ID %s not found in state file", s.ClusterID)

	default:
		return fmt.Errorf("unsupported source type: %s", s.SourceType)
	}
}
