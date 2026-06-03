package create_asset

// Always-on create-asset subcommands. These blank imports run each package's
// init(), registering its constructor with the registry. Present in every
// edition (prod and gov).
import (
	_ "github.com/confluentinc/kcp/cmd/create_asset/bastion_host"
	_ "github.com/confluentinc/kcp/cmd/create_asset/migrate_acls"
	_ "github.com/confluentinc/kcp/cmd/create_asset/migrate_schemas"
	_ "github.com/confluentinc/kcp/cmd/create_asset/migrate_topics"
	_ "github.com/confluentinc/kcp/cmd/create_asset/reverse_proxy"
)
