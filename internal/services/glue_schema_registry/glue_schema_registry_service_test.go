package glue_schema_registry

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockGlueClient struct {
	getRegistryFn        func(ctx context.Context, params *glue.GetRegistryInput, optFns ...func(*glue.Options)) (*glue.GetRegistryOutput, error)
	listSchemasFn        func(ctx context.Context, params *glue.ListSchemasInput, optFns ...func(*glue.Options)) (*glue.ListSchemasOutput, error)
	listSchemaVersionsFn func(ctx context.Context, params *glue.ListSchemaVersionsInput, optFns ...func(*glue.Options)) (*glue.ListSchemaVersionsOutput, error)
	getSchemaVersionFn   func(ctx context.Context, params *glue.GetSchemaVersionInput, optFns ...func(*glue.Options)) (*glue.GetSchemaVersionOutput, error)
}

func (m *mockGlueClient) GetRegistry(ctx context.Context, params *glue.GetRegistryInput, optFns ...func(*glue.Options)) (*glue.GetRegistryOutput, error) {
	return m.getRegistryFn(ctx, params, optFns...)
}

func (m *mockGlueClient) ListSchemas(ctx context.Context, params *glue.ListSchemasInput, optFns ...func(*glue.Options)) (*glue.ListSchemasOutput, error) {
	return m.listSchemasFn(ctx, params, optFns...)
}

func (m *mockGlueClient) ListSchemaVersions(ctx context.Context, params *glue.ListSchemaVersionsInput, optFns ...func(*glue.Options)) (*glue.ListSchemaVersionsOutput, error) {
	return m.listSchemaVersionsFn(ctx, params, optFns...)
}

func (m *mockGlueClient) GetSchemaVersion(ctx context.Context, params *glue.GetSchemaVersionInput, optFns ...func(*glue.Options)) (*glue.GetSchemaVersionOutput, error) {
	return m.getSchemaVersionFn(ctx, params, optFns...)
}

func TestGetRegistryInfo_Success(t *testing.T) {
	client := &mockGlueClient{
		getRegistryFn: func(ctx context.Context, params *glue.GetRegistryInput, optFns ...func(*glue.Options)) (*glue.GetRegistryOutput, error) {
			return &glue.GetRegistryOutput{
				RegistryArn:  aws.String("arn:aws:glue:us-east-1:123456789:registry/my-registry"),
				RegistryName: aws.String("my-registry"),
			}, nil
		},
	}

	service := NewGlueSchemaRegistryService(client)
	arn, err := service.GetRegistryInfo("my-registry")

	require.NoError(t, err)
	assert.Equal(t, "arn:aws:glue:us-east-1:123456789:registry/my-registry", arn)
}

func TestGetRegistryInfo_Error(t *testing.T) {
	client := &mockGlueClient{
		getRegistryFn: func(ctx context.Context, params *glue.GetRegistryInput, optFns ...func(*glue.Options)) (*glue.GetRegistryOutput, error) {
			return nil, fmt.Errorf("registry not found")
		},
	}

	service := NewGlueSchemaRegistryService(client)
	_, err := service.GetRegistryInfo("nonexistent")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestGetAllSchemasWithVersions_Success(t *testing.T) {
	client := &mockGlueClient{
		listSchemasFn: func(ctx context.Context, params *glue.ListSchemasInput, optFns ...func(*glue.Options)) (*glue.ListSchemasOutput, error) {
			return &glue.ListSchemasOutput{
				Schemas: []gluetypes.SchemaListItem{
					{SchemaName: aws.String("UserSchema"), SchemaArn: aws.String("arn:schema1")},
				},
			}, nil
		},
		listSchemaVersionsFn: func(ctx context.Context, params *glue.ListSchemaVersionsInput, optFns ...func(*glue.Options)) (*glue.ListSchemaVersionsOutput, error) {
			return &glue.ListSchemaVersionsOutput{
				Schemas: []gluetypes.SchemaVersionListItem{
					{VersionNumber: aws.Int64(1), SchemaVersionId: aws.String("v1-id")},
					{VersionNumber: aws.Int64(2), SchemaVersionId: aws.String("v2-id")},
				},
			}, nil
		},
		getSchemaVersionFn: func(ctx context.Context, params *glue.GetSchemaVersionInput, optFns ...func(*glue.Options)) (*glue.GetSchemaVersionOutput, error) {
			vn := *params.SchemaVersionNumber.VersionNumber
			return &glue.GetSchemaVersionOutput{
				SchemaDefinition: aws.String(fmt.Sprintf(`{"type":"record","name":"User","version":%d}`, vn)),
				DataFormat:       gluetypes.DataFormatAvro,
				VersionNumber:    aws.Int64(vn),
				Status:           gluetypes.SchemaVersionStatusAvailable,
				CreatedTime:      aws.String("2025-01-15T10:00:00Z"),
			}, nil
		},
	}

	service := NewGlueSchemaRegistryService(client)
	schemas, err := service.GetAllSchemasWithVersions("my-registry")

	require.NoError(t, err)
	require.Len(t, schemas, 1)

	schema := schemas[0]
	assert.Equal(t, "UserSchema", schema.SchemaName)
	assert.Equal(t, "arn:schema1", schema.SchemaArn)
	assert.Equal(t, "AVRO", schema.DataFormat)
	require.Len(t, schema.Versions, 2)
	assert.Equal(t, int64(1), schema.Versions[0].VersionNumber)
	assert.Equal(t, int64(2), schema.Versions[1].VersionNumber)
	assert.Equal(t, "AVAILABLE", schema.Versions[0].Status)

	require.NotNil(t, schema.Latest)
	assert.Equal(t, int64(2), schema.Latest.VersionNumber)
}

func TestGetAllSchemasWithVersions_Pagination(t *testing.T) {
	listSchemasCallCount := 0
	client := &mockGlueClient{
		listSchemasFn: func(ctx context.Context, params *glue.ListSchemasInput, optFns ...func(*glue.Options)) (*glue.ListSchemasOutput, error) {
			listSchemasCallCount++
			if listSchemasCallCount == 1 {
				return &glue.ListSchemasOutput{
					Schemas:   []gluetypes.SchemaListItem{{SchemaName: aws.String("Schema1"), SchemaArn: aws.String("arn:1")}},
					NextToken: aws.String("page2"),
				}, nil
			}
			return &glue.ListSchemasOutput{
				Schemas: []gluetypes.SchemaListItem{{SchemaName: aws.String("Schema2"), SchemaArn: aws.String("arn:2")}},
			}, nil
		},
		listSchemaVersionsFn: func(ctx context.Context, params *glue.ListSchemaVersionsInput, optFns ...func(*glue.Options)) (*glue.ListSchemaVersionsOutput, error) {
			return &glue.ListSchemaVersionsOutput{
				Schemas: []gluetypes.SchemaVersionListItem{
					{VersionNumber: aws.Int64(1)},
				},
			}, nil
		},
		getSchemaVersionFn: func(ctx context.Context, params *glue.GetSchemaVersionInput, optFns ...func(*glue.Options)) (*glue.GetSchemaVersionOutput, error) {
			return &glue.GetSchemaVersionOutput{
				SchemaDefinition: aws.String(`{}`),
				DataFormat:       gluetypes.DataFormatJson,
				VersionNumber:    aws.Int64(1),
				Status:           gluetypes.SchemaVersionStatusAvailable,
			}, nil
		},
	}

	service := NewGlueSchemaRegistryService(client)
	schemas, err := service.GetAllSchemasWithVersions("my-registry")

	require.NoError(t, err)
	assert.Len(t, schemas, 2)
	assert.Equal(t, "Schema1", schemas[0].SchemaName)
	assert.Equal(t, "Schema2", schemas[1].SchemaName)
	assert.Equal(t, 2, listSchemasCallCount)
}

func TestGetAllSchemasWithVersions_EmptyRegistry(t *testing.T) {
	client := &mockGlueClient{
		listSchemasFn: func(ctx context.Context, params *glue.ListSchemasInput, optFns ...func(*glue.Options)) (*glue.ListSchemasOutput, error) {
			return &glue.ListSchemasOutput{
				Schemas: []gluetypes.SchemaListItem{},
			}, nil
		},
	}

	service := NewGlueSchemaRegistryService(client)
	schemas, err := service.GetAllSchemasWithVersions("empty-registry")

	require.NoError(t, err)
	assert.Empty(t, schemas)
}
