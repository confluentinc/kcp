package iampolicy

import (
	"reflect"
	"strings"
	"testing"
)

func TestSortedUniqueDedupesAndSorts(t *testing.T) {
	got := sortedUnique([]string{"ec2:DescribeVpcs", "sts:GetCallerIdentity", "ec2:DescribeVpcs", "ec2:CreateSubnet"})
	want := []string{"ec2:CreateSubnet", "ec2:DescribeVpcs", "sts:GetCallerIdentity"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortedUnique mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestSortedUniqueEmptyReturnsEmptySlice(t *testing.T) {
	got := sortedUnique(nil)
	if got == nil || len(got) != 0 {
		t.Fatalf("expected empty non-nil slice, got %v", got)
	}
}

func TestUnionMergesFragments(t *testing.T) {
	a := []string{"ec2:CreateSubnet", "ec2:DeleteSubnet"}
	b := []string{"ec2:CreateSubnet", "sts:GetCallerIdentity"}
	got := Union(a, b)
	want := []string{"ec2:CreateSubnet", "ec2:DeleteSubnet", "sts:GetCallerIdentity"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Union mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestDifferenceExcludesBase(t *testing.T) {
	full := []string{"ec2:CreateSubnet", "ec2:DescribeAvailabilityZones", "sts:GetCallerIdentity"}
	base := []string{"sts:GetCallerIdentity", "ec2:CreateSubnet"}
	got := Difference(full, base)
	want := []string{"ec2:DescribeAvailabilityZones"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Difference mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestOverlapReportsSharedActions(t *testing.T) {
	base := []string{"sts:GetCallerIdentity", "ec2:CreateSubnet"}
	additions := []string{"ec2:CreateSubnet", "ec2:DescribeAvailabilityZones"}
	got := Overlap(base, additions)
	want := []string{"ec2:CreateSubnet"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Overlap mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestRenderHappyPath(t *testing.T) {
	out := Render(
		"Intro sentence.",
		[]string{"sts:GetCallerIdentity", "ec2:CreateSubnet"},
		[]Variant{
			{
				FlagHint:  "--cluster-type enterprise",
				Summary:   "Enterprise adds an AZ describe.",
				Additions: []string{"ec2:DescribeAvailabilityZones"},
			},
			{
				FlagHint: "--cluster-type dedicated",
				Summary:  "Dedicated reuses subnets.",
			},
		},
	)

	mustContain(t, out, "Intro sentence.")
	mustContain(t, out, "#### Base — always required")
	mustContain(t, out, `"ec2:CreateSubnet"`)
	mustContain(t, out, `"sts:GetCallerIdentity"`)
	mustContain(t, out, "#### Additional for `--cluster-type enterprise`")
	mustContain(t, out, "Enterprise adds an AZ describe.")
	mustContain(t, out, `"ec2:DescribeAvailabilityZones"`)
	mustContain(t, out, "#### Additional for `--cluster-type dedicated`")
	mustContain(t, out, "Dedicated reuses subnets.")
	mustContain(t, out, "_No additional permissions beyond the base._")

	// Emits the Version literal correctly (guards against a copy-paste of
	// the "20122012-10-17" typo from the captured notes).
	mustContain(t, out, `"Version": "2012-10-17"`)

	// Base block appears before the enterprise block.
	baseIdx := strings.Index(out, "#### Base")
	entIdx := strings.Index(out, "#### Additional for `--cluster-type enterprise`")
	if baseIdx < 0 || entIdx < 0 || baseIdx > entIdx {
		t.Fatalf("base block should precede variant blocks:\n%s", out)
	}
}

func TestRenderSortsAndDedupesActions(t *testing.T) {
	out := Render(
		"",
		[]string{"ec2:DescribeVpcs", "ec2:CreateSubnet", "ec2:DescribeVpcs", "ec2:AuthorizeSecurityGroupEgress"},
		nil,
	)
	// Actions should appear in alphabetical order, once each.
	idxAuth := strings.Index(out, `"ec2:AuthorizeSecurityGroupEgress"`)
	idxCreate := strings.Index(out, `"ec2:CreateSubnet"`)
	idxDescribeFirst := strings.Index(out, `"ec2:DescribeVpcs"`)
	idxDescribeSecond := strings.Index(out[idxDescribeFirst+1:], `"ec2:DescribeVpcs"`)

	if idxAuth <= 0 || idxAuth >= idxCreate || idxCreate >= idxDescribeFirst {
		t.Fatalf("actions not sorted:\n%s", out)
	}
	if idxDescribeSecond != -1 {
		t.Fatalf("duplicate action not deduped:\n%s", out)
	}
}

func mustContain(t *testing.T, s, needle string) {
	t.Helper()
	if !strings.Contains(s, needle) {
		t.Fatalf("expected output to contain %q, got:\n%s", needle, s)
	}
}
