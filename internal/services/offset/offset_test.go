package offset

import (
	"testing"
)

func TestSortedPartitionIDs(t *testing.T) {
	src := map[int32]int64{0: 100, 2: 200, 4: 400}
	dst := map[int32]int64{1: 50, 2: 150, 3: 300}

	got := SortedPartitionIDs(src, dst)
	want := []int32{0, 1, 2, 3, 4}

	if len(got) != len(want) {
		t.Fatalf("SortedPartitionIDs: got %d IDs, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("SortedPartitionIDs[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestSortedPartitionIDs_Empty(t *testing.T) {
	got := SortedPartitionIDs(nil, nil)
	if len(got) != 0 {
		t.Fatalf("SortedPartitionIDs(nil, nil): got %d IDs, want 0", len(got))
	}
}

func TestComputeTotalLag(t *testing.T) {
	src := map[int32]int64{0: 1000, 1: 2000, 2: 3000}
	dst := map[int32]int64{0: 900, 1: 2000, 2: 2500}

	got := ComputeTotalLag(src, dst)
	// partition 0: 1000-900=100, partition 1: 0, partition 2: 3000-2500=500
	var want int64 = 600

	if got != want {
		t.Errorf("ComputeTotalLag = %d, want %d", got, want)
	}
}

func TestComputeTotalLag_MissingDestPartition(t *testing.T) {
	src := map[int32]int64{0: 1000, 1: 2000, 2: 3000}
	dst := map[int32]int64{0: 1000}

	got := ComputeTotalLag(src, dst)
	// partition 0: 0, partition 1: 2000 (missing), partition 2: 3000 (missing)
	var want int64 = 5000

	if got != want {
		t.Errorf("ComputeTotalLag = %d, want %d", got, want)
	}
}

func TestComputeTotalLag_DstAhead(t *testing.T) {
	// Edge case: destination is ahead of source (should not add negative lag)
	src := map[int32]int64{0: 100}
	dst := map[int32]int64{0: 200}

	got := ComputeTotalLag(src, dst)
	var want int64 = 0

	if got != want {
		t.Errorf("ComputeTotalLag (dst ahead) = %d, want %d", got, want)
	}
}
