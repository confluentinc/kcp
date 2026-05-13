package migrate_topics

import (
	"reflect"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
)

func TestValidateModeFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		mode            string
		clusterLinkName string
		wantErr         string
	}{
		{
			name:            "mirror with cluster-link-name is valid",
			mode:            "mirror",
			clusterLinkName: "msk-to-cc-link",
		},
		{
			name:    "missing mode errors with required-flag message",
			wantErr: "--mode is required",
		},
		{
			name:    "mirror without cluster-link-name is rejected",
			mode:    "mirror",
			wantErr: "--cluster-link-name is required when --mode mirror",
		},
		{
			name:            "new with cluster-link-name is rejected",
			mode:            "new",
			clusterLinkName: "msk-to-cc-link",
			wantErr:         "--cluster-link-name is not valid when --mode new",
		},
		{
			name: "new without cluster-link-name is valid",
			mode: "new",
		},
		{
			name:    "unknown mode value is rejected",
			mode:    "foo",
			wantErr: `invalid --mode: "foo"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateModeFlags(tt.mode, tt.clusterLinkName)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateModeFlags(%q, %q) returned unexpected error: %v", tt.mode, tt.clusterLinkName, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateModeFlags(%q, %q) expected error containing %q, got nil", tt.mode, tt.clusterLinkName, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateModeFlags(%q, %q) error = %q, want substring %q", tt.mode, tt.clusterLinkName, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestSelectTopics(t *testing.T) {
	t.Parallel()

	td := func(names ...string) []types.TopicDetails {
		out := make([]types.TopicDetails, len(names))
		for i, n := range names {
			out[i] = types.TopicDetails{Name: n}
		}
		return out
	}
	names := func(ts []types.TopicDetails) []string {
		out := make([]string, len(ts))
		for i, t := range ts {
			out[i] = t.Name
		}
		return out
	}

	internalCarveOut := []string{"__consumer_offsets"}

	tests := []struct {
		name     string
		input    []types.TopicDetails
		include  []string
		exclude  []string
		expected []string
	}{
		{
			name:     "include glob keeps matching topics",
			input:    td("orders.a", "orders.b", "events.x"),
			include:  []string{"orders.*"},
			expected: []string{"orders.a", "orders.b"},
		},
		{
			name:     "exclude glob drops matching topics",
			input:    td("orders.a", "orders.dlq"),
			exclude:  []string{"*.dlq"},
			expected: []string{"orders.a"},
		},
		{
			name:     "include then exclude — exclude wins",
			input:    td("orders.a", "orders.b", "orders.dlq", "events.x"),
			include:  []string{"orders.*"},
			exclude:  []string{"*.dlq"},
			expected: []string{"orders.a", "orders.b"},
		},
		{
			name:     "empty include defaults to all non-internal",
			input:    td("orders.a", "events.x"),
			expected: []string{"orders.a", "events.x"},
		},
		{
			name:     "internal topics dropped except __consumer_offsets",
			input:    td("__consumer_offsets", "__transaction_state", "orders.a"),
			expected: []string{"__consumer_offsets", "orders.a"},
		},
		{
			name:     "include patterns do not pull internal topics back in (except carve-out)",
			input:    td("__transaction_state", "__consumer_offsets", "orders.a"),
			include:  []string{"*"},
			expected: []string{"__consumer_offsets", "orders.a"},
		},
		{
			name:     "no topics in returns empty",
			input:    nil,
			expected: []string{},
		},
		{
			name:     "preserves input order",
			input:    td("z", "a", "m"),
			expected: []string{"z", "a", "m"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := selectTopics(tt.input, internalCarveOut, tt.include, tt.exclude)
			gotNames := names(got)
			if !reflect.DeepEqual(gotNames, tt.expected) {
				t.Errorf("selectTopics names = %v, want %v", gotNames, tt.expected)
			}
		})
	}

	t.Run("empty carve-out (new mode) drops __consumer_offsets", func(t *testing.T) {
		t.Parallel()
		got := selectTopics(
			td("__consumer_offsets", "__transaction_state", "orders.a"),
			nil,
			nil,
			nil,
		)
		gotNames := names(got)
		want := []string{"orders.a"}
		if !reflect.DeepEqual(gotNames, want) {
			t.Errorf("selectTopics with empty carve-out = %v, want %v (__consumer_offsets must not survive in new mode)", gotNames, want)
		}
	})
}

func TestNoMatchError(t *testing.T) {
	t.Parallel()

	td := func(names ...string) []types.TopicDetails {
		out := make([]types.TopicDetails, len(names))
		for i, n := range names {
			out[i] = types.TopicDetails{Name: n}
		}
		return out
	}

	t.Run("mentions patterns and candidate count when state has topics", func(t *testing.T) {
		t.Parallel()
		err := noMatchError(
			td("orders", "events", "__consumer_offsets", "__transaction_state"),
			[]string{"__consumer_offsets"},
			[]string{"demo_*"},
			nil,
		)
		msg := err.Error()
		if !strings.Contains(msg, "demo_*") {
			t.Errorf("error should mention the include pattern, got: %s", msg)
		}
		// Candidates: orders, events, __consumer_offsets (carve-out). __transaction_state dropped.
		if !strings.Contains(msg, "3 candidates") {
			t.Errorf("error should report 3 candidates (orders, events, __consumer_offsets), got: %s", msg)
		}
		// Error stays one-line — no per-topic dump.
		if strings.Count(msg, "\n") > 0 {
			t.Errorf("error should be single-line for readability with repo-wide error-doubling, got: %s", msg)
		}
	})

	t.Run("distinct message when state has no topics", func(t *testing.T) {
		t.Parallel()
		err := noMatchError(nil, []string{"__consumer_offsets"}, []string{"demo_*"}, nil)
		if !strings.Contains(err.Error(), "no topics to filter") {
			t.Errorf("error should point to empty state file, got: %s", err.Error())
		}
		if !strings.Contains(err.Error(), "kcp scan clusters") {
			t.Errorf("error should hint at scan clusters, got: %s", err.Error())
		}
	})
}

func TestSelectTopics_PreservesTopicDetails(t *testing.T) {
	t.Parallel()

	cfg := "compact"
	input := []types.TopicDetails{
		{Name: "orders", Partitions: 6, Configurations: map[string]*string{"cleanup.policy": &cfg}},
		{Name: "__transaction_state", Partitions: 50},
	}

	got := selectTopics(input, []string{"__consumer_offsets"}, nil, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 topic, got %d", len(got))
	}
	if got[0].Name != "orders" || got[0].Partitions != 6 {
		t.Errorf("expected orders with 6 partitions, got %+v", got[0])
	}
	if got[0].Configurations["cleanup.policy"] == nil || *got[0].Configurations["cleanup.policy"] != "compact" {
		t.Errorf("expected cleanup.policy=compact, got %v", got[0].Configurations)
	}
}
