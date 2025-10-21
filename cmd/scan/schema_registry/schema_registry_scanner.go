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

	subjects, err := srs.SchemaRegistryService.GetAllSubjectsWithVersions()
	if err != nil {
		return fmt.Errorf("failed to export all subjects: %v", err)
	}

	schemaRegistryInformation := types.SchemaRegistryInformation{
		// assume only confluent schema registry for now
		Type:                 "confluent",
		URL:                  srs.Url,
		DefaultCompatibility: defaultCompatibility,
		Subjects:             subjects,
	}

	previouslyScanned := false
	for i, existing := range srs.State.SchemaRegistries {
		if existing.URL == schemaRegistryInformation.URL {
			srs.State.SchemaRegistries[i] = schemaRegistryInformation
			previouslyScanned = true
		}
	}

	if !previouslyScanned {
		srs.State.SchemaRegistries = append(srs.State.SchemaRegistries, schemaRegistryInformation)
	}

	if err := srs.State.PersistStateFile(srs.StateFile); err != nil {
		return fmt.Errorf("failed to save schema registry state: %v", err)
	}

	return nil
}
