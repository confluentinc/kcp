package lagcheck

import (
	"testing"
	"unicode/utf8"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
)

func TestFormatLag_Zero(t *testing.T) {
	got := formatLag(0)
	if got != "0" {
		t.Errorf("formatLag(0) = %q, want %q", got, "0")
	}
}

func TestFormatLag_Small(t *testing.T) {
	got := formatLag(999)
	if got != "999" {
		t.Errorf("formatLag(999) = %q, want %q", got, "999")
	}
}

func TestFormatLag_Thousands(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{1000, "1,000"},
		{21655, "21,655"},
	}
	for _, tc := range tests {
		got := formatLag(tc.input)
		if got != tc.want {
			t.Errorf("formatLag(%d) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestFormatLag_Millions(t *testing.T) {
	got := formatLag(1000000)
	if got != "1,000,000" {
		t.Errorf("formatLag(1000000) = %q, want %q", got, "1,000,000")
	}
}

func TestTotalLag(t *testing.T) {
	topic := clusterlink.MirrorTopic{
		MirrorTopicName: "test-topic",
		MirrorStatus:    "ACTIVE",
		MirrorLags: []clusterlink.MirrorLag{
			{Partition: 0, Lag: 100},
			{Partition: 1, Lag: 200},
			{Partition: 2, Lag: 300},
		},
	}
	got := totalLag(topic)
	if got != 600 {
		t.Errorf("totalLag() = %d, want 600", got)
	}
}

func TestTotalLag_Empty(t *testing.T) {
	topic := clusterlink.MirrorTopic{
		MirrorTopicName: "empty-topic",
		MirrorStatus:    "ACTIVE",
		MirrorLags:      []clusterlink.MirrorLag{},
	}
	got := totalLag(topic)
	if got != 0 {
		t.Errorf("totalLag() = %d, want 0", got)
	}
}

func TestRenderSparkline_Empty(t *testing.T) {
	got := renderSparkline([]int{})
	if got != "-" {
		t.Errorf("renderSparkline(empty) = %q, want %q", got, "-")
	}
}

func TestRenderSparkline_AllZeros(t *testing.T) {
	got := renderSparkline([]int{0, 0, 0})
	want := "▁▁▁"
	if got != want {
		t.Errorf("renderSparkline(all zeros) = %q, want %q", got, want)
	}
}

func TestRenderSparkline_Ascending(t *testing.T) {
	got := renderSparkline([]int{0, 50, 100})

	// Should have exactly 3 runes
	runeCount := utf8.RuneCountInString(got)
	if runeCount != 3 {
		t.Fatalf("renderSparkline(ascending) has %d runes, want 3", runeCount)
	}

	runes := []rune(got)
	lowest := sparkBlocks[0]                   // ▁
	highest := sparkBlocks[len(sparkBlocks)-1] // █

	if runes[0] != lowest {
		t.Errorf("first rune = %c, want %c (lowest block)", runes[0], lowest)
	}
	if runes[2] != highest {
		t.Errorf("last rune = %c, want %c (highest block)", runes[2], highest)
	}
}

func TestRenderSparkline_SingleValue(t *testing.T) {
	got := renderSparkline([]int{100})
	want := string(sparkBlocks[len(sparkBlocks)-1]) // █
	if got != want {
		t.Errorf("renderSparkline(single) = %q, want %q", got, want)
	}
}
