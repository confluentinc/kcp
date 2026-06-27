//go:build integration

package migrate

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"

	"github.com/IBM/sarama"
	"github.com/aws/aws-msk-iam-sasl-signer-go/signer"
	"github.com/stretchr/testify/require"
)

// mskIAMTokenProvider signs MSK IAM SASL/OAUTHBEARER tokens for the test's own
// sarama admin (the product MSKAccessTokenProvider's region field is unexported).
type mskIAMTokenProvider struct{ region string }

func (p mskIAMTokenProvider) Token() (*sarama.AccessToken, error) {
	tok, _, err := signer.GenerateAuthToken(context.TODO(), p.region)
	return &sarama.AccessToken{Token: tok}, err
}

// newMSKIAMAdmin opens a sarama ClusterAdmin to MSK using IAM (admin principal),
// used to seed topics and manage ACLs.
func newMSKIAMAdmin(t *testing.T, brokers []string, region string) sarama.ClusterAdmin {
	t.Helper()
	cfg := sarama.NewConfig()
	v, err := sarama.ParseKafkaVersion("3.6.0")
	require.NoError(t, err)
	cfg.Version = v
	cfg.Net.TLS.Enable = true
	cfg.Net.TLS.Config = &tls.Config{} // MSK uses public Amazon certs (system roots)
	cfg.Net.SASL.Enable = true
	cfg.Net.SASL.Mechanism = sarama.SASLTypeOAuth
	cfg.Net.SASL.TokenProvider = mskIAMTokenProvider{region: region}
	admin, err := sarama.NewClusterAdmin(brokers, cfg)
	require.NoError(t, err, "open MSK IAM admin")
	return admin
}

// seedMSKCatalog creates topicCatalog() on MSK under a unique prefix (RF=3 — MSK
// has 3 brokers) and returns the prefixed names. Already-exists is tolerated.
func seedMSKCatalog(t *testing.T, admin sarama.ClusterAdmin, prefix string) []string {
	t.Helper()
	names := make([]string, 0, len(topicCatalog()))
	for _, ct := range topicCatalog() {
		name := prefix + ct.name
		err := admin.CreateTopic(name, &sarama.TopicDetail{NumPartitions: int32(ct.partitions), ReplicationFactor: 3}, false)
		if err != nil && !isTopicExists(err) {
			require.NoError(t, err, "create MSK topic %q", name)
		}
		names = append(names, name)
	}
	return names
}

func isTopicExists(err error) bool {
	var kerr *sarama.TopicError
	if errors.As(err, &kerr) {
		return kerr.Err == sarama.ErrTopicAlreadyExists
	}
	return false
}

// grantUserReadDescribe grants the SCRAM user READ+DESCRIBE on each topic (LITERAL)
// so the cluster link (authenticating as that user) can replicate them.
func grantUserReadDescribe(t *testing.T, admin sarama.ClusterAdmin, user string, topics []string) {
	t.Helper()
	principal := "User:" + user
	for _, topic := range topics {
		res := sarama.Resource{ResourceType: sarama.AclResourceTopic, ResourceName: topic, ResourcePatternType: sarama.AclPatternLiteral}
		for _, op := range []sarama.AclOperation{sarama.AclOperationRead, sarama.AclOperationDescribe} {
			acl := sarama.Acl{Principal: principal, Host: "*", Operation: op, PermissionType: sarama.AclPermissionAllow}
			require.NoError(t, admin.CreateACL(res, acl), "grant %v on %q to %s", op, topic, principal)
		}
	}
}

// cleanupMSK deletes the seeded topics and revokes the granted ACLs (best-effort).
func cleanupMSK(t *testing.T, admin sarama.ClusterAdmin, user string, topics []string) {
	t.Helper()
	principal := "User:" + user
	host := "*"
	for _, topic := range topics {
		topic := topic
		filter := sarama.AclFilter{
			ResourceType:              sarama.AclResourceTopic,
			ResourceName:              &topic,
			ResourcePatternTypeFilter: sarama.AclPatternLiteral,
			Principal:                 &principal,
			Host:                      &host,
			Operation:                 sarama.AclOperationAny,
			PermissionType:            sarama.AclPermissionAny,
		}
		if _, err := admin.DeleteACL(filter, false); err != nil {
			t.Logf("cleanup: delete ACLs for %q: %v", topic, err)
		}
		if err := admin.DeleteTopic(topic); err != nil {
			t.Logf("cleanup: delete topic %q: %v", topic, err)
		}
	}
}
