package schema_registry

import (
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/types"
)

type SchemaRegistryScannerService interface {
	ExportAllSubjects() ([]types.Subject, error)
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
	subjects, err := srs.SchemaRegistryService.ExportAllSubjects()
	if err != nil {
		return fmt.Errorf("failed to export all subjects: %v", err)
	}

	schemaRegistryInformation := types.SchemaRegistryInformation{
		Type:     "confluent",
		URL:      srs.Url,
		Subjects: subjects,
	}

	srs.State.SchemaRegistries = append(srs.State.SchemaRegistries, schemaRegistryInformation)

	if err := srs.State.PersistStateFile(srs.StateFile); err != nil {
		return fmt.Errorf("failed to save schema registry state: %v", err)
	}

	return nil
}
