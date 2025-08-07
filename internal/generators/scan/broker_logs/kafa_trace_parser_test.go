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
		expectedResult *ApiRequest
		expectedError  error
	}{
		{
			name:       "valid PRODUCE request",
			line:       `[2025-07-25 14:45:53,662] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=PRODUCE, apiVersion=7, clientId=TESTING_PRODUCER-1, correlationId=2, headerVersion=1) -- {acks=1,timeout=10000,partitionSizes=[customers1-0=107]} from connection INTERNAL_IP-65.1.63.214:33245-169;securityProtocol:SASL_SSL,principal:[IAM]:[arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io]:[INTERNAL_IP-65.1.63.214:33245-169]:[00079d61-baba-497e-87c2-80c46608f1da] (kafka.server.KafkaApis)`,
			lineNumber: 1,
			fileName:   "test.log",
			expectedResult: &ApiRequest{
				Timestamp: time.Date(2025, 7, 25, 14, 45, 53, 662000000, time.UTC),
				ApiKey:    "PRODUCE",
				ClientId:  "TESTING_PRODUCER-1",
				Topic:     "customers1",
				IPAddress: "65.1.63.214",
				Auth:      "IAM",
				Principal: "arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io",
			},
			expectedError: nil,
		},
		{
			name:       "PRODUCE request with topic containing hyphens",
			line:       `[2025-07-25 14:45:53,662] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=PRODUCE, apiVersion=7, clientId=TESTING_PRODUCER-1, correlationId=2, headerVersion=1) -- {acks=1,timeout=10000,partitionSizes=[test-topic-0=107]} from connection INTERNAL_IP-65.1.63.214:33245-169;securityProtocol:SASL_SSL,principal:[IAM]:[arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io]:[INTERNAL_IP-65.1.63.214:33245-169]:[00079d61-baba-497e-87c2-80c46608f1da] (kafka.server.KafkaApis)`,
			lineNumber: 8,
			fileName:   "test.log",
			expectedResult: &ApiRequest{
				Timestamp: time.Date(2025, 7, 25, 14, 45, 53, 662000000, time.UTC),
				ApiKey:    "PRODUCE",
				ClientId:  "TESTING_PRODUCER-1",
				Topic:     "test-topic",
				IPAddress: "65.1.63.214",
				Auth:      "IAM",
				Principal: "arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io",
			},
			expectedError: nil,
		},
		{
			name:       "PRODUCE request with topic containing underscores",
			line:       `[2025-07-25 14:45:53,662] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=PRODUCE, apiVersion=7, clientId=TESTING_PRODUCER-1, correlationId=2, headerVersion=1) -- {acks=1,timeout=10000,partitionSizes=[test_topic-0=107]} from connection INTERNAL_IP-65.1.63.214:33245-169;securityProtocol:SASL_SSL,principal:[IAM]:[arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io]:[INTERNAL_IP-65.1.63.214:33245-169]:[00079d61-baba-497e-87c2-80c46608f1da] (kafka.server.KafkaApis)`,
			lineNumber: 9,
			fileName:   "test.log",
			expectedResult: &ApiRequest{
				Timestamp: time.Date(2025, 7, 25, 14, 45, 53, 662000000, time.UTC),
				ApiKey:    "PRODUCE",
				ClientId:  "TESTING_PRODUCER-1",
				Topic:     "test_topic",
				IPAddress: "65.1.63.214",
				Auth:      "IAM",
				Principal: "arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io",
			},
			expectedError: nil,
		},
		{
			name:       "PRODUCE request with complex topic name and partition",
			line:       `[2025-07-25 14:45:53,662] TRACE [KafkaApi-1] Handling request:RequestHeader(apiKey=PRODUCE, apiVersion=7, clientId=TESTING_PRODUCER-1, correlationId=2, headerVersion=1) -- {acks=1,timeout=10000,partitionSizes=[test-topic-1-0=1024]} from connection INTERNAL_IP-65.1.63.214:33245-169;securityProtocol:SASL_SSL,principal:[IAM]:[arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io]:[INTERNAL_IP-65.1.63.214:33245-169]:[00079d61-baba-497e-87c2-80c46608f1da] (kafka.server.KafkaApis)`,
			lineNumber: 10,
			fileName:   "test.log",
			expectedResult: &ApiRequest{
				Timestamp: time.Date(2025, 7, 25, 14, 45, 53, 662000000, time.UTC),
				ApiKey:    "PRODUCE",
				ClientId:  "TESTING_PRODUCER-1",
				Topic:     "test-topic-1",
				IPAddress: "65.1.63.214",
				Auth:      "IAM",
				Principal: "arn:aws:sts::635910096382:assumed-role/AWSReservedSSO_nonprod-administrator_b3955bd58a347b7b/me@confluent.io",
			},
			expectedError: nil,
		},
		{
			name:       "PRODUCE request with simple topic name",
			line:       `[2024-01-15 10:38:00,000] TRACE [ReplicaManager broker=1] Completed request:RequestHeader(apiKey=PRODUCE, apiVersion=8, clientId=producer-client-1, correlationId=42) -- partitionSizes=[testtopic-0=1024] from connection INTERNAL_IP-192.168.1.10:9092; principal:[User:test-user]:[test-user]; totalTime:5.2ms`,
			lineNumber: 11,
			fileName:   "test.log",
			expectedResult: &ApiRequest{
				Timestamp: time.Date(2024, 1, 15, 10, 38, 0, 0, time.UTC),
				ApiKey:    "PRODUCE",
				ClientId:  "producer-client-1",
				Topic:     "testtopic",
				IPAddress: "192.168.1.10",
				Auth:      "User:test-user",
				Principal: "test-user",
			},
			expectedError: nil,
		},
		{
			name:           "unsupported API key (METADATA)",
			line:           `[2024-01-15 10:33:00,999] TRACE [ReplicaManager broker=1] Completed request:RequestHeader(apiKey=METADATA, apiVersion=9, clientId=metadata-client, correlationId=789) totalTime:0.5ms`,
			lineNumber:     100,
			fileName:       "test.log",
			expectedResult: nil,
			expectedError:  ErrorUnsupportedApiKey,
		},
		{
			name:           "unsupported API key (HEARTBEAT)",
			line:           `[2024-01-15 10:34:00,999] TRACE [ReplicaManager broker=1] Completed request:RequestHeader(apiKey=HEARTBEAT, apiVersion=3, clientId=consumer-group-client, correlationId=456) totalTime:1.0ms`,
			lineNumber:     101,
			fileName:       "test.log",
			expectedResult: nil,
			expectedError:  ErrorUnsupportedApiKey,
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

// ,partitionSizes=[customers1-0=107]}
