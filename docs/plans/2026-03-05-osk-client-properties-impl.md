# OSK Client Properties Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace OSK's custom YAML credentials with standard Kafka `client.properties` format.

**Architecture:**
- Parse client.properties using `magiconair/properties` library
- Map Kafka properties to sarama configuration
- Support both JKS and PEM certificates using `keystore-go` library
- Leave MSK completely unchanged

**Tech Stack:** Go 1.25, Shopify/sarama (Kafka client), magiconair/properties (properties parser), pavlo-v-chernykh/keystore-go (JKS support)

---

## Task 1: Add Dependencies

**Files:**
- Modify: `go.mod`

**Step 1: Add magiconair/properties**

```bash
go get github.com/magiconair/properties@latest
```

**Step 2: Add keystore-go**

```bash
go get github.com/pavlo-v-chernykh/keystore-go/v4@v4.5.0
```

**Step 3: Tidy dependencies**

```bash
go mod tidy
```

**Step 4: Verify**

```bash
grep -E "(magiconair/properties|keystore-go)" go.mod
```

Expected: Both libraries listed

**Step 5: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add properties parser and JKS support for OSK"
```

---

## Task 2: Create Kafka Config Parser

**Files:**
- Create: `internal/config/kafka_properties.go`
- Create: `internal/config/kafka_properties_test.go`

**Step 1: Write failing test**

```go
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseKafkaProperties(t *testing.T) {
	tests := []struct {
		name             string
		properties       string
		wantBootstrap    []string
		wantProtocol     string
		wantSASLMech     string
		wantSASLUser     string
		wantErr          bool
	}{
		{
			name: "SASL_SSL with SCRAM-SHA-256",
			properties: `
bootstrap.servers=broker1:9092,broker2:9092
security.protocol=SASL_SSL
sasl.mechanism=SCRAM-SHA-256
sasl.username=alice
sasl.password=secret
ssl.ca.location=/path/to/ca.pem
`,
			wantBootstrap: []string{"broker1:9092", "broker2:9092"},
			wantProtocol:  "SASL_SSL",
			wantSASLMech:  "SCRAM-SHA-256",
			wantSASLUser:  "alice",
			wantErr:       false,
		},
		{
			name: "PLAINTEXT",
			properties: `
bootstrap.servers=localhost:9092
security.protocol=PLAINTEXT
`,
			wantBootstrap: []string{"localhost:9092"},
			wantProtocol:  "PLAINTEXT",
			wantErr:       false,
		},
		{
			name: "missing bootstrap.servers",
			properties: `
security.protocol=PLAINTEXT
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			propFile := filepath.Join(tmpDir, "client.properties")
			err := os.WriteFile(propFile, []byte(tt.properties), 0644)
			require.NoError(t, err)

			config, err := ParseKafkaProperties(propFile)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantBootstrap, config.BootstrapServers)
			assert.Equal(t, tt.wantProtocol, config.SecurityProtocol)
			if tt.wantSASLUser != "" {
				assert.Equal(t, tt.wantSASLUser, config.SASLUsername)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/config/... -v
```

Expected: Compilation errors (ParseKafkaProperties not defined)

**Step 3: Implement parser**

```go
package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/magiconair/properties"
	keystore "github.com/pavlo-v-chernykh/keystore-go/v4"
)

// KafkaProperties represents parsed Kafka configuration from client.properties
type KafkaProperties struct {
	BootstrapServers []string
	SecurityProtocol string

	// SASL
	SASLMechanism string
	SASLUsername  string
	SASLPassword  string

	// SSL/TLS - PEM format
	SSLCALocation          string
	SSLCertificateLocation string
	SSLKeyLocation         string

	// SSL/TLS - JKS format
	SSLTruststoreLocation string
	SSLTruststorePassword string
	SSLKeystoreLocation   string
	SSLKeystorePassword   string
	SSLKeyPassword        string

	// KCP metadata
	Metadata map[string]string

	// Prepared TLS config (populated after parsing)
	TLSConfig *tls.Config
}

// ParseKafkaProperties reads a Kafka client.properties file
func ParseKafkaProperties(filePath string) (*KafkaProperties, error) {
	props, err := properties.LoadFile(filePath, properties.UTF8)
	if err != nil {
		return nil, fmt.Errorf("failed to load properties file: %w", err)
	}

	config := &KafkaProperties{
		Metadata: make(map[string]string),
	}

	// Parse bootstrap servers (required)
	bootstrapStr := props.GetString("bootstrap.servers", "")
	if bootstrapStr == "" {
		return nil, fmt.Errorf("bootstrap.servers is required")
	}
	config.BootstrapServers = strings.Split(bootstrapStr, ",")
	for i := range config.BootstrapServers {
		config.BootstrapServers[i] = strings.TrimSpace(config.BootstrapServers[i])
	}

	// Parse security protocol
	config.SecurityProtocol = props.GetString("security.protocol", "PLAINTEXT")

	// Parse SASL config
	config.SASLMechanism = props.GetString("sasl.mechanism", "")
	config.SASLUsername = props.GetString("sasl.username", "")
	config.SASLPassword = props.GetString("sasl.password", "")

	// Parse SSL/TLS config - PEM format
	config.SSLCALocation = props.GetString("ssl.ca.location", "")
	config.SSLCertificateLocation = props.GetString("ssl.certificate.location", "")
	config.SSLKeyLocation = props.GetString("ssl.key.location", "")

	// Parse SSL/TLS config - JKS format
	config.SSLTruststoreLocation = props.GetString("ssl.truststore.location", "")
	config.SSLTruststorePassword = props.GetString("ssl.truststore.password", "")
	config.SSLKeystoreLocation = props.GetString("ssl.keystore.location", "")
	config.SSLKeystorePassword = props.GetString("ssl.keystore.password", "")
	config.SSLKeyPassword = props.GetString("ssl.key.password", "")

	// Parse KCP metadata
	for _, key := range props.Keys() {
		if strings.HasPrefix(key, "kcp.") {
			metaKey := strings.TrimPrefix(key, "kcp.")
			config.Metadata[metaKey] = props.MustGetString(key)
		}
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, err
	}

	// Prepare TLS config if needed
	if config.NeedsTLS() {
		tlsConfig, err := config.BuildTLSConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to build TLS config: %w", err)
		}
		config.TLSConfig = tlsConfig
	}

	return config, nil
}

// Validate checks if the configuration is valid
func (kp *KafkaProperties) Validate() error {
	switch kp.SecurityProtocol {
	case "PLAINTEXT":
		// No additional validation needed

	case "SSL":
		if !kp.HasSSLConfig() {
			return fmt.Errorf("SSL security.protocol requires SSL configuration (PEM or JKS)")
		}

	case "SASL_PLAINTEXT":
		if kp.SASLMechanism == "" {
			return fmt.Errorf("SASL_PLAINTEXT requires sasl.mechanism")
		}
		if kp.SASLUsername == "" || kp.SASLPassword == "" {
			return fmt.Errorf("SASL requires sasl.username and sasl.password")
		}

	case "SASL_SSL":
		if kp.SASLMechanism == "" {
			return fmt.Errorf("SASL_SSL requires sasl.mechanism")
		}
		if kp.SASLUsername == "" || kp.SASLPassword == "" {
			return fmt.Errorf("SASL requires sasl.username and sasl.password")
		}
		if !kp.HasSSLConfig() {
			return fmt.Errorf("SASL_SSL requires SSL configuration (PEM or JKS)")
		}

	default:
		return fmt.Errorf("unsupported security.protocol: %s", kp.SecurityProtocol)
	}

	return nil
}

// NeedsTLS returns true if TLS encryption is needed
func (kp *KafkaProperties) NeedsTLS() bool {
	return kp.SecurityProtocol == "SSL" || kp.SecurityProtocol == "SASL_SSL"
}

// HasSSLConfig returns true if SSL/TLS config is present (PEM or JKS)
func (kp *KafkaProperties) HasSSLConfig() bool {
	hasPEM := kp.SSLCALocation != "" || kp.SSLCertificateLocation != "" || kp.SSLKeyLocation != ""
	hasJKS := kp.SSLTruststoreLocation != "" || kp.SSLKeystoreLocation != ""
	return hasPEM || hasJKS
}

// BuildTLSConfig creates a tls.Config from SSL properties
func (kp *KafkaProperties) BuildTLSConfig() (*tls.Config, error) {
	tlsConfig := &tls.Config{}

	// Try PEM format first
	if kp.SSLCALocation != "" {
		caCert, err := os.ReadFile(kp.SSLCALocation)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA cert: %w", err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA cert")
		}
		tlsConfig.RootCAs = caCertPool
	}

	if kp.SSLCertificateLocation != "" && kp.SSLKeyLocation != "" {
		cert, err := tls.LoadX509KeyPair(kp.SSLCertificateLocation, kp.SSLKeyLocation)
		if err != nil {
			return nil, fmt.Errorf("failed to load client cert/key: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	// Try JKS format if PEM not provided
	if kp.SSLTruststoreLocation != "" {
		caCerts, err := loadJKSTruststore(kp.SSLTruststoreLocation, kp.SSLTruststorePassword)
		if err != nil {
			return nil, fmt.Errorf("failed to load truststore: %w", err)
		}
		caCertPool := x509.NewCertPool()
		for _, cert := range caCerts {
			caCertPool.AddCert(cert)
		}
		tlsConfig.RootCAs = caCertPool
	}

	if kp.SSLKeystoreLocation != "" {
		keyPassword := kp.SSLKeyPassword
		if keyPassword == "" {
			keyPassword = kp.SSLKeystorePassword
		}
		cert, err := loadJKSKeystore(kp.SSLKeystoreLocation, kp.SSLKeystorePassword, keyPassword)
		if err != nil {
			return nil, fmt.Errorf("failed to load keystore: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{*cert}
	}

	return tlsConfig, nil
}

func loadJKSTruststore(path, password string) ([]*x509.Certificate, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	ks := keystore.New()
	if err := ks.Load(f, []byte(password)); err != nil {
		return nil, fmt.Errorf("failed to load JKS: %w", err)
	}

	var certs []*x509.Certificate
	for _, alias := range ks.Aliases() {
		if ks.IsTrustedCertificateEntry(alias) {
			cert, err := ks.GetTrustedCertificateEntry(alias)
			if err != nil {
				continue
			}
			x509Cert, err := x509.ParseCertificate(cert.Certificate.Content)
			if err != nil {
				continue
			}
			certs = append(certs, x509Cert)
		}
	}

	return certs, nil
}

func loadJKSKeystore(path, keystorePassword, keyPassword string) (*tls.Certificate, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	ks := keystore.New()
	if err := ks.Load(f, []byte(keystorePassword)); err != nil {
		return nil, fmt.Errorf("failed to load JKS: %w", err)
	}

	for _, alias := range ks.Aliases() {
		if ks.IsPrivateKeyEntry(alias) {
			entry, err := ks.GetPrivateKeyEntry(alias, []byte(keyPassword))
			if err != nil {
				continue
			}

			privateKey, err := x509.ParsePKCS8PrivateKey(entry.PrivateKey)
			if err != nil {
				continue
			}

			var certChain [][]byte
			for _, certData := range entry.CertificateChain {
				certChain = append(certChain, certData.Content)
			}

			return &tls.Certificate{
				PrivateKey:  privateKey,
				Certificate: certChain,
			}, nil
		}
	}

	return nil, fmt.Errorf("no private key entry found in keystore")
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/config/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: add Kafka client.properties parser with JKS/PEM support"
```

---

## Task 3: Update OSK Scan Command

**Files:**
- Modify: `cmd/scan/clusters/cmd_scan_clusters.go`

**Step 1: Add new flags for OSK**

```go
var (
	// Existing flags
	sourceType      string
	stateFile       string
	credentialsFile string  // Keep for MSK only

	// New OSK flags
	clientPropertiesFile string
	clusterName          string

	// Scan options (existing)
	skipTopics      bool
	skipACLs        bool
	skipConnectors  bool
)

func init() {
	scanClustersCmd.Flags().StringVar(&sourceType, "source-type", "", "Source type: msk or osk")
	scanClustersCmd.Flags().StringVar(&stateFile, "state-file", "", "Path to state file")

	// MSK flag
	scanClustersCmd.Flags().StringVar(&credentialsFile, "credentials-file", "", "Path to credentials YAML (for MSK)")

	// OSK flags
	scanClustersCmd.Flags().StringVar(&clientPropertiesFile, "client-properties", "", "Path to client.properties file (for OSK)")
	scanClustersCmd.Flags().StringVar(&clusterName, "cluster-name", "", "Cluster name (required for OSK)")

	// ... existing scan option flags ...
}
```

**Step 2: Update validation logic**

```go
func runScanClusters(cmd *cobra.Command, args []string) error {
	// Validate common flags
	if sourceType == "" {
		return fmt.Errorf("--source-type is required (msk or osk)")
	}
	if stateFile == "" {
		return fmt.Errorf("--state-file is required")
	}

	// Source-specific validation
	switch sourceType {
	case "msk":
		if credentialsFile == "" {
			return fmt.Errorf("--credentials-file is required for MSK")
		}
	case "osk":
		if clientPropertiesFile == "" {
			return fmt.Errorf("--client-properties is required for OSK")
		}
		if clusterName == "" {
			return fmt.Errorf("--cluster-name is required for OSK")
		}
	default:
		return fmt.Errorf("invalid --source-type: %s (must be 'msk' or 'osk')", sourceType)
	}

	// ... rest of function ...
}
```

**Step 3: Update source creation logic**

```go
func runScanClusters(cmd *cobra.Command, args []string) error {
	// ... validation ...

	var source sources.Source

	switch sourceType {
	case "msk":
		// MSK logic unchanged
		source = msk.NewMSKSource()
		if err := source.LoadCredentials(credentialsFile); err != nil {
			return fmt.Errorf("failed to load MSK credentials: %w", err)
		}

	case "osk":
		// Parse client.properties
		kafkaProps, err := config.ParseKafkaProperties(clientPropertiesFile)
		if err != nil {
			return fmt.Errorf("failed to parse client.properties: %w", err)
		}

		// Create OSK source
		source = osk.NewOSKSourceFromProperties(clusterName, kafkaProps)
	}

	// ... rest of scanning logic is unchanged ...
}
```

**Step 4: Run build to verify**

```bash
go build ./cmd/scan/clusters/...
```

Expected: Clean build

**Step 5: Commit**

```bash
git add cmd/scan/clusters/cmd_scan_clusters.go
git commit -m "refactor: OSK scan accepts client.properties instead of YAML"
```

---

## Task 4: Update OSK Source

**Files:**
- Modify: `internal/sources/osk/osk_source.go`

**Step 1: Add new constructor**

```go
// NewOSKSourceFromProperties creates an OSK source from parsed Kafka properties
func NewOSKSourceFromProperties(clusterName string, props *config.KafkaProperties) *OSKSource {
	return &OSKSource{
		clusterName:     clusterName,
		kafkaProperties: props,
	}
}

type OSKSource struct {
	clusterName     string
	kafkaProperties *config.KafkaProperties
}
```

**Step 2: Update createKafkaAdmin method**

```go
func (s *OSKSource) createKafkaAdmin() (client.KafkaAdmin, error) {
	kafkaVersion := sarama.V2_8_0_0
	config := sarama.NewConfig()

	client.configureCommonSettings(config, "kcp-osk-scanner", kafkaVersion)

	// Configure based on security protocol
	switch s.kafkaProperties.SecurityProtocol {
	case "PLAINTEXT":
		// No auth, no TLS
		config.Net.TLS.Enable = false
		config.Net.SASL.Enable = false

	case "SSL":
		// mTLS
		config.Net.TLS.Enable = true
		config.Net.TLS.Config = s.kafkaProperties.TLSConfig
		config.Net.SASL.Enable = false

	case "SASL_PLAINTEXT":
		// SASL without TLS
		config.Net.TLS.Enable = false
		if err := s.configureSASL(config); err != nil {
			return nil, err
		}

	case "SASL_SSL":
		// SASL with TLS
		config.Net.TLS.Enable = true
		config.Net.TLS.Config = s.kafkaProperties.TLSConfig
		if err := s.configureSASL(config); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unsupported security.protocol: %s", s.kafkaProperties.SecurityProtocol)
	}

	// Create admin client
	admin, err := sarama.NewClusterAdmin(s.kafkaProperties.BootstrapServers, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kafka admin client: %w", err)
	}

	return &client.KafkaAdminClient{Admin: admin, Config: config}, nil
}

func (s *OSKSource) configureSASL(config *sarama.Config) error {
	config.Net.SASL.Enable = true
	config.Net.SASL.User = s.kafkaProperties.SASLUsername
	config.Net.SASL.Password = s.kafkaProperties.SASLPassword

	switch s.kafkaProperties.SASLMechanism {
	case "SCRAM-SHA-256":
		config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &client.XDGSCRAMClient{HashGeneratorFcn: client.SHA256}
		}
		config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA256

	case "SCRAM-SHA-512":
		config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &client.XDGSCRAMClient{HashGeneratorFcn: client.SHA512}
		}
		config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512

	case "PLAIN":
		config.Net.SASL.Mechanism = sarama.SASLTypePlaintext

	default:
		return fmt.Errorf("unsupported sasl.mechanism: %s", s.kafkaProperties.SASLMechanism)
	}

	return nil
}
```

**Step 3: Update Scan method**

```go
func (s *OSKSource) Scan(ctx context.Context, opts sources.ScanOptions) (*sources.ScanResult, error) {
	slog.Info("starting OSK cluster scan",
		"cluster", s.clusterName,
		"security_protocol", s.kafkaProperties.SecurityProtocol,
		"bootstrap_servers", s.kafkaProperties.BootstrapServers)

	// Create admin client
	kafkaAdmin, err := s.createKafkaAdmin()
	if err != nil {
		return nil, fmt.Errorf("failed to create Kafka admin client: %w", err)
	}
	defer kafkaAdmin.Close()

	// Scan resources
	kafkaAdminInfo, err := s.scanKafkaResources(kafkaAdmin, opts)
	if err != nil {
		return nil, err
	}

	// Build result
	cluster := types.OSKDiscoveredCluster{
		ID:                          s.clusterName,
		BootstrapServers:            s.kafkaProperties.BootstrapServers,
		KafkaAdminClientInformation: *kafkaAdminInfo,
		Metadata:                    s.kafkaProperties.Metadata,
	}

	return &sources.ScanResult{
		SourceType:  sources.SourceTypeOSK,
		OSKClusters: []types.OSKDiscoveredCluster{cluster},
	}, nil
}
```

**Step 4: Remove old YAML-based methods**

Remove `LoadCredentials` and any YAML parsing logic from OSKSource.

**Step 5: Commit**

```bash
git add internal/sources/osk/osk_source.go
git commit -m "refactor: OSK source uses client.properties instead of YAML"
```

---

## Task 5: Create Test client.properties Files

**Files:**
- Create: `test/credentials/osk-plaintext.properties`
- Create: `test/credentials/osk-kraft.properties`
- Create: `test/credentials/osk-sasl.properties`
- Create: `test/credentials/osk-tls.properties`
- Delete: Old YAML files

**Step 1: Create plaintext.properties**

```properties
bootstrap.servers=localhost:9092
security.protocol=PLAINTEXT
kcp.environment=test
kcp.location=docker-local-plaintext
```

**Step 2: Create kraft.properties**

```properties
bootstrap.servers=localhost:9095
security.protocol=PLAINTEXT
kcp.environment=test
kcp.location=docker-local-kraft
```

**Step 3: Create sasl.properties**

```properties
bootstrap.servers=localhost:9093
security.protocol=SASL_SSL
sasl.mechanism=SCRAM-SHA-256
sasl.username=kafkauser
sasl.password=kafkapass
kcp.environment=test
kcp.location=docker-local-sasl
```

**Step 4: Create tls.properties**

```properties
bootstrap.servers=localhost:9094
security.protocol=SSL
ssl.ca.location=/Users/tom.underhill/dev/kcp/test/docker/certs/ca-cert.pem
ssl.certificate.location=/Users/tom.underhill/dev/kcp/test/docker/certs/client-cert.pem
ssl.key.location=/Users/tom.underhill/dev/kcp/test/docker/certs/client-key.pem
kcp.environment=test
kcp.location=docker-local-tls
```

**Step 5: Update Makefile**

```makefile
test-all-envs:
	@echo "Testing OSK scanning against all Kafka configurations..."
	@echo "\n=== Testing ZooKeeper-based cluster (Plaintext) ==="
	$(MAKE) test-env-up-plaintext
	./kcp scan clusters --source-type osk \
		--client-properties test/credentials/osk-plaintext.properties \
		--cluster-name test-kafka-plaintext \
		--state-file test-state-plaintext.json
	$(MAKE) test-env-down

	@echo "\n=== Testing KRaft-based cluster (Plaintext) ==="
	$(MAKE) test-env-up-kraft
	./kcp scan clusters --source-type osk \
		--client-properties test/credentials/osk-kraft.properties \
		--cluster-name test-kafka-kraft \
		--state-file test-state-kraft.json
	$(MAKE) test-env-down

	@echo "\n=== Testing SASL/SCRAM authentication ==="
	$(MAKE) test-env-up-sasl
	./kcp scan clusters --source-type osk \
		--client-properties test/credentials/osk-sasl.properties \
		--cluster-name test-kafka-sasl \
		--state-file test-state-sasl.json
	$(MAKE) test-env-down

	@echo "\n=== Testing TLS/mTLS authentication ==="
	$(MAKE) test-env-up-tls
	./kcp scan clusters --source-type osk \
		--client-properties test/credentials/osk-tls.properties \
		--cluster-name test-kafka-tls \
		--state-file test-state-tls.json
	$(MAKE) test-env-down

	@echo "\n✅ All environment tests passed!"
```

**Step 6: Delete old YAML files and commit**

```bash
rm test/credentials/osk-credentials-*.yaml
git add test/credentials/ Makefile
git commit -m "test: use client.properties for OSK test credentials"
```

---

## Task 6: Create Example Files

**Files:**
- Create: `examples/osk-sasl-ssl.properties`
- Create: `examples/osk-mtls.properties`
- Create: `examples/osk-plaintext.properties`

**Step 1: Create SASL/SSL example**

```properties
# OSK cluster with SASL/SCRAM and TLS encryption (most common for production)
bootstrap.servers=broker1.example.com:9092,broker2.example.com:9092,broker3.example.com:9092

security.protocol=SASL_SSL
sasl.mechanism=SCRAM-SHA-256
sasl.username=your-username
sasl.password=your-password

# TLS configuration (PEM format - recommended for Go/Python/Node.js)
ssl.ca.location=/path/to/ca-cert.pem

# Or use JKS format (common for Java clients)
# ssl.truststore.location=/path/to/truststore.jks
# ssl.truststore.password=changeit

# Optional KCP metadata
kcp.environment=production
kcp.location=datacenter-1
```

**Step 2: Create mTLS example**

```properties
# OSK cluster with mTLS (mutual TLS) authentication
bootstrap.servers=secure-broker1.example.com:9093,secure-broker2.example.com:9093

security.protocol=SSL

# Client certificates for authentication (PEM format)
ssl.ca.location=/path/to/ca-cert.pem
ssl.certificate.location=/path/to/client-cert.pem
ssl.key.location=/path/to/client-key.pem

# Or use JKS keystores (Java clients)
# ssl.truststore.location=/path/to/truststore.jks
# ssl.truststore.password=changeit
# ssl.keystore.location=/path/to/keystore.jks
# ssl.keystore.password=changeit
# ssl.key.password=changeit

kcp.environment=production
kcp.location=on-prem-datacenter
```

**Step 3: Create plaintext example**

```properties
# OSK cluster - unauthenticated plaintext (development/testing only)
bootstrap.servers=localhost:9092

security.protocol=PLAINTEXT

kcp.environment=local
kcp.location=docker-compose
```

**Step 4: Commit**

```bash
git add examples/
git commit -m "docs: add example client.properties files for OSK"
```

---

## Task 7: Update Documentation

**Files:**
- Modify: `docs/README.md`

**Step 1: Add OSK section**

Add to README after installation section:

```markdown
## OSK Scanning with client.properties

KCP supports scanning Open Source Kafka (OSK) clusters using standard Kafka `client.properties` files.

### Example client.properties

```properties
# Connection
bootstrap.servers=broker1:9092,broker2:9092

# Security
security.protocol=SASL_SSL
sasl.mechanism=SCRAM-SHA-256
sasl.username=kafkauser
sasl.password=secretpass

# TLS (PEM format)
ssl.ca.location=/path/to/ca.pem

# Or use JKS format (Java clients)
# ssl.truststore.location=/path/to/truststore.jks
# ssl.truststore.password=changeit

# Optional metadata
kcp.environment=production
```

### Scanning OSK Clusters

```bash
kcp scan clusters --source-type osk \
  --client-properties /path/to/client.properties \
  --cluster-name production-cluster \
  --state-file kcp-state.json
```

### Supported Security Protocols

- `PLAINTEXT` - No authentication, no encryption
- `SSL` - mTLS authentication with encryption
- `SASL_PLAINTEXT` - SASL authentication without encryption
- `SASL_SSL` - SASL authentication with TLS encryption (most common)

### Supported SASL Mechanisms

- `SCRAM-SHA-256` (common for OSK)
- `SCRAM-SHA-512` (required for AWS MSK)
- `PLAIN`

### Certificate Formats

Both PEM and JKS formats are supported:

**PEM** (recommended for Go/Python/Node.js clients):
```properties
ssl.ca.location=/path/to/ca.pem
ssl.certificate.location=/path/to/client-cert.pem
ssl.key.location=/path/to/client-key.pem
```

**JKS** (Java clients):
```properties
ssl.truststore.location=/path/to/truststore.jks
ssl.truststore.password=changeit
ssl.keystore.location=/path/to/keystore.jks
ssl.keystore.password=changeit
```

### Multiple Clusters

To scan multiple clusters, run the command once per cluster:

```bash
kcp scan clusters --source-type osk \
  --client-properties prod.properties \
  --cluster-name production \
  --state-file kcp-state.json

kcp scan clusters --source-type osk \
  --client-properties dev.properties \
  --cluster-name development \
  --state-file kcp-state.json
```

The state file merges results from each scan.
```

**Step 2: Commit**

```bash
git add docs/README.md
git commit -m "docs: add OSK client.properties documentation"
```

---

## Task 8: Remove OSK YAML Code

**Files:**
- Delete: `internal/types/osk_credentials.go`
- Delete: `internal/types/osk_credentials_test.go`

**Step 1: Delete YAML credential files**

```bash
rm internal/types/osk_credentials.go
rm internal/types/osk_credentials_test.go
```

**Step 2: Verify no references remain**

```bash
grep -r "OSKCredentials" internal/ cmd/
```

Expected: No matches (or only in comments)

**Step 3: Run tests**

```bash
make test
```

Expected: All tests pass

**Step 4: Commit**

```bash
git add -A
git commit -m "cleanup: remove OSK YAML credentials code"
```

---

## Task 9: Final Integration Testing

**Step 1: Build binary**

```bash
make build
```

Expected: Clean build

**Step 2: Run unit tests**

```bash
make test
```

Expected: All tests pass

**Step 3: Run integration tests**

```bash
make test-all-envs
```

Expected: All 4 OSK environments pass with client.properties

**Step 4: Verify MSK unchanged**

```bash
# Check MSK files haven't been modified
git diff HEAD~10 internal/sources/msk/
git diff HEAD~10 internal/types/msk_credentials.go
git diff HEAD~10 cmd/discover/
```

Expected: No changes to MSK code

**Step 5: Final commit**

```bash
git add -A
git commit -m "test: verify OSK client.properties integration"
```

---

## Success Criteria

- [ ] All unit tests pass
- [ ] All OSK integration tests pass with client.properties (`make test-all-envs`)
- [ ] Both JKS and PEM certificate formats supported
- [ ] MSK code completely unchanged (no modifications)
- [ ] Documentation updated with examples
- [ ] Example client.properties files provided
- [ ] Old OSK YAML code removed
- [ ] Code compiles without errors

## Notes

- **MSK is unchanged** - all MSK code remains exactly as is
- **OSK uses standard Kafka format** - no custom YAML
- **JKS support** via keystore-go library (used by cert-manager)
- **PEM support** via Go's standard crypto/tls
- **Properties parsing** via magiconair/properties library
- Run command once per cluster for multiple OSK clusters
