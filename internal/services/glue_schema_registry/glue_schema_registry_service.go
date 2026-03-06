package glue_schema_registry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/confluentinc/kcp/internal/types"
)

type GlueClient interface {
	GetRegistry(ctx context.Context, params *glue.GetRegistryInput, optFns ...func(*glue.Options)) (*glue.GetRegistryOutput, error)
	ListSchemas(ctx context.Context, params *glue.ListSchemasInput, optFns ...func(*glue.Options)) (*glue.ListSchemasOutput, error)
	ListSchemaVersions(ctx context.Context, params *glue.ListSchemaVersionsInput, optFns ...func(*glue.Options)) (*glue.ListSchemaVersionsOutput, error)
	GetSchemaVersion(ctx context.Context, params *glue.GetSchemaVersionInput, optFns ...func(*glue.Options)) (*glue.GetSchemaVersionOutput, error)
}

type GlueSchemaRegistryService struct {
	client GlueClient
}

func NewGlueSchemaRegistryService(client GlueClient) *GlueSchemaRegistryService {
	return &GlueSchemaRegistryService{client: client}
}

func (s *GlueSchemaRegistryService) GetRegistryInfo(registryName string) (string, error) {
	slog.Info("fetching Glue Schema Registry info", "registry_name", registryName)

	output, err := s.client.GetRegistry(context.TODO(), &glue.GetRegistryInput{
		RegistryId: &gluetypes.RegistryId{
			RegistryName: &registryName,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get Glue Schema Registry %q: %w", registryName, err)
	}

	arn := ""
	if output.RegistryArn != nil {
		arn = *output.RegistryArn
	}

	return arn, nil
}

func (s *GlueSchemaRegistryService) GetAllSchemasWithVersions(registryName string) ([]types.GlueSchema, error) {
	slog.Info("listing all schemas in Glue Schema Registry", "registry_name", registryName)

	schemaItems, err := s.listAllSchemas(registryName)
	if err != nil {
		return nil, err
	}

	slog.Info("found schemas", "count", len(schemaItems))

	var schemas []types.GlueSchema
	for _, item := range schemaItems {
		schemaName := ""
		if item.SchemaName != nil {
			schemaName = *item.SchemaName
		}

		schemaArn := ""
		if item.SchemaArn != nil {
			schemaArn = *item.SchemaArn
		}

		slog.Info("fetching versions for schema", "schema_name", schemaName)

		versions, err := s.getSchemaVersions(registryName, schemaName)
		if err != nil {
			return nil, fmt.Errorf("failed to get versions for schema %q: %w", schemaName, err)
		}

		var latest *types.GlueSchemaVersion
		if len(versions) > 0 {
			// Find the version with the highest version number
			latestIdx := 0
			for i, v := range versions {
				if v.VersionNumber > versions[latestIdx].VersionNumber {
					latestIdx = i
				}
			}
			latest = &versions[latestIdx]
		}

		// Determine data format from the latest version if available
		dataFormat := ""
		if latest != nil {
			dataFormat = latest.DataFormat
		}

		schemas = append(schemas, types.GlueSchema{
			SchemaName: schemaName,
			SchemaArn:  schemaArn,
			DataFormat: dataFormat,
			Versions:   versions,
			Latest:     latest,
		})
	}

	return schemas, nil
}

func (s *GlueSchemaRegistryService) listAllSchemas(registryName string) ([]gluetypes.SchemaListItem, error) {
	var allSchemas []gluetypes.SchemaListItem
	var nextToken *string

	for {
		output, err := s.client.ListSchemas(context.TODO(), &glue.ListSchemasInput{
			RegistryId: &gluetypes.RegistryId{
				RegistryName: &registryName,
			},
			NextToken: nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list schemas in registry %q: %w", registryName, err)
		}

		allSchemas = append(allSchemas, output.Schemas...)

		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return allSchemas, nil
}

func (s *GlueSchemaRegistryService) getSchemaVersions(registryName, schemaName string) ([]types.GlueSchemaVersion, error) {
	versionItems, err := s.listAllSchemaVersions(registryName, schemaName)
	if err != nil {
		return nil, err
	}

	var versions []types.GlueSchemaVersion
	for _, item := range versionItems {
		versionNumber := int64(0)
		if item.VersionNumber != nil {
			versionNumber = *item.VersionNumber
		}

		// Get full schema definition for this version
		versionOutput, err := s.client.GetSchemaVersion(context.TODO(), &glue.GetSchemaVersionInput{
			SchemaId: &gluetypes.SchemaId{
				RegistryName: &registryName,
				SchemaName:   &schemaName,
			},
			SchemaVersionNumber: &gluetypes.SchemaVersionNumber{
				VersionNumber: aws.Int64(versionNumber),
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get schema version %d for schema %q: %w", versionNumber, schemaName, err)
		}

		schemaDefinition := ""
		if versionOutput.SchemaDefinition != nil {
			schemaDefinition = *versionOutput.SchemaDefinition
		}

		createdDate := time.Time{}
		if versionOutput.CreatedTime != nil {
			if parsed, parseErr := time.Parse(time.RFC3339, *versionOutput.CreatedTime); parseErr == nil {
				createdDate = parsed
			}
		}

		status := ""
		if versionOutput.Status != "" {
			status = string(versionOutput.Status)
		}

		versions = append(versions, types.GlueSchemaVersion{
			SchemaDefinition: schemaDefinition,
			DataFormat:       string(versionOutput.DataFormat),
			VersionNumber:    versionNumber,
			Status:           status,
			CreatedDate:      createdDate,
		})
	}

	return versions, nil
}

func (s *GlueSchemaRegistryService) listAllSchemaVersions(registryName, schemaName string) ([]gluetypes.SchemaVersionListItem, error) {
	var allVersions []gluetypes.SchemaVersionListItem
	var nextToken *string

	for {
		output, err := s.client.ListSchemaVersions(context.TODO(), &glue.ListSchemaVersionsInput{
			SchemaId: &gluetypes.SchemaId{
				RegistryName: &registryName,
				SchemaName:   &schemaName,
			},
			NextToken: nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list schema versions for %q: %w", schemaName, err)
		}

		allVersions = append(allVersions, output.Schemas...)

		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return allVersions, nil
}
