package broker_logs

import (
	"errors"
	"log/slog"
	"regexp"
	"time"
)

var (
	ErrorUnableToParseKafkaApiLine = errors.New("unable to parse Kafka API line")
	ErrorUnableToParseTimestamp    = errors.New("unable to parse timestamp")
	ErrorUnsupportedApiKey         = errors.New("unsupported API key - only FETCH and PRODUCE are supported")
	ErrorUnsupportedLogLine        = errors.New("unsupported log line")
)

var (
	TimestampPattern = regexp.MustCompile(`^\[([^\]]+)\]`)
	ApiKeyPattern    = regexp.MustCompile(`apiKey=([^,\)]+)`)
	ClientIdPattern  = regexp.MustCompile(`clientId=([^,\)]+)`)

	// Producer patterns (PRODUCE operations)
	ProducerTopicPattern     = regexp.MustCompile(`partitionSizes=\[(.+)-\d+=`)
	ProducerIpPattern        = regexp.MustCompile(`from connection INTERNAL_IP-(\d+\.\d+\.\d+\.\d+):`)
	ProducerAuthPattern      = regexp.MustCompile(`principal:\[([^\]]+)\]:`)
	ProducerPrincipalPattern = regexp.MustCompile(`principal:\[[^\]]+\]:\[([^\]]+)\]`)

	// Consumer patterns (FETCH operations)
	ConsumerTopicPattern     = regexp.MustCompile(`topics=\[([^\]]*)\]`)
	ConsumerIpPattern        = regexp.MustCompile(`from connection ([^;]+);`)
	ConsumerAuthPattern      = regexp.MustCompile(`principal:([^(]+)`)
	ConsumerPrincipalPattern = regexp.MustCompile(`principal:([^(]+)`)
	BrokerFetcherPattern     = regexp.MustCompile(`broker-(\d+)-fetcher-(\d+)`)
)

type KafkaApiTraceLineParser struct{}

func (p *KafkaApiTraceLineParser) Parse(line string, lineNumber int, fileName string) (*ApiRequest, error) {
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

	apiKey := p.extractField(line, ApiKeyPattern)
	clientId := p.extractField(line, ClientIdPattern)

	// Only process FETCH and PRODUCE operations
	if apiKey != "FETCH" && apiKey != "PRODUCE" {
		slog.Debug("unsupported API key", "apiKey", apiKey)
		return nil, ErrorUnsupportedApiKey
	}

	// how do we handle these?
	if clientId == "amazon.msk.canary.client" || BrokerFetcherPattern.MatchString(clientId) {
		return nil, ErrorUnsupportedLogLine
	}

	var topic, ipAddress, auth, principal string

	switch apiKey {
	case "FETCH":
		topic = p.extractField(line, ConsumerTopicPattern)
		ipAddress = p.extractField(line, ConsumerIpPattern)
		auth = p.extractField(line, ConsumerAuthPattern)
		principal = p.extractField(line, ConsumerPrincipalPattern)

	case "PRODUCE":
		topic = p.extractField(line, ProducerTopicPattern)
		ipAddress = p.extractField(line, ProducerIpPattern)
		auth = p.extractField(line, ProducerAuthPattern)
		principal = p.extractField(line, ProducerPrincipalPattern)
	}

	apiRequest := ApiRequest{
		Timestamp:  timestamp,
		ApiKey:     apiKey,
		ClientId:   clientId,
		Topic:      topic,
		IPAddress:  ipAddress,
		Auth:       auth,
		Principal:  principal,
		FileName:   fileName,
		LineNumber: lineNumber,
		LogLine:    line,
	}

	return &apiRequest, nil
}

func (p *KafkaApiTraceLineParser) extractField(line string, pattern *regexp.Regexp) string {
	matches := pattern.FindStringSubmatch(line)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}
