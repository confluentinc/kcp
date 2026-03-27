package schema_registry

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
)

type GlueSchemaRegistryScannerService interface {
	GetRegistryInfo(ctx context.Context, registryName string) (string, error)
	GetAllSchemasWithVersions(ctx context.Context, registryName string) ([]types.GlueSchema, error)
}

type GlueSchemaRegistryScannerOpts struct {
	StateFile    string
	State        types.State
	Region       string
	RegistryName string
}

type GlueSchemaRegistryScanner struct {
	GlueService GlueSchemaRegistryScannerService

	StateFile    string
	State        types.State
	Region       string
	RegistryName string
}

func NewGlueSchemaRegistryScanner(glueService GlueSchemaRegistryScannerService, opts GlueSchemaRegistryScannerOpts) *GlueSchemaRegistryScanner {
	return &GlueSchemaRegistryScanner{
		GlueService:  glueService,
		StateFile:    opts.StateFile,
		State:        opts.State,
		Region:       opts.Region,
		RegistryName: opts.RegistryName,
	}
}

func (s *GlueSchemaRegistryScanner) Run(ctx context.Context) error {
	fmt.Printf("🚀 Starting Glue Schema Registry scanner\n")

	registryArn, err := s.GlueService.GetRegistryInfo(ctx, s.RegistryName)
	if err != nil {
		return fmt.Errorf("failed to get registry info: %v", err)
	}

	schemas, err := s.GlueService.GetAllSchemasWithVersions(ctx, s.RegistryName)
	if err != nil {
		return fmt.Errorf("failed to get schemas: %v", err)
	}

	glueRegistryInfo := types.GlueSchemaRegistryInformation{
		RegistryName: s.RegistryName,
		RegistryArn:  registryArn,
		Region:       s.Region,
		Schemas:      schemas,
	}

	if s.State.SchemaRegistries == nil {
		s.State.SchemaRegistries = &types.SchemaRegistriesState{}
	}
	s.State.SchemaRegistries.UpsertGlueSchemaRegistry(glueRegistryInfo)

	if err := s.State.PersistStateFile(s.StateFile); err != nil {
		return fmt.Errorf("failed to save state file: %v", err)
	}

	return nil
}
