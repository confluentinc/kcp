package utils

import "fmt"

type ConnectorMapping struct {
	PluginName    string
	ConnectorType string
}

func InferPluginName(connectorClass string) (string, error) {
	if mapping, exists := ConnectorMap[connectorClass]; exists {
		return mapping.PluginName, nil
	}
	return "", fmt.Errorf("unknown or unsupported connector class: %s", connectorClass)
}

// https://github.com/confluentinc/connect-migration-utility/tree/master/templates/fm
var ConnectorMap = map[string]ConnectorMapping{
	"io.confluent.connect.activemq.ActiveMQSourceConnector": {
		PluginName:    "ActiveMQSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.jdbc.JdbcSinkConnector": {
		PluginName:    "AlloyDbSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.azure.blob.AzureBlobStorageSinkConnector": {
		PluginName:    "AzureBlobSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.azure.blob.storage.AzureBlobStorageSourceConnector": {
		PluginName:    "AzureBlobSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.azure.search.AzureSearchSinkConnector": {
		PluginName:    "AzureCognitiveSearchSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.azure.datalake.gen2.AzureDataLakeGen2SinkConnector": {
		PluginName:    "AzureDataLakeGen2Sink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.azure.eventhubs.EventHubsSourceConnector": {
		PluginName:    "AzureEventHubsSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.azure.functions.AzureFunctionsSinkConnector": {
		PluginName:    "AzureFunctionsSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.azureloganalytics.AzureLogAnalyticsSinkConnector": {
		PluginName:    "AzureLogAnalyticsSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.azure.servicebus.ServiceBusSourceConnector": {
		PluginName:    "AzureServiceBusSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.azuresqldw.AzureSqlDwSinkConnector": {
		PluginName:    "AzureSqlDwSink",
		ConnectorType: "Sink",
	},
	"com.wepay.kafka.connect.bigquery.BigQuerySinkConnector": {
		PluginName:    "BigQuerySink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.bigquerystorage.BigQueryStorageSinkConnector": {
		PluginName:    "BigQueryStorageSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.gcp.bigtable.BigtableSinkConnector": {
		PluginName:    "BigTableSink",
		ConnectorType: "Sink",
	},
	"com.clickhouse.kafka.connect.ClickHouseSinkConnector": {
		PluginName:    "ClickHouseSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.aws.cloudwatch.AwsCloudWatchSourceConnector": {
		PluginName:    "CloudWatchLogsSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.aws.cloudwatch.metrics.AwsCloudWatchMetricsSinkConnector": {
		PluginName:    "CloudWatchMetricsSink",
		ConnectorType: "Sink",
	},
	"com.azure.cosmos.kafka.connect.sink.CosmosDBSinkConnector": {
		PluginName:    "CosmosDbSink",
		ConnectorType: "Sink",
	},
	"com.azure.cosmos.kafka.connect.CosmosSinkConnector": {
		PluginName:    "CosmosDbSinkV2",
		ConnectorType: "Sink",
	},
	"com.azure.cosmos.kafka.connect.source.CosmosDBSourceConnector": {
		PluginName:    "CosmosDbSource",
		ConnectorType: "Source",
	},
	"com.azure.cosmos.kafka.connect.CosmosSourceConnector": {
		PluginName:    "CosmosDbSourceV2",
		ConnectorType: "Source",
	},
	"com.couchbase.connect.kafka.CouchbaseSinkConnector": {
		PluginName:    "CouchbaseSink",
		ConnectorType: "Sink",
	},
	"com.couchbase.connect.kafka.CouchbaseSourceConnector": {
		PluginName:    "CouchbaseSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.databricks.deltalake.DatabricksDeltaLakeSinkConnector": {
		PluginName:    "DatabricksDeltaLakeSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.datadog.metrics.DatadogMetricsSinkConnector": {
		PluginName:    "DatadogMetricsSink",
		ConnectorType: "Sink",
	},
	"io.confluent.kafka.connect.datagen.DatagenConnector": {
		PluginName:    "DatagenSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.gcp.dataproc.DataprocSinkConnector": {
		PluginName:    "DataprocSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.dynamodb.DynamoDBSourceConnector": {
		PluginName:    "DynamoDbCdcSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.aws.dynamodb.DynamoDbSinkConnector": {
		PluginName:    "DynamoDbSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.elasticsearch.ElasticsearchSinkConnector": {
		PluginName:    "ElasticsearchSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.gcs.GcsSinkConnector": {
		PluginName:    "GcsSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.gcs.GcsSourceConnector": {
		PluginName:    "GcsSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.github.GithubSourceConnector": {
		PluginName:    "GithubSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.gcp.functions.GoogleCloudFunctionsSinkConnector": {
		PluginName:    "GoogleCloudFunctionsSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.http.HttpSinkConnector": {
		PluginName:    "HttpSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.http.sink.GenericHttpSinkConnector": {
		PluginName:    "HttpSinkV2",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.http.HttpSourceConnector": {
		PluginName:    "HttpSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.http.source.GenericHttpSourceConnector": {
		PluginName:    "HttpSourceV2",
		ConnectorType: "Source",
	},
	"io.confluent.connect.jdbc.JdbcSourceConnector": {
		PluginName:    "IbmDb2Source",
		ConnectorType: "Source",
	},
	"io.confluent.connect.ibm.mq.IbmMQSourceConnector": {
		PluginName:    "IbmMQSource",
		ConnectorType: "Source",
	},
	"io.confluent.influxdb.v2.sink.InfluxDB2SinkConnector": {
		PluginName:    "InfluxDB2Sink",
		ConnectorType: "Sink",
	},
	"io.confluent.influxdb.v2.source.InfluxDB2SourceConnector": {
		PluginName:    "InfluxDB2Source",
		ConnectorType: "Source",
	},
	"io.confluent.connect.jms.JmsSourceConnector": {
		PluginName:    "JMSSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.jira.JiraSourceConnector": {
		PluginName:    "JiraSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.kinesis.KinesisSourceConnector": {
		PluginName:    "KinesisSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.aws.lambda.AwsLambdaSinkConnector": {
		PluginName:    "LambdaSink",
		ConnectorType: "Sink",
	},
	"io.debezium.connector.v2.mariadb.MariaDbConnector": {
		PluginName:    "MariaDbCdcSource",
		ConnectorType: "Source",
	},
	"com.mongodb.kafka.connect.MongoSinkConnector": {
		PluginName:    "MongoDbAtlasSink",
		ConnectorType: "Sink",
	},
	"com.mongodb.kafka.connect.MongoSourceConnector": {
		PluginName:    "MongoDbAtlasSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.mqtt.MqttSinkConnector": {
		PluginName:    "MqttSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.mqtt.MqttSourceConnector": {
		PluginName:    "MqttSource",
		ConnectorType: "Source",
	},
	"io.debezium.connector.mysql.MySqlConnector": {
		PluginName:    "MySqlCdcSource",
		ConnectorType: "Source",
	},
	"io.debezium.connector.v2.mysql.MySqlConnectorV2": {
		PluginName:    "MySqlCdcSourceV2",
		ConnectorType: "Source",
	},
	"io.confluent.connect.newrelic.metrics.NewRelicMetricsSinkConnector": {
		PluginName:    "NewRelicMetricsSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.oracle.cdc.OracleCdcSourceConnector": {
		PluginName:    "OracleCdcSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.oracle.xstream.cdc.OracleXStreamSourceConnector": {
		PluginName:    "OracleXStreamSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.pagerduty.PagerDutySinkConnector": {
		PluginName:    "PagerDutySink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.pinecone.PineconeSinkConnector": {
		PluginName:    "PineconeSink",
		ConnectorType: "Sink",
	},
	"io.debezium.connector.postgresql.PostgresConnector": {
		PluginName:    "PostgresCdcSource",
		ConnectorType: "Source",
	},
	"io.debezium.connector.v2.postgresql.PostgresConnectorV2": {
		PluginName:    "PostgresCdcSourceV2",
		ConnectorType: "Source",
	},
	"io.confluent.connect.gcp.pubsub.PubSubSourceConnector": {
		PluginName:    "PubSubSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.rabbitmq.sink.RabbitMQSinkConnector": {
		PluginName:    "RabbitMQSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.rabbitmq.RabbitMQSourceConnector": {
		PluginName:    "RabbitMQSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.rediskafka.RedisKafkaSinkConnector": {
		PluginName:    "RedisKafkaSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.rediskafka.RedisKafkaSourceConnector": {
		PluginName:    "RedisKafkaSource",
		ConnectorType: "Source",
	},
	"com.github.jcustenborder.kafka.connect.redis.RedisSinkConnector": {
		PluginName:    "RedisSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.aws.redshift.RedshiftSinkConnector": {
		PluginName:    "RedshiftSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.s3.source.S3SourceConnector": {
		PluginName:    "S3Source",
		ConnectorType: "Source",
	},
	"io.confluent.connect.salesforce.SalesforceBulkApiSourceConnector": {
		PluginName:    "SalesforceBulkApiSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.salesforce.SalesforceBulkApiSinkConnector": {
		PluginName:    "SalesforceBulkApiV2Sink",
		ConnectorType: "Sink",
	},
	"io.confluent.salesforce.SalesforceCdcSourceConnector": {
		PluginName:    "SalesforceCdcSource",
		ConnectorType: "Source",
	},
	"io.confluent.salesforce.SalesforcePlatformEventSinkConnector": {
		PluginName:    "SalesforcePlatformEventSink",
		ConnectorType: "Sink",
	},
	"io.confluent.salesforce.SalesforcePlatformEventSourceConnector": {
		PluginName:    "SalesforcePlatformEventSource",
		ConnectorType: "Source",
	},
	"io.confluent.salesforce.SalesforcePushTopicSourceConnector": {
		PluginName:    "SalesforcePushTopicSource",
		ConnectorType: "Source",
	},
	"io.confluent.salesforce.SalesforceSObjectSinkConnector": {
		PluginName:    "SalesforceSObjectSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.servicenow.ServiceNowSinkConnector": {
		PluginName:    "ServiceNowSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.servicenow.ServiceNowSourceConnector": {
		PluginName:    "ServiceNowSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.sftp.SftpSinkConnector": {
		PluginName:    "SftpSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.sftp.SftpGenericSourceConnector": {
		PluginName:    "SftpSource",
		ConnectorType: "Source",
	},
	"com.snowflake.kafka.connector.SnowflakeSinkConnector": {
		PluginName:    "SnowflakeSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.snowflake.jdbc.SnowflakeSourceConnector": {
		PluginName:    "SnowflakeSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.jms.SolaceSinkConnector": {
		PluginName:    "SolaceSink",
		ConnectorType: "Sink",
	},
	"io.confluent.connect.gcp.spanner.SpannerSinkConnector": {
		PluginName:    "SpannerSink",
		ConnectorType: "Sink",
	},
	"com.splunk.kafka.connect.SplunkSinkConnector": {
		PluginName:    "SplunkSink",
		ConnectorType: "Sink",
	},
	"io.debezium.connector.sqlserver.SqlServerConnector": {
		PluginName:    "SqlServerCdcSource",
		ConnectorType: "Source",
	},
	"io.debezium.connector.v2.sqlserver.SqlServerConnectorV2": {
		PluginName:    "SqlServerCdcSourceV2",
		ConnectorType: "Source",
	},
	"io.confluent.connect.sqs.source.SqsSourceConnector": {
		PluginName:    "SqsSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.zendesk.ZendeskSourceConnector": {
		PluginName:    "ZendeskSource",
		ConnectorType: "Source",
	},
	"io.confluent.connect.s3.S3SinkConnector": {
		PluginName:    "S3_SINK", // Above source sets this to s3-sink which is invalid.
		ConnectorType: "Sink",
	},
}
