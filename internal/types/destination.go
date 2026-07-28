package types

import (
	"fmt"
	"strings"
)

// ----- Confluent Cloud destination type -----

// DestinationType identifies which Confluent Cloud variant a migration targets.
// It gates linking-based create-asset paths that are unavailable on Confluent
// Cloud for Government (Cluster Linking, Schema Linking).
type DestinationType string

const (
	// DestinationCommercial is commercial (Standard) Confluent Cloud.
	DestinationCommercial DestinationType = "commercial"
	// DestinationGovernment is Confluent Cloud for Government.
	DestinationGovernment DestinationType = "government"
)

func (d DestinationType) IsValid() bool {
	switch d {
	case DestinationCommercial, DestinationGovernment:
		return true
	default:
		return false
	}
}

// IsGov reports whether the destination is Confluent Cloud for Government.
func (d DestinationType) IsGov() bool {
	return d == DestinationGovernment
}

// ToDestinationType parses a CLI/declaration value into a DestinationType.
// Matching is case-insensitive — the input is lowercased before validation and
// the returned value is the canonical lowercase form. Surrounding whitespace is
// not trimmed, so padded values are rejected. An empty or unrecognised value
// returns an error naming the allowed values so callers can surface it directly.
func ToDestinationType(input string) (DestinationType, error) {
	d := DestinationType(strings.ToLower(input))
	if !d.IsValid() {
		return "", fmt.Errorf("invalid value %q: must be one of %s, %s", input, DestinationCommercial, DestinationGovernment)
	}
	return d, nil
}
