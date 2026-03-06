package schema_registry

import (
	"fmt"
	"log/slog"

	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
	"github.com/confluentinc/kcp/internal/types"
)

type SchemaRegistryScannerService interface {
	GetDefaultCompatibility() (schemaregistry.Compatibility, error)
	GetAllSubjectsWithVersions() ([]types.Subject, error)
	GetAllContexts() ([]string, error)
}

type SchemaRegistryScannerOpts struct {
	StateFile string
	State     types.State
	Url       string
}

type SchemaRegistryScanner struct {
	SchemaRegistryService SchemaRegistryScannerService

	StateFile string
	State     types.State
	Url       string
}

func NewSchemaRegistryScanner(schemaRegistryService SchemaRegistryScannerService, opts SchemaRegistryScannerOpts) *SchemaRegistryScanner {
	return &SchemaRegistryScanner{
		SchemaRegistryService: schemaRegistryService,

		StateFile: opts.StateFile,
		State:     opts.State,
		Url:       opts.Url,
	}
}

func (srs *SchemaRegistryScanner) Run() error {
	slog.Info("🚀 starting schema registry scanner")

	defaultCompatibility, err := srs.SchemaRegistryService.GetDefaultCompatibility()
	if err != nil {
		return fmt.Errorf("failed to get default compatibility: %v", err)
	}

	contexts, err := srs.SchemaRegistryService.GetAllContexts()
	if err != nil {
		return fmt.Errorf("failed to get all contexts: %v", err)
	}

	subjects, err := srs.SchemaRegistryService.GetAllSubjectsWithVersions()
	if err != nil {
		return fmt.Errorf("failed to export all subjects: %v", err)
	}

	schemaRegistryInformation := types.SchemaRegistryInformation{
		// assume only confluent schema registry for now
		Type:                 "confluent",
		URL:                  srs.Url,
		DefaultCompatibility: defaultCompatibility,
		Contexts:             contexts,
		Subjects:             subjects,
	}

	if srs.State.SchemaRegistries == nil {
		srs.State.SchemaRegistries = &types.SchemaRegistriesState{}
	}
	srs.State.SchemaRegistries.UpsertConfluentSchemaRegistry(schemaRegistryInformation)

	if err := srs.State.PersistStateFile(srs.StateFile); err != nil {
		return fmt.Errorf("failed to save schema registry state: %v", err)
	}

	return nil
}
