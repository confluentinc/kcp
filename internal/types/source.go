package types

import "fmt"

// SourceType represents the type of Kafka source
type SourceType string

const (
	SourceTypeMSK SourceType = "msk"
	SourceTypeOSK SourceType = "osk"
)

// ParseSourceTypeFlag maps a user-facing --source-type flag value to the internal
// SourceType token. The Apache Kafka source is "apache-kafka" to users but is
// represented internally as "osk".
func ParseSourceTypeFlag(flag string) (SourceType, error) {
	switch flag {
	case "msk":
		return SourceTypeMSK, nil
	case "apache-kafka":
		return SourceTypeOSK, nil
	default:
		return "", fmt.Errorf("invalid source-type %q: must be 'msk' or 'apache-kafka'", flag)
	}
}
