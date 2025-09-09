package utils

import (
	"strings"
)

func ConvertKafkaVersion(kafkaVersion *string) string {
	switch {
	case strings.Contains(*kafkaVersion, "kraft"):
		return strings.ReplaceAll(*kafkaVersion, ".x.kraft", ".0")
	case strings.Contains(*kafkaVersion, "x"):
		return strings.ReplaceAll(*kafkaVersion, ".x", ".0")
	case strings.Contains(*kafkaVersion, "tiered"):
		return strings.ReplaceAll(*kafkaVersion, ".tiered", "")
	case *kafkaVersion == "3.6.0.1":
		return "3.6.0"
	default:
		return *kafkaVersion
	}
}
