package migrate_connectors

import "github.com/confluentinc/kcp/cmd/create_asset/registry"

func init() { registry.Register(NewMigrateConnectorsCmd) }
