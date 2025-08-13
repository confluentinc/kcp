package broker_logs

import (
	"testing"
	"time"
)

func TestKafkaApiTraceLineParser_Parse(t *testing.T) {
	processor := &KafkaApiTraceLineParser{}

	tests := []struct {
		name           string
		line           string
		lineNumber     int
		fileName       string
		expectedResult *RequestMetadata
		expectedError  error
	}{
		{
			name:       "valid PRODUCE request for IAM auth",
			line:       `[2025-07-25 14:45:53,662] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=PRODUCE, apiVersion=7, clientId=TESTING_PRODUCER-1, correlationId=2, headerVersion=1) -- {acks=1,timeout=10000,partitionSizes=[customers1-0=107]} from connection INTERNAL_IP-65.1.63.214:33245-169;securityProtocol:SASL_SSL,principal:[IAM]:[arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io]:[INTERNAL_IP-65.1.63.214:33245-169]:[00079d61-baba-497e-87c2-80c46608f1da] (kafka.server.KafkaApis)`,
			lineNumber: 1,
			fileName:   "test.log",
			expectedResult: &RequestMetadata{
				Timestamp: time.Date(2025, 7, 25, 14, 45, 53, 662000000, time.UTC),
				Role:      "Producer",
				ApiKey:    "PRODUCE",
				ClientId:  "TESTING_PRODUCER-1",
				Topic:     "customers1",
				IPAddress: "65.1.63.214",
				Auth:      "IAM",
				Principal: "arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io",
			},
		},
		{
			name:       "valid PRODUCE request for SASL_SCRAM auth",
			line:       `[2025-08-07 14:34:27,495] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=PRODUCE, apiVersion=7, clientId=producer_with_sasl_scram-9, correlationId=19, headerVersion=1) -- {acks=1,timeout=10000,partitionSizes=[test-topic-1-5=108]} from connection INTERNAL_IP-65.1.63.214:14972-3;securityProtocol:SASL_SSL,principal:User:kafka-user-2 (kafka.server.KafkaApis)`,
			lineNumber: 2,
			fileName:   "test.log",
			expectedResult: &RequestMetadata{
				Timestamp: time.Date(2025, 8, 7, 14, 34, 27, 495000000, time.UTC),
				Role:      "Producer",
				ApiKey:    "PRODUCE",
				ClientId:  "producer_with_sasl_scram-9",
				Topic:     "test-topic-1",
				IPAddress: "65.1.63.214",
				Auth:      "SASL_SCRAM",
				Principal: "User:kafka-user-2",
			},
		},
		{
			name:       "valid PRODUCE request for TLS auth",
			line:       `[2025-08-13 12:11:26,271] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=PRODUCE, apiVersion=7, clientId=sarama, correlationId=1, headerVersion=1) -- {acks=1,timeout=10000,partitionSizes=[test-topic-1-1=109]} from connection INTERNAL_IP-65.1.63.214:31531-26;securityProtocol:SSL,principal:User:CN=kcp_tls_testing (kafka.server.KafkaApis)`,
			lineNumber: 1,
			fileName:   "test.log",
			expectedResult: &RequestMetadata{
				Timestamp: time.Date(2025, 8, 13, 12, 11, 26, 271000000, time.UTC),
				Role:      "Producer",
				ApiKey:    "PRODUCE",
				ClientId:  "sarama",
				Topic:     "test-topic-1",
				IPAddress: "65.1.63.214",
				Auth:      "TLS",
				Principal: "User:CN=kcp_tls_testing",
			},
		},
		{
			name:       "PRODUCE request with topic containing hyphens",
			line:       `[2025-07-25 14:45:53,662] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=PRODUCE, apiVersion=7, clientId=TESTING_PRODUCER-1, correlationId=2, headerVersion=1) -- {acks=1,timeout=10000,partitionSizes=[test-topic-0=107]} from connection INTERNAL_IP-65.1.63.214:33245-169;securityProtocol:SASL_SSL,principal:[IAM]:[arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io]:[INTERNAL_IP-65.1.63.214:33245-169]:[00079d61-baba-497e-87c2-80c46608f1da] (kafka.server.KafkaApis)`,
			lineNumber: 8,
			fileName:   "test.log",
			expectedResult: &RequestMetadata{
				Timestamp: time.Date(2025, 7, 25, 14, 45, 53, 662000000, time.UTC),
				Role:      "Producer",
				ApiKey:    "PRODUCE",
				ClientId:  "TESTING_PRODUCER-1",
				Topic:     "test-topic",
				IPAddress: "65.1.63.214",
				Auth:      "IAM",
				Principal: "arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io",
			},
		},
		{
			name:       "PRODUCE request with topic containing underscores",
			line:       `[2025-07-25 14:45:53,662] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=PRODUCE, apiVersion=7, clientId=TESTING_PRODUCER-1, correlationId=2, headerVersion=1) -- {acks=1,timeout=10000,partitionSizes=[test_topic-0=107]} from connection INTERNAL_IP-65.1.63.214:33245-169;securityProtocol:SASL_SSL,principal:[IAM]:[arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io]:[INTERNAL_IP-65.1.63.214:33245-169]:[00079d61-baba-497e-87c2-80c46608f1da] (kafka.server.KafkaApis)`,
			lineNumber: 9,
			fileName:   "test.log",
			expectedResult: &RequestMetadata{
				Timestamp: time.Date(2025, 7, 25, 14, 45, 53, 662000000, time.UTC),
				Role:      "Producer",
				ApiKey:    "PRODUCE",
				ClientId:  "TESTING_PRODUCER-1",
				Topic:     "test_topic",
				IPAddress: "65.1.63.214",
				Auth:      "IAM",
				Principal: "arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io",
			},
		},
		{
			name:       "PRODUCE request with complex topic name and partition",
			line:       `[2025-07-25 14:45:53,662] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=PRODUCE, apiVersion=7, clientId=TESTING_PRODUCER-1, correlationId=2, headerVersion=1) -- {acks=1,timeout=10000,partitionSizes=[test-topic-1-0=1024]} from connection INTERNAL_IP-65.1.63.214:33245-169;securityProtocol:SASL_SSL,principal:[IAM]:[arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io]:[INTERNAL_IP-65.1.63.214:33245-169]:[00079d61-baba-497e-87c2-80c46608f1da] (kafka.server.KafkaApis)`,
			lineNumber: 10,
			fileName:   "test.log",
			expectedResult: &RequestMetadata{
				Timestamp: time.Date(2025, 7, 25, 14, 45, 53, 662000000, time.UTC),
				Role:      "Producer",
				ApiKey:    "PRODUCE",
				ClientId:  "TESTING_PRODUCER-1",
				Topic:     "test-topic-1",
				IPAddress: "65.1.63.214",
				Auth:      "IAM",
				Principal: "arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io",
			},
		},
		{
			name:           "unsupported API key (METADATA)",
			line:           `[2024-01-15 10:33:00,999] TRACE [ReplicaManager broker=1] Completed request:RequestHeader(apiKey=METADATA, apiVersion=9, clientId=metadata-client, correlationId=789) totalTime:0.5ms`,
			lineNumber:     100,
			fileName:       "test.log",
			expectedResult: nil,
			expectedError:  ErrorUnsupportedLogLine,
		},
		{
			name:       "valid FETCH request for SASL_SCRAM auth",
			line:       `[2025-08-08 07:30:42,834] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=FETCH, apiVersion=11, clientId=glenns_consumer_with_sasl_scram, correlationId=2010, headerVersion=1) -- FetchRequestData(clusterId=null, replicaId=-1, replicaState=ReplicaState(replicaId=-1, replicaEpoch=-1), maxWaitMs=500, minBytes=1, maxBytes=104857600, isolationLevel=0, sessionId=0, sessionEpoch=-1, topics=[FetchTopic(topic='test-topic-1', topicId=AAAAAAAAAAAAAAAAAAAAAA, partitions=[FetchPartition(partition=2, currentLeaderEpoch=0, fetchOffset=20419, lastFetchedEpoch=-1, logStartOffset=0, partitionMaxBytes=1048576), FetchPartition(partition=5, currentLeaderEpoch=0, fetchOffset=20258, lastFetchedEpoch=-1, logStartOffset=0, partitionMaxBytes=1048576)])], forgottenTopicsData=[], rackId='') from connection INTERNAL_IP-65.1.63.214:25530-59;securityProtocol:SASL_SSL,principal:User:kafka-user-2 (kafka.server.KafkaApis)`,
			lineNumber: 1,
			fileName:   "test.log",
			expectedResult: &RequestMetadata{
				Timestamp: time.Date(2025, 8, 8, 7, 30, 42, 834000000, time.UTC),
				Role:      "Consumer",
				ApiKey:    "FETCH",
				ClientId:  "glenns_consumer_with_sasl_scram",
				Topic:     "test-topic-1",
				IPAddress: "65.1.63.214",
				Auth:      "SASL_SCRAM",
				Principal: "User:kafka-user-2",
			},
		},
		{
			name:       "valid FETCH request for TLS auth",
			line:       `[2025-08-13 12:11:38,087] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=FETCH, apiVersion=11, clientId=sarama, correlationId=9, headerVersion=1) -- FetchRequestData(clusterId=null, replicaId=-1, replicaState=ReplicaState(replicaId=-1, replicaEpoch=-1), maxWaitMs=500, minBytes=1, maxBytes=104857600, isolationLevel=0, sessionId=0, sessionEpoch=-1, topics=[FetchTopic(topic='test-topic-1', topicId=AAAAAAAAAAAAAAAAAAAAAA, partitions=[FetchPartition(partition=1, currentLeaderEpoch=0, fetchOffset=180, lastFetchedEpoch=-1, logStartOffset=0, partitionMaxBytes=1048576)])], forgottenTopicsData=[], rackId='') from connection INTERNAL_IP-65.1.63.214:21744-27;securityProtocol:SSL,principal:User:CN=kcp_tls_testing (kafka.server.KafkaApis)`,
			lineNumber: 1,
			fileName:   "test.log",
			expectedResult: &RequestMetadata{
				Timestamp: time.Date(2025, 8, 13, 12, 11, 38, 87000000, time.UTC),
				Role:      "Consumer",
				ApiKey:    "FETCH",
				ClientId:  "sarama",
				Topic:     "test-topic-1",
				IPAddress: "65.1.63.214",
				Auth:      "TLS",
				Principal: "User:CN=kcp_tls_testing",
			},
		},
		{
			name:       "valid FETCH request for TLS auth CN with more than one word",
			line:       `[2025-08-13 12:11:38,087] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=FETCH, apiVersion=11, clientId=sarama, correlationId=9, headerVersion=1) -- FetchRequestData(clusterId=null, replicaId=-1, replicaState=ReplicaState(replicaId=-1, replicaEpoch=-1), maxWaitMs=500, minBytes=1, maxBytes=104857600, isolationLevel=0, sessionId=0, sessionEpoch=-1, topics=[FetchTopic(topic='test-topic-1', topicId=AAAAAAAAAAAAAAAAAAAAAA, partitions=[FetchPartition(partition=1, currentLeaderEpoch=0, fetchOffset=180, lastFetchedEpoch=-1, logStartOffset=0, partitionMaxBytes=1048576)])], forgottenTopicsData=[], rackId='') from connection INTERNAL_IP-65.1.63.214:21744-27;securityProtocol:SSL,principal:User:CN=kcp testing (kafka.server.KafkaApis)`,
			lineNumber: 1,
			fileName:   "test.log",
			expectedResult: &RequestMetadata{
				Timestamp: time.Date(2025, 8, 13, 12, 11, 38, 87000000, time.UTC),
				Role:      "Consumer",
				ApiKey:    "FETCH",
				ClientId:  "sarama",
				Topic:     "test-topic-1",
				IPAddress: "65.1.63.214",
				Auth:      "TLS",
				Principal: "User:CN=kcp testing",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := processor.Parse(tt.line, tt.lineNumber, tt.fileName)

			// Check error
			if tt.expectedError != nil {
				if err == nil {
					t.Errorf("expected error %v, got nil", tt.expectedError)
					return
				}
				if err != tt.expectedError {
					t.Errorf("expected error %v, got %v", tt.expectedError, err)
					return
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Check result
			if result == nil {
				t.Errorf("expected result, got nil")
				return
			}

			if !result.Timestamp.Equal(tt.expectedResult.Timestamp) {
				t.Errorf("expected timestamp %v, got %v", tt.expectedResult.Timestamp, result.Timestamp)
			}

			if result.ApiKey != tt.expectedResult.ApiKey {
				t.Errorf("expected ApiKey %q, got %q", tt.expectedResult.ApiKey, result.ApiKey)
			}

			if result.ClientId != tt.expectedResult.ClientId {
				t.Errorf("expected ClientId %q, got %q", tt.expectedResult.ClientId, result.ClientId)
			}

			if result.Role != tt.expectedResult.Role {
				t.Errorf("expected Role %q, got %q", tt.expectedResult.Role, result.Role)
			}

			if result.Topic != tt.expectedResult.Topic {
				t.Errorf("expected Topic %q, got %q", tt.expectedResult.Topic, result.Topic)
			}

			if result.IPAddress != tt.expectedResult.IPAddress {
				t.Errorf("expected IPAddress %q, got %q", tt.expectedResult.IPAddress, result.IPAddress)
			}

			if result.Auth != tt.expectedResult.Auth {
				t.Errorf("expected Auth %q, got %q", tt.expectedResult.Auth, result.Auth)
			}

			if result.Principal != tt.expectedResult.Principal {
				t.Errorf("expected Principal %q, got %q", tt.expectedResult.Principal, result.Principal)
			}
		})
	}
}

func TestDetermineAuthTypeAndPrincipal(t *testing.T) {
	tests := []struct {
		name              string
		logLine           string
		expectedAuthType  string
		expectedPrincipal string
	}{
		{
			name:              "IAM authentication with SASL_SSL",
			logLine:           `securityProtocol:SASL_SSL,principal:[IAM]:[arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io]:[INTERNAL_IP-65.1.63.214:33245-169]:[00079d61-baba-497e-87c2-80c46608f1da]`,
			expectedAuthType:  AuthTypeIAM,
			expectedPrincipal: "arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io",
		},
		{
			name:              "TLS authentication with SSL protocol and simple CN",
			logLine:           `securityProtocol:SSL,principal:User:CN=kcp_tls_testing (kafka.server.KafkaApis)`,
			expectedAuthType:  AuthTypeTLS,
			expectedPrincipal: "User:CN=kcp_tls_testing",
		},
		{
			name:              "TLS authentication with complex CN containing spaces",
			logLine:           `securityProtocol:SSL,principal:User:CN=kcp testing more info (kafka.server.KafkaApis)`,
			expectedAuthType:  AuthTypeTLS,
			expectedPrincipal: "User:CN=kcp testing more info",
		},
		{
			name:              "TLS authentication with CN containing underscores and numbers",
			logLine:           `securityProtocol:SSL,principal:User:CN=kcp_tls_testing_123 extra data (kafka.server.KafkaApis)`,
			expectedAuthType:  AuthTypeTLS,
			expectedPrincipal: "User:CN=kcp_tls_testing_123 extra data",
		},
		{
			name:              "SASL_SCRAM authentication with simple username",
			logLine:           `securityProtocol:SASL_SSL,principal:User:kafka-user-2 (kafka.server.KafkaApis)`,
			expectedAuthType:  AuthTypeSASL_SCRAM,
			expectedPrincipal: "User:kafka-user-2",
		},
		{
			name:              "SASL_SCRAM authentication with complex username",
			logLine:           `securityProtocol:SASL_SSL,principal:User:kafka_user_with_underscores (kafka.server.KafkaApis)`,
			expectedAuthType:  AuthTypeSASL_SCRAM,
			expectedPrincipal: "User:kafka_user_with_underscores",
		},
		{
			name:              "Unknown authentication - malformed principal",
			logLine:           `securityProtocol:SSL,principal:InvalidFormat`,
			expectedAuthType:  AuthTypeUNKNOWN,
			expectedPrincipal: "",
		},
		{
			name:              "Edge case - IAM with different security protocol should still work",
			logLine:           `securityProtocol:SSL,principal:[IAM]:[arn:aws:iam::123456789012:user/TestUser]:`,
			expectedAuthType:  AuthTypeIAM,
			expectedPrincipal: "arn:aws:iam::123456789012:user/TestUser",
		},
		{
			name:              "Full log line context - IAM",
			logLine:           `[2025-07-25 14:45:53,662] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=PRODUCE) from connection INTERNAL_IP-65.1.63.214:33245-169;securityProtocol:SASL_SSL,principal:[IAM]:[arn:aws:sts::635910096382:assumed-role/TestRole/user@example.com]:[INTERNAL_IP-65.1.63.214:33245-169]:[uuid] (kafka.server.KafkaApis)`,
			expectedAuthType:  AuthTypeIAM,
			expectedPrincipal: "arn:aws:sts::635910096382:assumed-role/TestRole/user@example.com",
		},
		{
			name:              "Full log line context - TLS",
			logLine:           `[2025-08-13 12:11:26,271] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=FETCH) from connection INTERNAL_IP-65.1.63.214:31531-26;securityProtocol:SSL,principal:User:CN=test_client_cert (kafka.server.KafkaApis)`,
			expectedAuthType:  AuthTypeTLS,
			expectedPrincipal: "User:CN=test_client_cert",
		},
		{
			name:              "Full log line context - SASL_SCRAM",
			logLine:           `[2025-08-07 14:34:27,495] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=PRODUCE) from connection INTERNAL_IP-65.1.63.214:14972-3;securityProtocol:SASL_SSL,principal:User:scram_user (kafka.server.KafkaApis)`,
			expectedAuthType:  AuthTypeSASL_SCRAM,
			expectedPrincipal: "User:scram_user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authType, principal := determineAuthTypeAndPrincipal(tt.logLine)

			if authType != tt.expectedAuthType {
				t.Errorf("expected auth type %q, got %q", tt.expectedAuthType, authType)
			}

			if principal != tt.expectedPrincipal {
				t.Errorf("expected principal %q, got %q", tt.expectedPrincipal, principal)
			}
		})
	}
}
