package utils

import (
	"strings"

	"github.com/confluentinc/kcp/internal/types"
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

// Gets a list of the authentication method(s) selected in the `creds.yaml` file generated during discovery.
func GetAuthMethods(clusterEntry types.ClusterEntry) []string {
	enabledMethods := []string{}

	if clusterEntry.AuthMethod.Unauthenticated != nil && clusterEntry.AuthMethod.Unauthenticated.Use {
		enabledMethods = append(enabledMethods, "unauthenticated")
	}
	if clusterEntry.AuthMethod.IAM != nil && clusterEntry.AuthMethod.IAM.Use {
		enabledMethods = append(enabledMethods, "iam")
	}
	if clusterEntry.AuthMethod.SASLScram != nil && clusterEntry.AuthMethod.SASLScram.Use {
		enabledMethods = append(enabledMethods, "sasl_scram")
	}
	if clusterEntry.AuthMethod.TLS != nil && clusterEntry.AuthMethod.TLS.Use {
		enabledMethods = append(enabledMethods, "tls")
	}

	return enabledMethods
}