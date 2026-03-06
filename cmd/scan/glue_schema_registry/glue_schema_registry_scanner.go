package glue_schema_registry

import (
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/types"
)

type GlueSchemaRegistryScannerService interface {
	GetRegistryInfo(registryName string) (string, error)
	GetAllSchemasWithVersions(registryName string) ([]types.GlueSchema, error)
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

func (s *GlueSchemaRegistryScanner) Run() error {
	slog.Info("starting Glue Schema Registry scanner", "registry_name", s.RegistryName, "region", s.Region)

	registryArn, err := s.GlueService.GetRegistryInfo(s.RegistryName)
	if err != nil {
		return fmt.Errorf("failed to get registry info: %v", err)
	}

	slog.Info("found Glue Schema Registry", "registry_arn", registryArn)

	schemas, err := s.GlueService.GetAllSchemasWithVersions(s.RegistryName)
	if err != nil {
		return fmt.Errorf("failed to get schemas: %v", err)
	}

	slog.Info("discovered schemas", "count", len(schemas))

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
