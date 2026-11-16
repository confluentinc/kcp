//go:build integration

package mskemulator

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/aws/aws-sdk-go-v2/service/kafkaconnect"
	kctypes "github.com/aws/aws-sdk-go-v2/service/kafkaconnect/types"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

const (
	pollInterval = 2 * time.Second
	pollTimeout  = 120 * time.Second
)

type seededSubnet struct {
	ID, AZ, CIDR string
}

type seededNetwork struct {
	VpcID, SecurityGroupID string
	Subnets                []seededSubnet
}

func (n seededNetwork) subnetIDs() []string {
	ids := make([]string, len(n.Subnets))
	for i, s := range n.Subnets {
		ids[i] = s.ID
	}
	return ids
}

type seededCluster struct {
	Name, Region, Arn, KafkaVersion, InstanceType string
	Brokers, VolumeGB                             int32
	Serverless, IAM, SCRAM, TLSInTransit          bool
	Network                                       seededNetwork
	ScramSecretArn                                string
}

type seededConnector struct {
	Name, Arn, PluginArn, Bootstrap string
	MCU, Workers                    int32
	Config                          map[string]string
}

type fixture struct {
	IAMScram, Serverless, ScramOnly seededCluster
	ConfigurationArn                string
	Connector                       seededConnector
}

var fx fixture

func seedNetwork(ctx context.Context, region, cidrPrefix string, subnets int) (seededNetwork, error) {
	cfg, err := sdkConfig(ctx, region)
	if err != nil {
		return seededNetwork{}, err
	}
	ec := ec2.NewFromConfig(cfg)

	vpc, err := ec.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String(cidrPrefix + ".0.0/16")})
	if err != nil {
		return seededNetwork{}, fmt.Errorf("create VPC in %s: %w", region, err)
	}
	net := seededNetwork{VpcID: aws.ToString(vpc.Vpc.VpcId)}

	for i := range subnets {
		az := fmt.Sprintf("%s%c", region, 'a'+i)
		cidr := fmt.Sprintf("%s.%d.0/24", cidrPrefix, i)
		sn, err := ec.CreateSubnet(ctx, &ec2.CreateSubnetInput{
			VpcId:            vpc.Vpc.VpcId,
			CidrBlock:        aws.String(cidr),
			AvailabilityZone: aws.String(az),
		})
		if err != nil {
			return seededNetwork{}, fmt.Errorf("create subnet %s in %s: %w", cidr, az, err)
		}
		net.Subnets = append(net.Subnets, seededSubnet{ID: aws.ToString(sn.Subnet.SubnetId), AZ: az, CIDR: cidr})
	}

	sg, err := ec.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("kcp-it"),
		Description: aws.String("kcp integration tests"),
		VpcId:       vpc.Vpc.VpcId,
	})
	if err != nil {
		return seededNetwork{}, fmt.Errorf("create security group in %s: %w", region, err)
	}
	net.SecurityGroupID = aws.ToString(sg.GroupId)
	return net, nil
}

func kafkaClient(ctx context.Context, region string) (*kafka.Client, error) {
	cfg, err := sdkConfig(ctx, region)
	if err != nil {
		return nil, err
	}
	return kafka.NewFromConfig(cfg), nil
}

func clientAuth(c seededCluster) *kafkatypes.ClientAuthentication {
	sasl := &kafkatypes.Sasl{}
	if c.IAM {
		sasl.Iam = &kafkatypes.Iam{Enabled: aws.Bool(true)}
	}
	if c.SCRAM {
		sasl.Scram = &kafkatypes.Scram{Enabled: aws.Bool(true)}
	}
	return &kafkatypes.ClientAuthentication{Sasl: sasl}
}

func seedProvisioned(ctx context.Context, c seededCluster, configArn string) (seededCluster, error) {
	k, err := kafkaClient(ctx, c.Region)
	if err != nil {
		return c, err
	}
	req := &kafkatypes.ProvisionedRequest{
		BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{
			ClientSubnets:  c.Network.subnetIDs(),
			InstanceType:   aws.String(c.InstanceType),
			SecurityGroups: []string{c.Network.SecurityGroupID},
			StorageInfo: &kafkatypes.StorageInfo{
				EbsStorageInfo: &kafkatypes.EBSStorageInfo{VolumeSize: aws.Int32(c.VolumeGB)},
			},
		},
		KafkaVersion:         aws.String(c.KafkaVersion),
		NumberOfBrokerNodes:  aws.Int32(c.Brokers),
		ClientAuthentication: clientAuth(c),
	}
	if c.TLSInTransit {
		req.EncryptionInfo = &kafkatypes.EncryptionInfo{
			EncryptionInTransit: &kafkatypes.EncryptionInTransit{
				ClientBroker: kafkatypes.ClientBrokerTls,
				InCluster:    aws.Bool(true),
			},
		}
	}
	if configArn != "" {
		req.ConfigurationInfo = &kafkatypes.ConfigurationInfo{Arn: aws.String(configArn), Revision: aws.Int64(1)}
	}

	out, err := k.CreateClusterV2(ctx, &kafka.CreateClusterV2Input{ClusterName: aws.String(c.Name), Provisioned: req})
	if err != nil {
		return c, fmt.Errorf("create cluster %s: %w", c.Name, err)
	}
	c.Arn = aws.ToString(out.ClusterArn)
	return c, waitClusterState(ctx, c.Region, c.Arn, "ACTIVE")
}

func seedServerless(ctx context.Context, c seededCluster) (seededCluster, error) {
	k, err := kafkaClient(ctx, c.Region)
	if err != nil {
		return c, err
	}
	out, err := k.CreateClusterV2(ctx, &kafka.CreateClusterV2Input{
		ClusterName: aws.String(c.Name),
		Serverless: &kafkatypes.ServerlessRequest{
			VpcConfigs: []kafkatypes.VpcConfig{{
				SubnetIds:        c.Network.subnetIDs(),
				SecurityGroupIds: []string{c.Network.SecurityGroupID},
			}},
			ClientAuthentication: &kafkatypes.ServerlessClientAuthentication{
				Sasl: &kafkatypes.ServerlessSasl{Iam: &kafkatypes.Iam{Enabled: aws.Bool(true)}},
			},
		},
	})
	if err != nil {
		return c, fmt.Errorf("create serverless cluster %s: %w", c.Name, err)
	}
	c.Arn = aws.ToString(out.ClusterArn)
	return c, waitClusterState(ctx, c.Region, c.Arn, "ACTIVE")
}

func poll(ctx context.Context, what string, done func() (bool, string, error)) error {
	deadline := time.Now().Add(pollTimeout)
	for {
		ok, last, err := done()
		if err != nil {
			return fmt.Errorf("waiting for %s: %w", what, err)
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for %s (last: %s)", pollTimeout, what, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func waitClusterState(ctx context.Context, region, arn, want string) error {
	k, err := kafkaClient(ctx, region)
	if err != nil {
		return err
	}
	return poll(ctx, fmt.Sprintf("cluster %s to be %s", arn, want), func() (bool, string, error) {
		out, err := k.DescribeClusterV2(ctx, &kafka.DescribeClusterV2Input{ClusterArn: aws.String(arn)})
		if err != nil {
			return false, "", err
		}
		state := string(out.ClusterInfo.State)
		if state == string(kafkatypes.ClusterStateFailed) && want != state {
			return false, state, errors.New("cluster entered FAILED")
		}
		return state == want, state, nil
	})
}

func seedScramSecret(ctx context.Context, region, clusterArn, name string) (string, error) {
	cfg, err := sdkConfig(ctx, region)
	if err != nil {
		return "", err
	}
	// MSK rejects SCRAM secrets encrypted with the default Secrets Manager key.
	key, err := kms.NewFromConfig(cfg).CreateKey(ctx, &kms.CreateKeyInput{Description: aws.String("kcp-it SCRAM secret")})
	if err != nil {
		return "", fmt.Errorf("create KMS key: %w", err)
	}
	sec, err := secretsmanager.NewFromConfig(cfg).CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String("AmazonMSK_" + name),
		KmsKeyId:     key.KeyMetadata.KeyId,
		SecretString: aws.String(`{"username":"kcp-it","password":"kcp-it-password"}`),
	})
	if err != nil {
		return "", fmt.Errorf("create secret AmazonMSK_%s: %w", name, err)
	}
	out, err := kafka.NewFromConfig(cfg).BatchAssociateScramSecret(ctx, &kafka.BatchAssociateScramSecretInput{
		ClusterArn:    aws.String(clusterArn),
		SecretArnList: []string{aws.ToString(sec.ARN)},
	})
	if err != nil {
		return "", fmt.Errorf("associate SCRAM secret: %w", err)
	}
	if len(out.UnprocessedScramSecrets) > 0 {
		u := out.UnprocessedScramSecrets[0]
		return "", fmt.Errorf("SCRAM secret not associated: %s: %s", aws.ToString(u.ErrorCode), aws.ToString(u.ErrorMessage))
	}
	return aws.ToString(sec.ARN), waitClusterState(ctx, region, clusterArn, "ACTIVE")
}

func pluginZip() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("kcp-it/README")
	if err != nil {
		return nil, err
	}
	if _, err := f.Write([]byte("kcp integration test plugin\n")); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func seedConnector(ctx context.Context, cluster seededCluster) (seededConnector, error) {
	cfg, err := sdkConfig(ctx, cluster.Region)
	if err != nil {
		return seededConnector{}, err
	}
	conn := seededConnector{
		Name:    "kcp-it-connector",
		MCU:     1,
		Workers: 2,
		Config: map[string]string{
			"connector.class":       "io.confluent.connect.s3.S3SinkConnector",
			"tasks.max":             "2",
			"topics":                "orders",
			"s3.bucket.name":        "kcp-it-sink",
			"aws.secret.access.key": "kcp-it-not-a-secret",
		},
	}

	s3c := s3.NewFromConfig(cfg, func(o *s3.Options) { o.UsePathStyle = true })
	if _, err := s3c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("kcp-it-plugins")}); err != nil {
		return conn, fmt.Errorf("create plugin bucket: %w", err)
	}
	body, err := pluginZip()
	if err != nil {
		return conn, err
	}
	if _, err := s3c.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("kcp-it-plugins"),
		Key:    aws.String("s3-sink.zip"),
		Body:   bytes.NewReader(body),
	}); err != nil {
		return conn, fmt.Errorf("upload plugin: %w", err)
	}

	kc := kafkaconnect.NewFromConfig(cfg)
	plugin, err := kc.CreateCustomPlugin(ctx, &kafkaconnect.CreateCustomPluginInput{
		Name:        aws.String("kcp-it-s3-sink"),
		ContentType: kctypes.CustomPluginContentTypeZip,
		Location: &kctypes.CustomPluginLocation{S3Location: &kctypes.S3Location{
			BucketArn: aws.String("arn:aws:s3:::kcp-it-plugins"),
			FileKey:   aws.String("s3-sink.zip"),
		}},
	})
	if err != nil {
		return conn, fmt.Errorf("create custom plugin: %w", err)
	}
	conn.PluginArn = aws.ToString(plugin.CustomPluginArn)
	if err := poll(ctx, "custom plugin to be ACTIVE", func() (bool, string, error) {
		out, err := kc.DescribeCustomPlugin(ctx, &kafkaconnect.DescribeCustomPluginInput{CustomPluginArn: plugin.CustomPluginArn})
		if err != nil {
			return false, "", err
		}
		return out.CustomPluginState == kctypes.CustomPluginStateActive, string(out.CustomPluginState), nil
	}); err != nil {
		return conn, err
	}

	role, err := iam.NewFromConfig(cfg).CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("kcp-it-connect"),
		AssumeRolePolicyDocument: aws.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"kafkaconnect.amazonaws.com"},"Action":"sts:AssumeRole"}]}`),
	})
	if err != nil {
		return conn, fmt.Errorf("create connector role: %w", err)
	}

	k := kafka.NewFromConfig(cfg)
	bs, err := k.GetBootstrapBrokers(ctx, &kafka.GetBootstrapBrokersInput{ClusterArn: aws.String(cluster.Arn)})
	if err != nil {
		return conn, fmt.Errorf("get bootstrap brokers: %w", err)
	}
	conn.Bootstrap = aws.ToString(bs.BootstrapBrokerStringSaslIam)
	if conn.Bootstrap == "" {
		return conn, fmt.Errorf("cluster %s has no IAM bootstrap string", cluster.Name)
	}

	out, err := kc.CreateConnector(ctx, &kafkaconnect.CreateConnectorInput{
		ConnectorName:       aws.String(conn.Name),
		KafkaConnectVersion: aws.String("2.7.1"),
		Capacity: &kctypes.Capacity{ProvisionedCapacity: &kctypes.ProvisionedCapacity{
			McuCount:    conn.MCU,
			WorkerCount: conn.Workers,
		}},
		ConnectorConfiguration: conn.Config,
		KafkaCluster: &kctypes.KafkaCluster{ApacheKafkaCluster: &kctypes.ApacheKafkaCluster{
			BootstrapServers: aws.String(conn.Bootstrap),
			Vpc: &kctypes.Vpc{
				Subnets:        cluster.Network.subnetIDs()[:2],
				SecurityGroups: []string{cluster.Network.SecurityGroupID},
			},
		}},
		KafkaClusterClientAuthentication: &kctypes.KafkaClusterClientAuthentication{
			AuthenticationType: kctypes.KafkaClusterClientAuthenticationTypeIam,
		},
		KafkaClusterEncryptionInTransit: &kctypes.KafkaClusterEncryptionInTransit{
			EncryptionType: kctypes.KafkaClusterEncryptionInTransitTypeTls,
		},
		Plugins: []kctypes.Plugin{{CustomPlugin: &kctypes.CustomPlugin{
			CustomPluginArn: plugin.CustomPluginArn,
			Revision:        1,
		}}},
		ServiceExecutionRoleArn: role.Role.Arn,
	})
	if err != nil {
		return conn, fmt.Errorf("create connector: %w", err)
	}
	conn.Arn = aws.ToString(out.ConnectorArn)
	return conn, poll(ctx, "connector to be RUNNING", func() (bool, string, error) {
		d, err := kc.DescribeConnector(ctx, &kafkaconnect.DescribeConnectorInput{ConnectorArn: out.ConnectorArn})
		if err != nil {
			return false, "", err
		}
		return d.ConnectorState == kctypes.ConnectorStateRunning, string(d.ConnectorState), nil
	})
}

func clusterArns(ctx context.Context, region string) ([]string, error) {
	k, err := kafkaClient(ctx, region)
	if err != nil {
		return nil, err
	}
	var arns []string
	p := kafka.NewListClustersV2Paginator(k, &kafka.ListClustersV2Input{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list clusters in %s: %w", region, err)
		}
		for _, c := range page.ClusterInfoList {
			arns = append(arns, aws.ToString(c.ClusterArn))
		}
	}
	return arns, nil
}

func deleteClusterAndWait(ctx context.Context, region, arn string) error {
	k, err := kafkaClient(ctx, region)
	if err != nil {
		return err
	}
	if _, err := k.DeleteCluster(ctx, &kafka.DeleteClusterInput{ClusterArn: aws.String(arn)}); err != nil {
		return fmt.Errorf("delete cluster %s: %w", arn, err)
	}
	return poll(ctx, fmt.Sprintf("cluster %s to disappear", arn), func() (bool, string, error) {
		arns, err := clusterArns(ctx, region)
		if err != nil {
			return false, "", err
		}
		for _, a := range arns {
			if a == arn {
				return false, "still listed", nil
			}
		}
		return true, "", nil
	})
}

func seedFixture(ctx context.Context) (fixture, error) {
	for _, region := range []string{"us-east-1", "eu-west-3", "ap-northeast-1", "ap-south-1"} {
		arns, err := clusterArns(ctx, region)
		if err != nil {
			return fixture{}, err
		}
		if len(arns) > 0 {
			return fixture{}, errors.New("emulator already has MSK clusters; re-run integration-tests/msk-emulator/setup.sh for a fresh emulator")
		}
	}

	var f fixture
	iamNet, err := seedNetwork(ctx, "us-east-1", "10.10", 3)
	if err != nil {
		return f, err
	}
	serverlessNet, err := seedNetwork(ctx, "us-east-1", "10.11", 2)
	if err != nil {
		return f, err
	}
	scramNet, err := seedNetwork(ctx, "eu-west-3", "10.20", 3)
	if err != nil {
		return f, err
	}

	k, err := kafkaClient(ctx, "us-east-1")
	if err != nil {
		return f, err
	}
	conf, err := k.CreateConfiguration(ctx, &kafka.CreateConfigurationInput{
		Name:             aws.String("kcp-it-config"),
		KafkaVersions:    []string{"3.6.0"},
		ServerProperties: []byte("auto.create.topics.enable=false\n"),
	})
	if err != nil {
		return f, fmt.Errorf("create configuration: %w", err)
	}
	f.ConfigurationArn = aws.ToString(conf.Arn)

	f.IAMScram, err = seedProvisioned(ctx, seededCluster{
		Name: "kcp-it-iam-scram", Region: "us-east-1", KafkaVersion: "3.6.0",
		InstanceType: "kafka.m5.large", Brokers: 3, VolumeGB: 100,
		IAM: true, SCRAM: true, TLSInTransit: true, Network: iamNet,
	}, f.ConfigurationArn)
	if err != nil {
		return f, err
	}
	desc, err := k.DescribeClusterV2(ctx, &kafka.DescribeClusterV2Input{ClusterArn: aws.String(f.IAMScram.Arn)})
	if err != nil {
		return f, fmt.Errorf("describe %s: %w", f.IAMScram.Name, err)
	}
	if _, err := k.UpdateBrokerStorage(ctx, &kafka.UpdateBrokerStorageInput{
		ClusterArn:     aws.String(f.IAMScram.Arn),
		CurrentVersion: desc.ClusterInfo.CurrentVersion,
		TargetBrokerEBSVolumeInfo: []kafkatypes.BrokerEBSVolumeInfo{{
			KafkaBrokerNodeId: aws.String("ALL"),
			VolumeSizeGB:      aws.Int32(200),
		}},
	}); err != nil {
		return f, fmt.Errorf("update broker storage: %w", err)
	}
	if err := waitClusterState(ctx, "us-east-1", f.IAMScram.Arn, "ACTIVE"); err != nil {
		return f, err
	}
	f.IAMScram.VolumeGB = 200

	f.IAMScram.ScramSecretArn, err = seedScramSecret(ctx, "us-east-1", f.IAMScram.Arn, "kcp-it")
	if err != nil {
		return f, err
	}

	f.Serverless, err = seedServerless(ctx, seededCluster{
		Name: "kcp-it-serverless", Region: "us-east-1", Serverless: true, IAM: true, Network: serverlessNet,
	})
	if err != nil {
		return f, err
	}

	f.ScramOnly, err = seedProvisioned(ctx, seededCluster{
		Name: "kcp-it-scram-only", Region: "eu-west-3", KafkaVersion: "3.5.1",
		InstanceType: "kafka.m5.xlarge", Brokers: 3, VolumeGB: 500, SCRAM: true, Network: scramNet,
	}, "")
	if err != nil {
		return f, err
	}

	f.Connector, err = seedConnector(ctx, f.IAMScram)
	return f, err
}
