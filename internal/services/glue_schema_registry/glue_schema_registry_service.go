package glue_schema_registry

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
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

func (s *GlueSchemaRegistryService) GetRegistryInfo(ctx context.Context, registryName string) (string, error) {
	slog.Info("fetching Glue Schema Registry info", "registry_name", registryName)

	output, err := s.client.GetRegistry(ctx, &glue.GetRegistryInput{
		RegistryId: &gluetypes.RegistryId{
			RegistryName: &registryName,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get Glue Schema Registry %q: %w", registryName, err)
	}

	return aws.ToString(output.RegistryArn), nil
}

func (s *GlueSchemaRegistryService) GetAllSchemasWithVersions(ctx context.Context, registryName string) ([]types.GlueSchema, error) {
	slog.Info("listing all schemas in Glue Schema Registry", "registry_name", registryName)

	schemaItems, err := s.listAllSchemas(ctx, registryName)
	if err != nil {
		return nil, err
	}

	slog.Info("found schemas", "count", len(schemaItems))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	const maxConcurrency = 10
	sem := make(chan struct{}, maxConcurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error

	schemas := make([]types.GlueSchema, len(schemaItems))

	for i, item := range schemaItems {
		wg.Add(1)
		go func(idx int, item gluetypes.SchemaListItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			mu.Lock()
			if firstErr != nil {
				mu.Unlock()
				return
			}
			mu.Unlock()

			schemaName := aws.ToString(item.SchemaName)
			schemaArn := aws.ToString(item.SchemaArn)

			slog.Debug("fetching versions for schema", "schema_name", schemaName)

			versions, err := s.getSchemaVersions(ctx, registryName, schemaName)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("failed to get versions for schema %q: %w", schemaName, err)
					cancel()
				}
				mu.Unlock()
				return
			}

			var latest *types.GlueSchemaVersion
			if len(versions) > 0 {
				latestIdx := 0
				for i, v := range versions {
					if v.VersionNumber > versions[latestIdx].VersionNumber {
						latestIdx = i
					}
				}
				latest = &versions[latestIdx]
			}

			dataFormat := ""
			if latest != nil {
				dataFormat = latest.DataFormat
			}

			schemas[idx] = types.GlueSchema{
				SchemaName: schemaName,
				SchemaArn:  schemaArn,
				DataFormat: dataFormat,
				Versions:   versions,
				Latest:     latest,
			}
		}(i, item)
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	return schemas, nil
}

func (s *GlueSchemaRegistryService) listAllSchemas(ctx context.Context, registryName string) ([]gluetypes.SchemaListItem, error) {
	var allSchemas []gluetypes.SchemaListItem
	var nextToken *string

	for {
		output, err := s.client.ListSchemas(ctx, &glue.ListSchemasInput{
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

func (s *GlueSchemaRegistryService) getSchemaVersions(ctx context.Context, registryName, schemaName string) ([]types.GlueSchemaVersion, error) {
	versionItems, err := s.listAllSchemaVersions(ctx, registryName, schemaName)
	if err != nil {
		return nil, err
	}

	var versions []types.GlueSchemaVersion
	for _, item := range versionItems {
		versionNumber := aws.ToInt64(item.VersionNumber)

		versionOutput, err := s.client.GetSchemaVersion(ctx, &glue.GetSchemaVersionInput{
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

		createdDate := time.Time{}
		if versionOutput.CreatedTime != nil {
			if parsed, parseErr := time.Parse(time.RFC3339, *versionOutput.CreatedTime); parseErr == nil {
				createdDate = parsed
			} else {
				slog.Warn("failed to parse created time", "schema", schemaName, "version", versionNumber, "error", parseErr)
			}
		}

		versions = append(versions, types.GlueSchemaVersion{
			SchemaDefinition: aws.ToString(versionOutput.SchemaDefinition),
			DataFormat:       string(versionOutput.DataFormat),
			VersionNumber:    versionNumber,
			Status:           string(versionOutput.Status),
			CreatedDate:      createdDate,
		})
	}

	return versions, nil
}

func (s *GlueSchemaRegistryService) listAllSchemaVersions(ctx context.Context, registryName, schemaName string) ([]gluetypes.SchemaVersionListItem, error) {
	var allVersions []gluetypes.SchemaVersionListItem
	var nextToken *string

	for {
		output, err := s.client.ListSchemaVersions(ctx, &glue.ListSchemaVersionsInput{
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
