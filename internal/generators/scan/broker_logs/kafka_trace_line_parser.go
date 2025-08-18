package broker_logs

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

const (
	AuthTypeIAM             = "IAM"
	AuthTypeSASL_SCRAM      = "SASL_SCRAM"
	AuthTypeTLS             = "TLS"
	AuthTypeUNAUTHENTICATED = "UNAUTHENTICATED"
	AuthTypeUNKNOWN         = "UNKNOWN"
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

	ProducerTopicPattern = regexp.MustCompile(`partitionSizes=\[(.+)-\d+=`)
	ConsumerTopicPattern = regexp.MustCompile(`FetchTopic\(topic='([^']+)'`)

	// IAM-specific pattern to extract ARN
	IAMPrincipalArnPattern = regexp.MustCompile(`principal:\[IAM\]:\[(arn:aws:[^\]]+)\]:`)
	// SASL_SCRAM-specific pattern to extract User:username
	SASLSCRAMPrincipalPattern = regexp.MustCompile(`principal:(User:[^ ]+)`)
	// TLS pattern - extract User:CN= and any additional certificate info (requires SSL protocol)
	TLSPrincipalPattern = regexp.MustCompile(`securityProtocol:SSL,principal:(User:CN=[^(]+?)\s*\(`)
	// Anonymous pattern - detect unauthenticated requests with User:ANONYMOUS
	AnonymousPrincipalPattern = regexp.MustCompile(`principal:(User:ANONYMOUS)`)
)

type KafkaApiTraceLineParser struct{}

func (p *KafkaApiTraceLineParser) Parse(line string, lineNumber int, fileName string) (*RequestMetadata, error) {
	apiKey := extractField(line, ApiKeyPattern)
	clientId := extractField(line, ClientIdPattern)

	if apiKey != "FETCH" && apiKey != "PRODUCE" {
		return nil, ErrorUnsupportedLogLine
	}

	if clientId == "amazon.msk.canary.client" || BrokerFetcherPattern.MatchString(clientId) {
		return nil, ErrorUnsupportedLogLine
	}

	auth, principal := determineAuthTypeAndPrincipal(line)

	var role, topic string
	switch apiKey {
	case "FETCH":
		role = "Consumer"
		topic = extractField(line, ConsumerTopicPattern)

	case "PRODUCE":
		role = "Producer"
		topic = extractField(line, ProducerTopicPattern)
	}

	timestampMatches := TimestampPattern.FindStringSubmatch(line)
	if len(timestampMatches) != 2 {
		return nil, ErrorUnableToParseKafkaApiLine
	}

	timestamp, err := time.Parse("2006-01-02 15:04:05,000", timestampMatches[1])
	if err != nil {
		return nil, ErrorUnableToParseTimestamp
	}

	requestMetadata := RequestMetadata{
		CompositeKey: fmt.Sprintf("%s|%s|%s|%s|%s", clientId, topic, role, auth, principal),
		ClientId:     clientId,
		Topic:        topic,
		Role:         role,
		GroupId:      "N/A",
		Principal:    principal,
		Auth:         auth,
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
	// Unauthenticated requests - matches principal:User:ANONYMOUS
	if principal := extractField(logLine, AnonymousPrincipalPattern); principal != "" {
		return AuthTypeUNAUTHENTICATED, principal
	}

	// IAM authentication - matches principal:[IAM]:[arn:aws:...] pattern
	if principal := extractField(logLine, IAMPrincipalArnPattern); principal != "" {
		return AuthTypeIAM, principal
	}

	// TLS client certificate - matches securityProtocol:SSL,principal:User:CN=... pattern
	if principal := extractField(logLine, TLSPrincipalPattern); principal != "" {
		return AuthTypeTLS, principal
	}

	// SASL_SCRAM authentication - matches principal:User:<username> (without CN=)
	if principal := extractField(logLine, SASLSCRAMPrincipalPattern); principal != "" {
		return AuthTypeSASL_SCRAM, principal
	}

	return AuthTypeUNKNOWN, ""
}
