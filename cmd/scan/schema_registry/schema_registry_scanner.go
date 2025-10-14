package schema_registry

import "github.com/confluentinc/kcp/internal/types"

type SchemaRegistryScannerOpts struct {
	State types.State
	Url   string
}

type SchemaRegistryScanner struct {
	State types.State
	Url   string
}

func NewSchemaRegistryScanner(opts SchemaRegistryScannerOpts) *SchemaRegistryScanner {
	return &SchemaRegistryScanner{
		State: opts.State,
		Url:   opts.Url,
	}
}

func (s *SchemaRegistryScanner) Run() error {
	return nil
}
