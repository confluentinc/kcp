package schema_registry

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockGlueService struct {
	getRegistryInfoFn           func(ctx context.Context, registryName string) (string, error)
	getAllSchemasWithVersionsFn func(ctx context.Context, registryName string) ([]types.GlueSchema, error)
}

func (m *mockGlueService) GetRegistryInfo(ctx context.Context, registryName string) (string, error) {
	return m.getRegistryInfoFn(ctx, registryName)
}

func (m *mockGlueService) GetAllSchemasWithVersions(ctx context.Context, registryName string) ([]types.GlueSchema, error) {
	return m.getAllSchemasWithVersionsFn(ctx, registryName)
}

func TestGlueSchemaRegistryScanner_Run(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	// Write initial state
	_, err = tmpFile.WriteString(`{"regions":[],"schema_registries":null,"kcp_build_info":{"version":"0.0.0-localdev","commit":"unknown","date":"unknown"}}`)
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	service := &mockGlueService{
		getRegistryInfoFn: func(ctx context.Context, registryName string) (string, error) {
			return "arn:aws:glue:us-east-1:123456789:registry/my-registry", nil
		},
		getAllSchemasWithVersionsFn: func(ctx context.Context, registryName string) ([]types.GlueSchema, error) {
			return []types.GlueSchema{
				{
					SchemaName: "UserSchema",
					SchemaArn:  "arn:schema1",
					DataFormat: "AVRO",
					Versions: []types.GlueSchemaVersion{
						{SchemaDefinition: `{"type":"record"}`, DataFormat: "AVRO", VersionNumber: 1, Status: "AVAILABLE"},
					},
					Latest: &types.GlueSchemaVersion{SchemaDefinition: `{"type":"record"}`, DataFormat: "AVRO", VersionNumber: 1, Status: "AVAILABLE"},
				},
			}, nil
		},
	}

	state, err := types.NewStateFromFile(tmpFile.Name())
	require.NoError(t, err)

	scanner := NewGlueSchemaRegistryScanner(service, GlueSchemaRegistryScannerOpts{
		StateFile:    tmpFile.Name(),
		State:        *state,
		Region:       "us-east-1",
		RegistryName: "my-registry",
	})

	err = scanner.Run(context.Background())
	require.NoError(t, err)

	// Verify state was persisted
	updatedState, err := types.NewStateFromFile(tmpFile.Name())
	require.NoError(t, err)
	require.NotNil(t, updatedState.SchemaRegistries)
	require.Len(t, updatedState.SchemaRegistries.AWSGlue, 1)

	glueReg := updatedState.SchemaRegistries.AWSGlue[0]
	assert.Equal(t, "my-registry", glueReg.RegistryName)
	assert.Equal(t, "arn:aws:glue:us-east-1:123456789:registry/my-registry", glueReg.RegistryArn)
	assert.Equal(t, "us-east-1", glueReg.Region)
	require.Len(t, glueReg.Schemas, 1)
	assert.Equal(t, "UserSchema", glueReg.Schemas[0].SchemaName)
}

func TestGlueSchemaRegistryScanner_Run_UpsertExisting(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	// Write state with existing Glue registry
	_, err = tmpFile.WriteString(`{"regions":[],"schema_registries":{"aws_glue":[{"registry_name":"my-registry","region":"us-east-1","registry_arn":"old-arn","schemas":[]}]},"kcp_build_info":{"version":"0.0.0-localdev","commit":"unknown","date":"unknown"}}`)
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	service := &mockGlueService{
		getRegistryInfoFn: func(ctx context.Context, registryName string) (string, error) {
			return "new-arn", nil
		},
		getAllSchemasWithVersionsFn: func(ctx context.Context, registryName string) ([]types.GlueSchema, error) {
			return []types.GlueSchema{{SchemaName: "NewSchema", DataFormat: "JSON"}}, nil
		},
	}

	state, err := types.NewStateFromFile(tmpFile.Name())
	require.NoError(t, err)

	scanner := NewGlueSchemaRegistryScanner(service, GlueSchemaRegistryScannerOpts{
		StateFile:    tmpFile.Name(),
		State:        *state,
		Region:       "us-east-1",
		RegistryName: "my-registry",
	})

	err = scanner.Run(context.Background())
	require.NoError(t, err)

	updatedState, err := types.NewStateFromFile(tmpFile.Name())
	require.NoError(t, err)
	require.Len(t, updatedState.SchemaRegistries.AWSGlue, 1)
	assert.Equal(t, "new-arn", updatedState.SchemaRegistries.AWSGlue[0].RegistryArn)
	require.Len(t, updatedState.SchemaRegistries.AWSGlue[0].Schemas, 1)
	assert.Equal(t, "NewSchema", updatedState.SchemaRegistries.AWSGlue[0].Schemas[0].SchemaName)
}

func TestGlueSchemaRegistryScanner_Run_RegistryNotFound(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	_, err = tmpFile.WriteString(`{"regions":[],"kcp_build_info":{"version":"0.0.0-localdev","commit":"unknown","date":"unknown"}}`)
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	service := &mockGlueService{
		getRegistryInfoFn: func(ctx context.Context, registryName string) (string, error) {
			return "", fmt.Errorf("registry not found")
		},
	}

	state, err := types.NewStateFromFile(tmpFile.Name())
	require.NoError(t, err)

	scanner := NewGlueSchemaRegistryScanner(service, GlueSchemaRegistryScannerOpts{
		StateFile:    tmpFile.Name(),
		State:        *state,
		Region:       "us-east-1",
		RegistryName: "nonexistent",
	})

	err = scanner.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get registry info")
}
