package migration_infra

import "github.com/confluentinc/kcp/cmd/create_asset/registry"

func init() { registry.Register(NewMigrationInfraCmd) }
