package brokers

import (
	"context"
	"fmt"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

type KafkaAdminFactory func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error)

type BrokerScannerOpts struct {
	BootstrapServer   string
	AuthType          types.AuthType
	SASLScramUsername string
	SASLScramPassword string
	TLSCACert         string
	TLSClientCert     string
	TLSClientKey      string
}

type BrokerScanner struct {
	kafkaAdminFactory KafkaAdminFactory
	region            string
}

func NewBrokerScanner(kafkaAdminFactory KafkaAdminFactory, opts BrokerScannerOpts) *BrokerScanner {
	return &BrokerScanner{
		kafkaAdminFactory: kafkaAdminFactory,
	}
}

func (bs *BrokerScanner) Run() error {
	ctx := context.TODO()

	brokerInfo, err := bs.ScanBrokers(ctx)
	if err != nil {
		return err
	}

	fmt.Println("brokerInfo", brokerInfo)

	return nil
}

func (bs *BrokerScanner) ScanBrokers(ctx context.Context) (*types.ClusterInformation, error) {
	return nil, nil
}
