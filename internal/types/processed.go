package types

import (
	"time"

	"github.com/aws/aws-sdk-go-v2/service/kafka"
)

// ProcessedState represents the transformed output data structure
// This is what comes OUT of the frontend/API after processing the raw State data
// Same structure as State but with costs and metrics flattened for easier frontend consumption
type ProcessedState struct {
	Sources          []ProcessedSource      `json:"sources"`
	SchemaRegistries *SchemaRegistriesState `json:"schema_registries,omitempty"`
	KcpBuildInfo     interface{}            `json:"kcp_build_info,omitempty"`
	Timestamp        time.Time              `json:"timestamp"`
}

// ProcessedRegion mirrors DiscoveredRegion but with flattened costs and simplified clusters
type ProcessedRegion struct {
	Name           string                                      `json:"name"`
	Configurations []kafka.DescribeConfigurationRevisionOutput `json:"configurations"`
	Costs          ProcessedRegionCosts                        `json:"costs"`    // Flattened from raw AWS Cost Explorer data
	Clusters       []ProcessedCluster                          `json:"clusters"` // Simplified from full DiscoveredCluster data
}

type ProcessedRegionCosts struct {
	Region     string              `json:"region"`
	Metadata   CostMetadata        `json:"metadata"`
	Results    []ProcessedCost     `json:"results"`
	Aggregates ProcessedAggregates `json:"aggregates"`
	QueryInfo  CostQueryInfo       `json:"query_info"`
}

// AWS service name constants — single source of truth for Cost Explorer service filters.
// Frontend constants (cmd/ui/frontend/src/constants/index.ts AWS_SERVICES) should mirror these.
const (
	ServiceAWSCertificateManager = "AWS Certificate Manager"
	ServiceMSK                   = "Amazon Managed Streaming for Apache Kafka"
	ServiceEC2Other              = "EC2 - Other"
	ServiceELB                   = "Amazon Elastic Load Balancing"
	ServiceVPC                   = "Amazon Virtual Private Cloud"
)

// newServiceCostAggregates creates a ServiceCostAggregates with all maps initialized
func newServiceCostAggregates() ServiceCostAggregates {
	return ServiceCostAggregates{
		UnblendedCost:    make(map[string]any),
		BlendedCost:      make(map[string]any),
		AmortizedCost:    make(map[string]any),
		NetAmortizedCost: make(map[string]any),
		NetUnblendedCost: make(map[string]any),
	}
}

// ForService returns a pointer to the ServiceCostAggregates for the given service name,
// or nil if the service is not recognized.
func (a *ProcessedAggregates) ForService(name string) *ServiceCostAggregates {
	switch name {
	case ServiceAWSCertificateManager:
		return &a.AWSCertificateManager
	case ServiceMSK:
		return &a.AmazonManagedStreamingForApacheKafka
	case ServiceEC2Other:
		return &a.EC2Other
	case ServiceELB:
		return &a.ElasticLoadBalancing
	case ServiceVPC:
		return &a.AmazonVPC
	}
	return nil
}

// ProcessedAggregates represents the specific services we query
type ProcessedAggregates struct {
	AWSCertificateManager                ServiceCostAggregates `json:"AWS Certificate Manager"`
	AmazonManagedStreamingForApacheKafka ServiceCostAggregates `json:"Amazon Managed Streaming for Apache Kafka"`
	EC2Other                             ServiceCostAggregates `json:"EC2 - Other"`
	ElasticLoadBalancing                 ServiceCostAggregates `json:"Amazon Elastic Load Balancing"`
	AmazonVPC                            ServiceCostAggregates `json:"Amazon Virtual Private Cloud"`
}

// NewProcessedAggregates creates a new ProcessedAggregates with all maps initialized
func NewProcessedAggregates() ProcessedAggregates {
	return ProcessedAggregates{
		AWSCertificateManager:                newServiceCostAggregates(),
		AmazonManagedStreamingForApacheKafka: newServiceCostAggregates(),
		EC2Other:                             newServiceCostAggregates(),
		ElasticLoadBalancing:                 newServiceCostAggregates(),
		AmazonVPC:                            newServiceCostAggregates(),
	}
}

type ProcessedCost struct {
	Start     string                 `json:"start"`
	End       string                 `json:"end"`
	Service   string                 `json:"service"`
	UsageType string                 `json:"usage_type"`
	Values    ProcessedCostBreakdown `json:"values"`
}

type ProcessedCostBreakdown struct {
	UnblendedCost    float64 `json:"unblended_cost"`
	BlendedCost      float64 `json:"blended_cost"`
	AmortizedCost    float64 `json:"amortized_cost"`
	NetAmortizedCost float64 `json:"net_amortized_cost"`
	NetUnblendedCost float64 `json:"net_unblended_cost"`
}

// ProcessedCluster contains the complete cluster data with flattened metrics
// This is the full cluster information with processed metrics, unlike the simplified version in types.go
type ProcessedCluster struct {
	Name                        string                      `json:"name"`
	Arn                         string                      `json:"arn"`
	Region                      string                      `json:"region"`
	ClusterMetrics              ProcessedClusterMetrics     `json:"metrics"` // Flattened from raw CloudWatch metrics
	AWSClientInformation        AWSClientInformation        `json:"aws_client_information"`
	KafkaAdminClientInformation KafkaAdminClientInformation `json:"kafka_admin_client_information"`
	DiscoveredClients           []DiscoveredClient          `json:"discovered_clients"`
}

type ProcessedClusterMetrics struct {
	Region     string                     `json:"region"`
	ClusterArn string                     `json:"cluster_arn"`
	Metadata   MetricMetadata             `json:"metadata"`
	Metrics    []ProcessedMetric          `json:"results"`
	Aggregates map[string]MetricAggregate `json:"aggregates"`
	QueryInfo  []MetricQueryInfo          `json:"query_info"`
	// OSK-specific fields (optional, omitempty for MSK clusters)
	Environment string `json:"environment,omitempty"`
	Location    string `json:"location,omitempty"`
}

type ProcessedMetric struct {
	Start string   `json:"start"`
	End   string   `json:"end"`
	Label string   `json:"label"`
	Value *float64 `json:"value"`
}

type MetricAggregate struct {
	Average *float64 `json:"avg"`
	Maximum *float64 `json:"max"`
	Minimum *float64 `json:"min"`
	P95     *float64 `json:"p95"`
	P99     *float64 `json:"p99"`
	// Count is the sample size of the aggregate. With `omitempty`,
	// "unknown" and "exactly 0 samples" both render as absent — treat
	// absence as "no sample data".
	Count int `json:"count,omitempty"`
}

type CostAggregate struct {
	Sum     *float64 `json:"sum"`
	Average *float64 `json:"avg"`
	Maximum *float64 `json:"max"`
	Minimum *float64 `json:"min"`
}

// ServiceCostAggregates represents cost aggregates for a single service
// Uses explicit fields for each metric type instead of a map
type ServiceCostAggregates struct {
	UnblendedCost    map[string]any `json:"unblended_cost"`
	BlendedCost      map[string]any `json:"blended_cost"`
	AmortizedCost    map[string]any `json:"amortized_cost"`
	NetAmortizedCost map[string]any `json:"net_amortized_cost"`
	NetUnblendedCost map[string]any `json:"net_unblended_cost"`
}

// SourceType represents the type of Kafka source
type SourceType string

const (
	SourceTypeMSK SourceType = "msk"
	SourceTypeOSK SourceType = "osk"
)

// ProcessedSource represents a unified source (MSK or OSK) with discriminated union
type ProcessedSource struct {
	Type    SourceType          `json:"type"`
	MSKData *ProcessedMSKSource `json:"msk_data,omitempty"`
	OSKData *ProcessedOSKSource `json:"osk_data,omitempty"`
}

// ProcessedMSKSource contains processed MSK data (regions)
type ProcessedMSKSource struct {
	Regions []ProcessedRegion `json:"regions"`
}

// ProcessedOSKSource contains processed OSK data (flat cluster array)
type ProcessedOSKSource struct {
	Clusters []ProcessedOSKCluster `json:"clusters"`
}

// ProcessedOSKCluster represents an OSK cluster in the API response
type ProcessedOSKCluster struct {
	ID                          string                      `json:"id"`
	BootstrapServers            []string                    `json:"bootstrap_servers"`
	KafkaAdminClientInformation KafkaAdminClientInformation `json:"kafka_admin_client_information"`
	ClusterMetrics              *ProcessedClusterMetrics    `json:"metrics,omitempty"`
	DiscoveredClients           []DiscoveredClient          `json:"discovered_clients"`
	Metadata                    OSKClusterMetadata          `json:"metadata"`
}
