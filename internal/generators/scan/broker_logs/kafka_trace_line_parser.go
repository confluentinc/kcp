package broker_logs

import (
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

var (
	AuthTypeIAM        = "IAM"
	AuthTypeSASL_SCRAM = "SASL_SCRAM"
	// AuthTypeTLS = "TLS"
	// AuthTypeNONE = "NONE"
	AuthTypeUNKNOWN = "UNKNOWN"
)

var (
	ErrorUnableToParseKafkaApiLine = errors.New("unable to parse Kafka API line")
	ErrorUnableToParseTimestamp    = errors.New("unable to parse timestamp")
	ErrorUnsupportedLogLine        = errors.New("unsupported log line")
)

var (
	TimestampPattern = regexp.MustCompile(`^\[([^\]]+)\]`)
	ApiKeyPattern    = regexp.MustCompile(`apiKey=([^,\)]+)`)
	ClientIdPattern  = regexp.MustCompile(`clientId=([^,\)]+)`)

	// Producer patterns (PRODUCE operations)
	ProducerTopicPattern     = regexp.MustCompile(`partitionSizes=\[(.+)-\d+=`)
	ProducerIpPattern        = regexp.MustCompile(`from connection INTERNAL_IP-(\d+\.\d+\.\d+\.\d+):`)
	ProducerPrincipalPattern = regexp.MustCompile(`principal:([^ ]+)`)

	// Consumer patterns (FETCH operations)
	ConsumerTopicPattern     = regexp.MustCompile(`FetchTopic\(topic='([^']+)'`)
	// ConsumerIpPattern        = regexp.MustCompile(`from connection ([^;]+);`)
	ConsumerIpPattern        = regexp.MustCompile(`from connection INTERNAL_IP-(\d+\.\d+\.\d+\.\d+):`)
	ConsumerPrincipalPattern = regexp.MustCompile(`principal:([^ ]+)`)
	BrokerFetcherPattern     = regexp.MustCompile(`broker-(\d+)-fetcher-(\d+)`)
)

type KafkaApiTraceLineParser struct{}

func (p *KafkaApiTraceLineParser) Parse(line string, lineNumber int, fileName string) (*RequestMetadata, error) {
	timestampMatches := TimestampPattern.FindStringSubmatch(line)
	if len(timestampMatches) != 2 {
		slog.Debug("failed to match timestamp", "line", line)
		return nil, ErrorUnableToParseKafkaApiLine
	}

	timestamp, err := time.Parse("2006-01-02 15:04:05,000", timestampMatches[1])
	if err != nil {
		slog.Debug("failed to parse timestamp", "timestamp", timestampMatches[1], "error", err)
		return nil, ErrorUnableToParseTimestamp
	}

	apiKey := extractField(line, ApiKeyPattern)
	clientId := extractField(line, ClientIdPattern)

	// are we interested in anything else
	if apiKey != "FETCH" && apiKey != "PRODUCE" {
		return nil, ErrorUnsupportedLogLine
	}

	// do we care about these?
	if clientId == "amazon.msk.canary.client" || BrokerFetcherPattern.MatchString(clientId) {
		return nil, ErrorUnsupportedLogLine
	}

	var role, topic, ipAddress, auth, principal string

	switch apiKey {
	case "FETCH":
		role = "Consumer"
		topic = extractField(line, ConsumerTopicPattern)
		ipAddress = extractField(line, ConsumerIpPattern)
		auth = determineAuthType(line)
		principal = extractField(line, ConsumerPrincipalPattern)

	case "PRODUCE":
		role = "Producer"
		topic = extractField(line, ProducerTopicPattern)
		ipAddress = extractField(line, ProducerIpPattern)
		auth = determineAuthType(line)
		principal = extractField(line, ProducerPrincipalPattern)
	}

	requestMetadata := RequestMetadata{
		ClientId:   clientId,
		ClientType: "External App",
		Topic:      topic,
		Role:       role,
		GroupId:    "N/A",
		Principal:  principal,
		Auth:       auth,
		IPAddress:  ipAddress,
		ApiKey:     apiKey,
		Timestamp:  timestamp,
		FileName:   fileName,
		LineNumber: lineNumber,
		LogLine:    line,
	}

	return &requestMetadata, nil
}

func extractField(line string, pattern *regexp.Regexp) string {
	matches := pattern.FindStringSubmatch(line)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

func determineAuthType(logLine string) string {
	if strings.Contains(logLine, "principal:[IAM]:") {
		return AuthTypeIAM
	} else if strings.Contains(logLine, "principal:User:") {
		return AuthTypeSASL_SCRAM
	}
	// else if strings.Contains(logLine, "securityProtocol:SSL") {
	// 	return "TLS" // Pure TLS without SASL
	// } else if strings.Contains(logLine, "securityProtocol:PLAINTEXT") {
	// 	return "NONE"
	// }
	// return "UNKNOWN"
	return AuthTypeUNKNOWN
}
