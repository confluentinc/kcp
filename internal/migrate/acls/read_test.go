package acls

import (
	"testing"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

type fakeAdm struct {
	acls []sarama.ResourceAcls
	err  error
}

func (f fakeAdm) ListAcls() ([]sarama.ResourceAcls, error) {
	return f.acls, f.err
}

func TestReadNativeACLs_Flatten(t *testing.T) {
	adm := fakeAdm{acls: []sarama.ResourceAcls{{
		Resource: sarama.Resource{ResourceType: sarama.AclResourceTopic, ResourceName: "orders", ResourcePatternType: sarama.AclPatternLiteral},
		Acls:     []*sarama.Acl{{Principal: "User:app", Host: "*", Operation: sarama.AclOperationRead, PermissionType: sarama.AclPermissionAllow}},
	}}}
	got, err := ReadNativeACLs(adm)
	require.NoError(t, err)
	require.Equal(t, types.Acls{ResourceType: "Topic", ResourceName: "orders", ResourcePatternType: "Literal", Principal: "User:app", Host: "*", Operation: "Read", PermissionType: "Allow"}, got[0])
}

func TestAllowEveryone_AbsentDefaultsTrue(t *testing.T) {
	require.True(t, AllowEveryoneIfNoACLFound([]byte("auto.create.topics.enable=false\n")))
	require.False(t, AllowEveryoneIfNoACLFound([]byte("allow.everyone.if.no.acl.found=false\n")))
	require.True(t, AllowEveryoneIfNoACLFound([]byte("allow.everyone.if.no.acl.found=true\n")))
}
