package types

// SourceType represents the type of Kafka source
type SourceType string

const (
	SourceTypeMSK SourceType = "msk"
	SourceTypeOSK SourceType = "osk"
)
