package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/iampolicy"
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
	outDir := "docs/assets/command-reference"

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
	m := buildLinkMap(root, "docs/assets/command-reference")

	checks := map[string]string{
		"kcp.md":                                 filepath.Join("docs/assets/command-reference", "index.md"),
		"kcp_discover.md":                        filepath.Join("docs/assets/command-reference", "discover.md"),
		"kcp_scan.md":                            filepath.Join("docs/assets/command-reference", "scan", "index.md"),
		"kcp_scan_clusters.md":                   filepath.Join("docs/assets/command-reference", "scan", "clusters.md"),
		"kcp_create-asset_migrate-acls_kafka.md": filepath.Join("docs/assets/command-reference", "create-asset", "migrate-acls", "kafka.md"),
	}
	for cobraName, want := range checks {
		if got := m[cobraName]; got != want {
			t.Errorf("linkMap[%q] = %q, want %q", cobraName, got, want)
		}
	}
}

// buildPruningTree builds a tree exercising the emit/walkAndInjectIAM pruning
// predicate: one visible leaf, one hidden leaf, one additional-help-topic
// leaf. `leaf` carries an IAM annotation so the injection path is triggered
// on exactly one file.
func buildPruningTree() *cobra.Command {
	root := &cobra.Command{Use: "kcp", Run: func(*cobra.Command, []string) {}}

	leaf := &cobra.Command{
		Use: "leaf",
		Run: func(*cobra.Command, []string) {},
		Annotations: map[string]string{
			iampolicy.AnnotationKey: "```json\n{\"Version\":\"leaf-policy\"}\n```\n",
		},
	}
	root.AddCommand(leaf)

	hidden := &cobra.Command{
		Use:    "hidden",
		Hidden: true,
		Run:    func(*cobra.Command, []string) {},
		Annotations: map[string]string{
			iampolicy.AnnotationKey: "should-not-appear",
		},
	}
	root.AddCommand(hidden)

	helpTopic := &cobra.Command{
		Use: "helptopic",
		Annotations: map[string]string{
			iampolicy.AnnotationKey: "should-not-appear",
		},
		// No Run and no RunE → cobra treats this as an additional-help-topic
		// command (IsAdditionalHelpTopicCommand() returns true).
	}
	root.AddCommand(helpTopic)

	// Leaf with an annotation but no SEE ALSO so we exercise the fallback
	// append path in injectIAMSection. Give it no siblings to keep cobra
	// from generating a SEE ALSO block for its parent.
	standalone := &cobra.Command{
		Use: "standalone",
		Run: func(*cobra.Command, []string) {},
		Annotations: map[string]string{
			iampolicy.AnnotationKey: "```json\n{\"Version\":\"standalone-policy\"}\n```\n",
		},
	}
	standaloneParent := &cobra.Command{Use: "group", Run: func(*cobra.Command, []string) {}}
	standaloneParent.AddCommand(standalone)
	root.AddCommand(standaloneParent)

	return root
}

// TestEmitPrunesHiddenAndHelpTopicCommands verifies that `emit` does not
// write files for Hidden subcommands or additional-help-topic commands —
// the predicate walkAndInjectIAM relies on to stay consistent with emit.
func TestEmitPrunesHiddenAndHelpTopicCommands(t *testing.T) {
	root := buildPruningTree()
	outDir := t.TempDir()
	linkMap := buildLinkMap(root, outDir)

	if err := emit(root, outDir, linkMap); err != nil {
		t.Fatalf("emit: %v", err)
	}

	mustExist := []string{
		filepath.Join(outDir, "index.md"),
		filepath.Join(outDir, "leaf.md"),
		filepath.Join(outDir, "group", "index.md"),
		filepath.Join(outDir, "group", "standalone.md"),
	}
	for _, p := range mustExist {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to be written, got %v", p, err)
		}
	}

	mustNotExist := []string{
		filepath.Join(outDir, "hidden.md"),
		filepath.Join(outDir, "helptopic.md"),
	}
	for _, p := range mustNotExist {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s to be absent, got err=%v", p, err)
		}
	}
}

// TestWalkAndInjectIAMInsertsBeforeSeeAlso verifies the happy path:
// a leaf with both an IAM annotation and a SEE ALSO block gets the
// `### AWS IAM Permissions` section inserted immediately before SEE ALSO.
func TestWalkAndInjectIAMInsertsBeforeSeeAlso(t *testing.T) {
	root := buildPruningTree()
	outDir := t.TempDir()
	linkMap := buildLinkMap(root, outDir)

	if err := emit(root, outDir, linkMap); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := walkAndInjectIAM(root, outDir); err != nil {
		t.Fatalf("walkAndInjectIAM: %v", err)
	}

	leafPath := filepath.Join(outDir, "leaf.md")
	b, err := os.ReadFile(leafPath)
	if err != nil {
		t.Fatalf("read %s: %v", leafPath, err)
	}
	content := string(b)

	iamIdx := strings.Index(content, "### AWS IAM Permissions")
	seeAlsoIdx := strings.Index(content, "### SEE ALSO")
	if iamIdx < 0 {
		t.Fatalf("expected ### AWS IAM Permissions section in %s:\n%s", leafPath, content)
	}
	if seeAlsoIdx < 0 {
		t.Fatalf("expected ### SEE ALSO section in %s (cobra should emit one for a child of root):\n%s", leafPath, content)
	}
	if iamIdx >= seeAlsoIdx {
		t.Errorf("expected IAM section before SEE ALSO; iamIdx=%d, seeAlsoIdx=%d", iamIdx, seeAlsoIdx)
	}
	if !strings.Contains(content, "leaf-policy") {
		t.Errorf("expected leaf-policy payload in injected section:\n%s", content)
	}
}

// TestWalkAndInjectIAMAppendsWhenSeeAlsoAbsent covers the fallback path in
// injectIAMSection: when cobra does not emit a SEE ALSO block, the IAM
// section lands at the end of the file.
func TestWalkAndInjectIAMAppendsWhenSeeAlsoAbsent(t *testing.T) {
	// Build a minimal tree: single leaf under root with an annotation but
	// no sibling → cobra still renders SEE ALSO for a parent link. To hit
	// the no-SEE-ALSO branch we drive injectIAMSection directly with a
	// file we author without a SEE ALSO block.
	outDir := t.TempDir()
	path := filepath.Join(outDir, "no-seealso.md")
	existing := "---\ntitle: kcp no-seealso\n---\n\n# body\n\nSome prose.\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := injectIAMSection(path, "```json\n{\"policy\":true}\n```\n"); err != nil {
		t.Fatalf("injectIAMSection: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "### AWS IAM Permissions") {
		t.Fatalf("expected injected section in output:\n%s", out)
	}
	if idx := strings.Index(out, "### AWS IAM Permissions"); idx <= strings.Index(out, "Some prose.") {
		t.Errorf("expected injected section after existing prose (append fallback); got idx=%d", idx)
	}
	if strings.Contains(out, "### SEE ALSO") {
		t.Errorf("unexpected SEE ALSO in seeded fixture: %s", out)
	}
}

// TestWalkAndInjectIAMRootGuardSkipsHiddenEntry documents the root-entry
// guard: if a future caller hands walkAndInjectIAM a non-root hidden or
// additional-help-topic command directly, it must return nil without
// touching the filesystem (no file was emitted for that command).
func TestWalkAndInjectIAMRootGuardSkipsHiddenEntry(t *testing.T) {
	root := buildPruningTree()
	outDir := t.TempDir()
	// Don't emit anything — the guard should fire before any file IO.

	var hidden *cobra.Command
	for _, sub := range root.Commands() {
		if sub.Name() == "hidden" {
			hidden = sub
			break
		}
	}
	if hidden == nil {
		t.Fatal("setup: hidden subcommand not found")
	}

	if err := walkAndInjectIAM(hidden, outDir); err != nil {
		t.Fatalf("walkAndInjectIAM on hidden entry returned %v, want nil", err)
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read outDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("walkAndInjectIAM on hidden entry wrote files: %v", entries)
	}
}
