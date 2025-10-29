package schema_registry

import (
	"errors"
	"testing"

	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
)

// MockSchemaRegistryClient is a mock implementation of SchemaRegistryClient interface
type MockSchemaRegistryClient struct {
	GetDefaultCompatibilityFunc func() (schemaregistry.Compatibility, error)
	GetAllSubjectsFunc          func() ([]string, error)
	GetLatestSchemaMetadataFunc func(subject string) (schemaregistry.SchemaMetadata, error)
	GetCompatibilityFunc        func(subject string) (schemaregistry.Compatibility, error)
	GetAllVersionsFunc          func(subject string) ([]int, error)
	GetSchemaMetadataFunc       func(subject string, version int) (schemaregistry.SchemaMetadata, error)
	GetAllContextsFunc          func() ([]string, error)
}

func (m *MockSchemaRegistryClient) GetDefaultCompatibility() (schemaregistry.Compatibility, error) {
	return m.GetDefaultCompatibilityFunc()
}

func (m *MockSchemaRegistryClient) GetAllSubjects() ([]string, error) {
	return m.GetAllSubjectsFunc()
}

func (m *MockSchemaRegistryClient) GetLatestSchemaMetadata(subject string) (schemaregistry.SchemaMetadata, error) {
	return m.GetLatestSchemaMetadataFunc(subject)
}

func (m *MockSchemaRegistryClient) GetCompatibility(subject string) (schemaregistry.Compatibility, error) {
	return m.GetCompatibilityFunc(subject)
}

func (m *MockSchemaRegistryClient) GetAllVersions(subject string) ([]int, error) {
	return m.GetAllVersionsFunc(subject)
}

func (m *MockSchemaRegistryClient) GetSchemaMetadata(subject string, version int) (schemaregistry.SchemaMetadata, error) {
	return m.GetSchemaMetadataFunc(subject, version)
}

func (m *MockSchemaRegistryClient) GetAllContexts() ([]string, error) {
	return m.GetAllContextsFunc()
}

func TestSchemaRegistryService_GetDefaultCompatibility(t *testing.T) {
	tests := []struct {
		name           string
		mockClient     *MockSchemaRegistryClient
		wantErr        bool
		wantCompatible schemaregistry.Compatibility
	}{
		{
			name: "GetDefaultCompatibility returns error",
			mockClient: &MockSchemaRegistryClient{
				GetDefaultCompatibilityFunc: func() (schemaregistry.Compatibility, error) {
					return 0, errors.New("connection failed")
				},
			},
			wantErr:        true,
			wantCompatible: 0,
		},
		{
			name: "successful get default compatibility",
			mockClient: &MockSchemaRegistryClient{
				GetDefaultCompatibilityFunc: func() (schemaregistry.Compatibility, error) {
					return schemaregistry.Backward, nil
				},
			},
			wantErr:        false,
			wantCompatible: schemaregistry.Backward,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srs := NewSchemaRegistryService(tt.mockClient)

			result, err := srs.GetDefaultCompatibility()

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.wantCompatible, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantCompatible, result)
			}
		})
	}
}

func TestSchemaRegistryService_GetAllSubjectsWithVersions(t *testing.T) {
	tests := []struct {
		name         string
		mockClient   *MockSchemaRegistryClient
		wantErr      bool
		wantErrMsg   string
		wantSubjects []types.Subject
	}{
		{
			name: "GetAllSubjects returns error",
			mockClient: &MockSchemaRegistryClient{
				GetAllSubjectsFunc: func() ([]string, error) {
					return nil, errors.New("connection timeout")
				},
			},
			wantErr:      true,
			wantErrMsg:   "failed to get all subjects: connection timeout",
			wantSubjects: nil,
		},
		{
			name: "GetLatestSchemaMetadata fails - subject is skipped",
			mockClient: &MockSchemaRegistryClient{
				GetAllSubjectsFunc: func() ([]string, error) {
					return []string{"test-subject"}, nil
				},
				GetLatestSchemaMetadataFunc: func(subject string) (schemaregistry.SchemaMetadata, error) {
					return schemaregistry.SchemaMetadata{}, errors.New("schema not found")
				},
			},
			wantErr:      false,
			wantSubjects: []types.Subject{},
		},
		{
			name: "GetCompatibility fails - subject continues with empty compatibility",
			mockClient: &MockSchemaRegistryClient{
				GetAllSubjectsFunc: func() ([]string, error) {
					return []string{"test-subject"}, nil
				},
				GetLatestSchemaMetadataFunc: func(subject string) (schemaregistry.SchemaMetadata, error) {
					return schemaregistry.SchemaMetadata{
						SchemaInfo: schemaregistry.SchemaInfo{
							Schema:     `{"type":"record","name":"Test"}`,
							SchemaType: "AVRO",
						},
						ID:      1,
						Version: 1,
						Subject: subject,
					}, nil
				},
				GetCompatibilityFunc: func(subject string) (schemaregistry.Compatibility, error) {
					return 0, errors.New("compatibility not set")
				},
				GetAllVersionsFunc: func(subject string) ([]int, error) {
					return []int{1}, nil
				},
				GetSchemaMetadataFunc: func(subject string, version int) (schemaregistry.SchemaMetadata, error) {
					return schemaregistry.SchemaMetadata{
						SchemaInfo: schemaregistry.SchemaInfo{
							Schema:     `{"type":"record","name":"Test"}`,
							SchemaType: "AVRO",
						},
						ID:      1,
						Version: version,
						Subject: subject,
					}, nil
				},
			},
			wantErr: false,
			wantSubjects: []types.Subject{
				{
					Name:          "test-subject",
					SchemaType:    "AVRO",
					Compatibility: "",
					Versions: []schemaregistry.SchemaMetadata{
						{
							SchemaInfo: schemaregistry.SchemaInfo{
								Schema:     `{"type":"record","name":"Test"}`,
								SchemaType: "AVRO",
							},
							ID:      1,
							Version: 1,
							Subject: "test-subject",
						},
					},
					Latest: schemaregistry.SchemaMetadata{
						SchemaInfo: schemaregistry.SchemaInfo{
							Schema:     `{"type":"record","name":"Test"}`,
							SchemaType: "AVRO",
						},
						ID:      1,
						Version: 1,
						Subject: "test-subject",
					},
				},
			},
		},
		{
			name: "GetAllVersions fails - subject is skipped",
			mockClient: &MockSchemaRegistryClient{
				GetAllSubjectsFunc: func() ([]string, error) {
					return []string{"test-subject"}, nil
				},
				GetLatestSchemaMetadataFunc: func(subject string) (schemaregistry.SchemaMetadata, error) {
					return schemaregistry.SchemaMetadata{
						SchemaInfo: schemaregistry.SchemaInfo{
							Schema:     `{"type":"record","name":"Test"}`,
							SchemaType: "AVRO",
						},
						ID:      1,
						Version: 1,
						Subject: subject,
					}, nil
				},
				GetCompatibilityFunc: func(subject string) (schemaregistry.Compatibility, error) {
					return schemaregistry.Backward, nil
				},
				GetAllVersionsFunc: func(subject string) ([]int, error) {
					return nil, errors.New("unable to fetch versions")
				},
			},
			wantErr:      false,
			wantSubjects: []types.Subject{},
		},
		{
			name: "GetSchemaMetadata fails for one version - version is skipped",
			mockClient: &MockSchemaRegistryClient{
				GetAllSubjectsFunc: func() ([]string, error) {
					return []string{"test-subject"}, nil
				},
				GetLatestSchemaMetadataFunc: func(subject string) (schemaregistry.SchemaMetadata, error) {
					return schemaregistry.SchemaMetadata{
						SchemaInfo: schemaregistry.SchemaInfo{
							Schema:     `{"type":"record","name":"Test"}`,
							SchemaType: "AVRO",
						},
						ID:      2,
						Version: 2,
						Subject: subject,
					}, nil
				},
				GetCompatibilityFunc: func(subject string) (schemaregistry.Compatibility, error) {
					return schemaregistry.Backward, nil
				},
				GetAllVersionsFunc: func(subject string) ([]int, error) {
					return []int{1, 2}, nil
				},
				GetSchemaMetadataFunc: func(subject string, version int) (schemaregistry.SchemaMetadata, error) {
					if version == 1 {
						return schemaregistry.SchemaMetadata{}, errors.New("version not found")
					}
					return schemaregistry.SchemaMetadata{
						SchemaInfo: schemaregistry.SchemaInfo{
							Schema:     `{"type":"record","name":"Test"}`,
							SchemaType: "AVRO",
						},
						ID:      2,
						Version: version,
						Subject: subject,
					}, nil
				},
			},
			wantErr: false,
			wantSubjects: []types.Subject{
				{
					Name:          "test-subject",
					SchemaType:    "AVRO",
					Compatibility: "BACKWARD",
					Versions: []schemaregistry.SchemaMetadata{
						{
							SchemaInfo: schemaregistry.SchemaInfo{
								Schema:     `{"type":"record","name":"Test"}`,
								SchemaType: "AVRO",
							},
							ID:      2,
							Version: 2,
							Subject: "test-subject",
						},
					},
					Latest: schemaregistry.SchemaMetadata{
						SchemaInfo: schemaregistry.SchemaInfo{
							Schema:     `{"type":"record","name":"Test"}`,
							SchemaType: "AVRO",
						},
						ID:      2,
						Version: 2,
						Subject: "test-subject",
					},
				},
			},
		},
		{
			name: "empty SchemaType defaults to AVRO for latest schema",
			mockClient: &MockSchemaRegistryClient{
				GetAllSubjectsFunc: func() ([]string, error) {
					return []string{"orders-value"}, nil
				},
				GetLatestSchemaMetadataFunc: func(subject string) (schemaregistry.SchemaMetadata, error) {
					return schemaregistry.SchemaMetadata{
						SchemaInfo: schemaregistry.SchemaInfo{
							Schema:     `{"type":"record","name":"Order"}`,
							SchemaType: "", // Empty - should default to AVRO
						},
						ID:      123,
						Version: 1,
						Subject: subject,
					}, nil
				},
				GetCompatibilityFunc: func(subject string) (schemaregistry.Compatibility, error) {
					return schemaregistry.Backward, nil
				},
				GetAllVersionsFunc: func(subject string) ([]int, error) {
					return []int{1}, nil
				},
				GetSchemaMetadataFunc: func(subject string, version int) (schemaregistry.SchemaMetadata, error) {
					return schemaregistry.SchemaMetadata{
						SchemaInfo: schemaregistry.SchemaInfo{
							Schema:     `{"type":"record","name":"Order"}`,
							SchemaType: "", // Empty - should default to AVRO
						},
						ID:      123,
						Version: version,
						Subject: subject,
					}, nil
				},
			},
			wantErr: false,
			wantSubjects: []types.Subject{
				{
					Name:          "orders-value",
					SchemaType:    "AVRO",
					Compatibility: "BACKWARD",
					Versions: []schemaregistry.SchemaMetadata{
						{
							SchemaInfo: schemaregistry.SchemaInfo{
								Schema:     `{"type":"record","name":"Order"}`,
								SchemaType: "AVRO",
							},
							ID:      123,
							Version: 1,
							Subject: "orders-value",
						},
					},
					Latest: schemaregistry.SchemaMetadata{
						SchemaInfo: schemaregistry.SchemaInfo{
							Schema:     `{"type":"record","name":"Order"}`,
							SchemaType: "AVRO",
						},
						ID:      123,
						Version: 1,
						Subject: "orders-value",
					},
				},
			},
		},
		{
			name: "successful with compatibility set to FORWARD",
			mockClient: &MockSchemaRegistryClient{
				GetAllSubjectsFunc: func() ([]string, error) {
					return []string{"users-value"}, nil
				},
				GetLatestSchemaMetadataFunc: func(subject string) (schemaregistry.SchemaMetadata, error) {
					return schemaregistry.SchemaMetadata{
						SchemaInfo: schemaregistry.SchemaInfo{
							Schema:     `{"type":"record","name":"User"}`,
							SchemaType: "AVRO",
						},
						ID:      456,
						Version: 1,
						Subject: subject,
					}, nil
				},
				GetCompatibilityFunc: func(subject string) (schemaregistry.Compatibility, error) {
					return schemaregistry.Forward, nil
				},
				GetAllVersionsFunc: func(subject string) ([]int, error) {
					return []int{1}, nil
				},
				GetSchemaMetadataFunc: func(subject string, version int) (schemaregistry.SchemaMetadata, error) {
					return schemaregistry.SchemaMetadata{
						SchemaInfo: schemaregistry.SchemaInfo{
							Schema:     `{"type":"record","name":"User"}`,
							SchemaType: "AVRO",
						},
						ID:      456,
						Version: version,
						Subject: subject,
					}, nil
				},
			},
			wantErr: false,
			wantSubjects: []types.Subject{
				{
					Name:          "users-value",
					SchemaType:    "AVRO",
					Compatibility: "FORWARD",
					Versions: []schemaregistry.SchemaMetadata{
						{
							SchemaInfo: schemaregistry.SchemaInfo{
								Schema:     `{"type":"record","name":"User"}`,
								SchemaType: "AVRO",
							},
							ID:      456,
							Version: 1,
							Subject: "users-value",
						},
					},
					Latest: schemaregistry.SchemaMetadata{
						SchemaInfo: schemaregistry.SchemaInfo{
							Schema:     `{"type":"record","name":"User"}`,
							SchemaType: "AVRO",
						},
						ID:      456,
						Version: 1,
						Subject: "users-value",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srs := NewSchemaRegistryService(tt.mockClient)

			result, err := srs.GetAllSubjectsWithVersions()

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, result)
				if tt.wantErrMsg != "" {
					assert.Equal(t, tt.wantErrMsg, err.Error())
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, tt.wantSubjects, result)
			}
		})
	}
}
