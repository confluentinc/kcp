package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// buildTree constructs a small command tree mirroring the kcp shape so we can
// exercise outputPath against root / parent / leaf / deep-leaf cases.
func buildTree() *cobra.Command {
	root := &cobra.Command{Use: "kcp", Run: func(*cobra.Command, []string) {}}

	discover := &cobra.Command{Use: "discover", Run: func(*cobra.Command, []string) {}}
	root.AddCommand(discover)

	scan := &cobra.Command{Use: "scan", Run: func(*cobra.Command, []string) {}}
	clusters := &cobra.Command{Use: "clusters", Run: func(*cobra.Command, []string) {}}
	scan.AddCommand(clusters)
	root.AddCommand(scan)

	createAsset := &cobra.Command{Use: "create-asset", Run: func(*cobra.Command, []string) {}}
	migrateAcls := &cobra.Command{Use: "migrate-acls", Run: func(*cobra.Command, []string) {}}
	kafka := &cobra.Command{Use: "kafka", Run: func(*cobra.Command, []string) {}}
	migrateAcls.AddCommand(kafka)
	createAsset.AddCommand(migrateAcls)
	root.AddCommand(createAsset)

	return root
}

func findCmd(t *testing.T, root *cobra.Command, path string) *cobra.Command {
	t.Helper()
	parts := strings.Fields(path)
	if len(parts) == 0 || parts[0] != root.Name() {
		t.Fatalf("path %q must start with root name %q", path, root.Name())
	}
	cur := root
	for _, name := range parts[1:] {
		var next *cobra.Command
		for _, sub := range cur.Commands() {
			if sub.Name() == name {
				next = sub
				break
			}
		}
		if next == nil {
			t.Fatalf("command %q not found under %q", name, cur.Name())
		}
		cur = next
	}
	return cur
}

func TestOutputPath(t *testing.T) {
	root := buildTree()
	outDir := "docs/command-reference"

	cases := []struct {
		name    string
		cmdPath string
		want    string
	}{
		{"root (has children) → index.md", "kcp", filepath.Join(outDir, "index.md")},
		{"leaf directly under root", "kcp discover", filepath.Join(outDir, "discover.md")},
		{"parent with children → index.md", "kcp scan", filepath.Join(outDir, "scan", "index.md")},
		{"leaf under parent", "kcp scan clusters", filepath.Join(outDir, "scan", "clusters.md")},
		{"deep parent → index.md", "kcp create-asset migrate-acls", filepath.Join(outDir, "create-asset", "migrate-acls", "index.md")},
		{"deep leaf", "kcp create-asset migrate-acls kafka", filepath.Join(outDir, "create-asset", "migrate-acls", "kafka.md")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := findCmd(t, root, tc.cmdPath)
			got := outputPath(c, outDir)
			if got != tc.want {
				t.Errorf("outputPath(%q) = %q, want %q", tc.cmdPath, got, tc.want)
			}
		})
	}
}

func TestCobraBasename(t *testing.T) {
	root := buildTree()
	c := findCmd(t, root, "kcp create-asset migrate-acls kafka")
	if got, want := cobraBasename(c), "kcp_create-asset_migrate-acls_kafka.md"; got != want {
		t.Errorf("cobraBasename = %q, want %q", got, want)
	}
}

func TestBuildLinkMapRewritesToRealPaths(t *testing.T) {
	root := buildTree()
	m := buildLinkMap(root, "docs/command-reference")

	checks := map[string]string{
		"kcp.md":                                 filepath.Join("docs/command-reference", "index.md"),
		"kcp_discover.md":                        filepath.Join("docs/command-reference", "discover.md"),
		"kcp_scan.md":                            filepath.Join("docs/command-reference", "scan", "index.md"),
		"kcp_scan_clusters.md":                   filepath.Join("docs/command-reference", "scan", "clusters.md"),
		"kcp_create-asset_migrate-acls_kafka.md": filepath.Join("docs/command-reference", "create-asset", "migrate-acls", "kafka.md"),
	}
	for cobraName, want := range checks {
		if got := m[cobraName]; got != want {
			t.Errorf("linkMap[%q] = %q, want %q", cobraName, got, want)
		}
	}
}
