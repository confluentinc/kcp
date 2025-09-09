package schema_registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
)

type SchemaRegistryService struct {
		client schemaregistry.Client
		confluentCloudRegistryEndpoint string
    confluentCloudApiKey           string
    confluentCloudApiSecret        string
}

func NewSchemaRegistryService(client schemaregistry.Client) *SchemaRegistryService {
	return &SchemaRegistryService{client: client}
}

func (sr *SchemaRegistryService) ConfigureConfluentCloud(endpoint, apiKey, apiSecret string) {
	sr.confluentCloudRegistryEndpoint = endpoint
	sr.confluentCloudApiKey = apiKey
	sr.confluentCloudApiSecret = apiSecret
}

// Returns a list of all subjects in the schema registry
func (sr *SchemaRegistryService) GetAllSubjects() ([]string, error) {
	return sr.client.GetAllSubjects()
}

// Returns a list of all versions available for a subject
func (sr *SchemaRegistryService) GetAllSubjectVersions(subject string) ([]int, error) {
	return sr.client.GetAllVersions(subject)
}

// Returns full schema details with version, ID, and schema definition
func (sr *SchemaRegistryService) GetLatestSchema(subject string) (schemaregistry.SchemaMetadata, error) {
	return sr.client.GetLatestSchemaMetadata(subject)
}

type RegisterSchemaRequest struct {
    Schema     string `json:"schema"`
    SchemaType string `json:"schemaType"`
}

type RegisterSchemaResponse struct {
    ID int `json:"id"`
}

func (sr *SchemaRegistryService) RegisterSchema(subjectName, schema, schemaType string) (*RegisterSchemaResponse, error) {
    if sr.confluentCloudRegistryEndpoint == "" || sr.confluentCloudApiKey == "" || sr.confluentCloudApiSecret == "" {
        return nil, fmt.Errorf("Confluent Cloud not configured.")
    }

    payload := RegisterSchemaRequest{
        Schema:     schema,
        SchemaType: schemaType,
    }

    jsonData, err := json.Marshal(payload)
    if err != nil {
        return nil, fmt.Errorf("failed to marshal request: %v", err)
    }

    url := fmt.Sprintf("%s/subjects/%s/versions", sr.confluentCloudRegistryEndpoint, subjectName)

    req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %v", err)
    }

    req.Header.Set("Content-Type", "application/json")
    req.SetBasicAuth(sr.confluentCloudApiKey, sr.confluentCloudApiSecret)

    client := &http.Client{}
    resp, err := client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("failed to execute request: %v", err)
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("failed to read response: %v", err)
    }

    if resp.StatusCode != 200 {
        return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
    }

    var result RegisterSchemaResponse
    if err := json.Unmarshal(body, &result); err != nil {
        return nil, fmt.Errorf("failed to parse response: %v", err)
    }

    return &result, nil
}
