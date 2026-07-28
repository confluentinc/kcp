package discover

import (
	"reflect"
	"testing"
)

func TestRegionsFromClusterArns(t *testing.T) {
	t.Run("distinct regions preserving order", func(t *testing.T) {
		got, err := regionsFromClusterArns([]string{
			"arn:aws:kafka:us-east-1:111:cluster/a/uuid",
			"arn:aws:kafka:eu-west-1:111:cluster/b/uuid",
			"arn:aws:kafka:us-east-1:111:cluster/c/uuid",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"us-east-1", "eu-west-1"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("malformed ARN errors", func(t *testing.T) {
		_, err := regionsFromClusterArns([]string{"not-an-arn"})
		if err == nil {
			t.Error("expected error for malformed ARN, got nil")
		}
	})
}

func TestFilterArnsToDiscover(t *testing.T) {
	regionArns := []string{
		"arn:aws:kafka:us-east-1:111:cluster/a/uuid",
		"arn:aws:kafka:us-east-1:111:cluster/b/uuid",
	}

	t.Run("empty target returns all", func(t *testing.T) {
		got := filterArnsToDiscover(regionArns, nil)
		if !reflect.DeepEqual(got, regionArns) {
			t.Errorf("got %v, want all %v", got, regionArns)
		}
	})

	t.Run("intersection only", func(t *testing.T) {
		got := filterArnsToDiscover(regionArns, []string{
			"arn:aws:kafka:us-east-1:111:cluster/b/uuid",
			"arn:aws:kafka:eu-west-1:111:cluster/z/uuid", // not in this region
		})
		want := []string{"arn:aws:kafka:us-east-1:111:cluster/b/uuid"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}
