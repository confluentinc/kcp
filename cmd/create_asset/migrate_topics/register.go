package migrate_topics

import "github.com/confluentinc/kcp/cmd/create_asset/registry"

func init() { registry.Register(NewMigrateTopicsCmd) }
