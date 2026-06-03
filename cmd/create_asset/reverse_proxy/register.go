package reverse_proxy

import "github.com/confluentinc/kcp/cmd/create_asset/registry"

func init() { registry.Register(NewReverseProxyCmd) }
