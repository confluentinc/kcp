package schema_registry

import "github.com/confluentinc/kcp/internal/types"

type SchemaRegistryScannerOpts struct {
	State types.State
}

type SchemaRegistryScanner struct {
	State types.State
}

func NewSchemaRegistryScanner(opts SchemaRegistryScannerOpts) *SchemaRegistryScanner {
	return &SchemaRegistryScanner{
		State: opts.State,
	}
}

func (s *SchemaRegistryScanner) Run() error {
	return nil
}
