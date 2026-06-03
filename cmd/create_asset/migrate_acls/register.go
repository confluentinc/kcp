package migrate_acls

import "github.com/confluentinc/kcp/cmd/create_asset/registry"

func init() { registry.Register(NewMigrateAclsCmd) }
