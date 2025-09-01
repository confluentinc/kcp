package brokers

import (
	"context"
	"fmt"
	"log/slog"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

type KafkaAdminFactory func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error)

type BrokerScannerOpts struct {
	AuthType          types.AuthType
	ClusterName       string
	BootstrapServer   string
	SASLScramUsername string
	SASLScramPassword string
	TLSCACert         string
	TLSClientCert     string
	TLSClientKey      string
}

type BrokerScanner struct {
	kafkaAdminFactory KafkaAdminFactory
	opts              *BrokerScannerOpts
}

func NewBrokerScanner(kafkaAdminFactory KafkaAdminFactory, opts *BrokerScannerOpts) *BrokerScanner {
	return &BrokerScanner{
		kafkaAdminFactory: kafkaAdminFactory,
		opts:              opts,
	}
}

func (bs *BrokerScanner) Run() error {
	// ctx := context.TODO()

	// brokerInfo, err := bs.ScanBrokers(ctx)
	// if err != nil {
		// return err
	// }

	// fmt.Println("brokerInfo", brokerInfo)
	// fmt.Println("bs.opts", bs.opts)

	if bs.opts.BootstrapServer == "" {
		return fmt.Errorf("no bootstrap server found, skipping the broker scan")
	}

	slog.Info(fmt.Sprintf("🚀 starting broker scan for %s using %s authentication", bs.opts.ClusterName, bs.opts.AuthType))

	return nil
}

func (bs *BrokerScanner) ScanBrokers(ctx context.Context) (*types.ClusterInformation, error) {
	return nil, nil
}
