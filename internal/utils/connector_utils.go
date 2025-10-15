package utils

import "fmt"

func InferPluginName(connectorClass string) (string, error) {
	switch connectorClass {
	case "io.confluent.kafka.connect.datagen.DatagenConnector":
		return "DatagenSource", nil
	case "io.confluent.connect.s3.S3SinkConnector":
		return "S3_SINK", nil
	}

	return "", fmt.Errorf("unknown or unsupported connector class: %s", connectorClass)
}
