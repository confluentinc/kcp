package migrate_schemas

import "github.com/confluentinc/kcp/cmd/create_asset/registry"

func init() { registry.Register(NewMigrateSchemasCmd) }
