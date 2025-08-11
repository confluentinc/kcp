package broker_logs

import (
	"errors"
	"fmt"
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
	TimestampPattern     = regexp.MustCompile(`^\[([^\]]+)\]`)
	ApiKeyPattern        = regexp.MustCompile(`apiKey=([^,\)]+)`)
	ClientIdPattern      = regexp.MustCompile(`clientId=([^,\)]+)`)
	BrokerFetcherPattern = regexp.MustCompile(`broker-(\d+)-fetcher-(\d+)`)
	IpPattern            = regexp.MustCompile(`from connection INTERNAL_IP-(\d+\.\d+\.\d+\.\d+):`)

	ProducerTopicPattern = regexp.MustCompile(`partitionSizes=\[(.+)-\d+=`)
	ConsumerTopicPattern = regexp.MustCompile(`FetchTopic\(topic='([^']+)'`)

	// IAM-specific pattern to extract ARN
	IAMPrincipalArnPattern = regexp.MustCompile(`principal:\[IAM\]:\[(arn:aws:[^\]]+)\]:`)

	// SASL_SCRAM-specific pattern to extract User:username
	SASLSCRAMPrincipalPattern = regexp.MustCompile(`principal:(User:[^ ]+)`)
)

type KafkaApiTraceLineParser struct{}

func (p *KafkaApiTraceLineParser) Parse(line string, lineNumber int, fileName string) (*RequestMetadata, error) {
	timestampMatches := TimestampPattern.FindStringSubmatch(line)
	if len(timestampMatches) != 2 {
		return nil, ErrorUnableToParseKafkaApiLine
	}

	timestamp, err := time.Parse("2006-01-02 15:04:05,000", timestampMatches[1])
	if err != nil {
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

	auth, principal := determineAuthTypeAndPrincipal(line)
	ipAddress := extractField(line, IpPattern)

	var role, topic string
	switch apiKey {
	case "FETCH":
		role = "Consumer"
		topic = extractField(line, ConsumerTopicPattern)

	case "PRODUCE":
		role = "Producer"
		topic = extractField(line, ProducerTopicPattern)
	}

	requestMetadata := RequestMetadata{
		CompositeKey: fmt.Sprintf("%s|%s|%s|%s|%s|%s", clientId, topic, role, auth, principal, ipAddress),
		ClientId:     clientId,
		ClientType:   "External App",
		Topic:        topic,
		Role:         role,
		GroupId:      "N/A",
		Principal:    principal,
		Auth:         auth,
		IPAddress:    ipAddress,
		ApiKey:       apiKey,
		Timestamp:    timestamp,
		FileName:     fileName,
		LineNumber:   lineNumber,
		LogLine:      line,
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

func determineAuthTypeAndPrincipal(logLine string) (string, string) {
	// iam
	if strings.Contains(logLine, "principal:[IAM]:") {
		principal := extractField(logLine, IAMPrincipalArnPattern)
		return AuthTypeIAM, principal
	}
	// sasl scram
	if strings.Contains(logLine, "principal:User:") {
		principal := extractField(logLine, SASLSCRAMPrincipalPattern)
		return AuthTypeSASL_SCRAM, principal
	}
	// else if strings.Contains(logLine, "securityProtocol:SSL") {
	// 	return "TLS", "" // Pure TLS without SASL
	// } else if strings.Contains(logLine, "securityProtocol:PLAINTEXT") {
	// 	return "NONE", ""
	// }
	return AuthTypeUNKNOWN, ""
}
