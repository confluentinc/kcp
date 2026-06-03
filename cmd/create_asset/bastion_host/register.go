package bastion_host

import "github.com/confluentinc/kcp/cmd/create_asset/registry"

func init() { registry.Register(NewBastionHostCmd) }
