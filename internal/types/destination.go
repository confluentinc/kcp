package types

import "fmt"

// ----- Confluent Cloud destination type -----

// DestinationType identifies which Confluent Cloud variant a migration targets.
// It gates linking-based create-asset paths that are unavailable on Confluent
// Cloud for Government (Cluster Linking, Schema Linking).
type DestinationType string

const (
	// DestinationCC is commercial (Standard) Confluent Cloud.
	DestinationCC DestinationType = "cc"
	// DestinationCCGov is Confluent Cloud for Government.
	DestinationCCGov DestinationType = "cc-gov"
)

func (d DestinationType) IsValid() bool {
	switch d {
	case DestinationCC, DestinationCCGov:
		return true
	default:
		return false
	}
}

// IsGov reports whether the destination is Confluent Cloud for Government.
func (d DestinationType) IsGov() bool {
	return d == DestinationCCGov
}

// ToDestinationType parses a CLI/declaration value into a DestinationType. An
// empty or unrecognised value returns an error naming the allowed values so
// callers can surface it directly.
func ToDestinationType(input string) (DestinationType, error) {
	d := DestinationType(input)
	if !d.IsValid() {
		return "", fmt.Errorf("invalid value %q: must be one of %s, %s", input, DestinationCC, DestinationCCGov)
	}
	return d, nil
}
