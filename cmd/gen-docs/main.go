// Command gen-docs regenerates the per-command markdown reference under
// docs/assets/command-reference/ from the Cobra command tree. It is not a user-facing
// kcp subcommand — invoke via `make docs-gen`.
//
// Parent commands (and the root) are emitted as <path>/index.md; leaves are
// emitted as <parent-path>/<name>.md. SEE ALSO links are rewritten so they
// resolve correctly inside the nested layout. This lets mkdocs-awesome-pages
// infer the sidebar from the filesystem instead of requiring manual nav
// maintenance in mkdocs.yml.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/confluentinc/kcp/cmd"
	"github.com/confluentinc/kcp/internal/services/iampolicy"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// commandOrder pins the sidebar order of subcommands under a given parent in
// the generated `.pages` files. Keys are full command paths; values are child
// command names in display order. Children of an ordered parent that are not
// listed here fall in at the end alphabetically (via awesome-pages' `...`
// token), so a newly added subcommand still surfaces in the sidebar without
// touching this map. Parents not present here keep the awesome-pages default
// (alphabetical).
var commandOrder = map[string][]string{
	"kcp": {
		"discover",
		"scan",
		"report",
		"create-asset",
		"migration",
		"ui",
		"docs",
		"update",
		"version",
	},
	"kcp create-asset": {
		"bastion-host",
		"target-infra",
		"migration-infra",
		"migrate-topics",
		"migrate-schemas",
		"migrate-acls",
		"migrate-connectors",
	},
	"kcp migration": {
		"init",
		"execute",
		"lag-check",
		"list",
	},
	"kcp scan": {
		"clusters",
		"client-inventory",
		"schema-registry",
	},
}

func main() {
	outDir := flag.String("out", "docs/assets/command-reference", "output directory for generated markdown")
	flag.Parse()

	if err := os.RemoveAll(*outDir); err != nil {
		die(err)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		die(err)
	}

	root := cmd.RootCmd
	root.DisableAutoGenTag = true

	linkMap := buildLinkMap(root, *outDir)

	if err := emit(root, *outDir, linkMap); err != nil {
		die(err)
	}
	if err := walkAndInjectIAM(root, *outDir); err != nil {
		die(err)
	}

	fmt.Printf("gen-docs: wrote command reference to %s\n", *outDir)
}

// buildPagesContent renders a .pages file for a parent command. If
// commandOrder pins the parent's children, emit an explicit nav: block (with
// awesome-pages' `...` rest-token at the end so unlisted children — e.g. a
// newly added subcommand — still surface). Otherwise emit just the title and
// let awesome-pages fall back to alphabetical.
func buildPagesContent(c *cobra.Command, title string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "title: %s\n", title)

	order, ok := commandOrder[c.CommandPath()]
	if !ok {
		return b.String()
	}

	childByName := map[string]*cobra.Command{}
	for _, sub := range c.Commands() {
		if !sub.IsAvailableCommand() || sub.IsAdditionalHelpTopicCommand() {
			continue
		}
		childByName[sub.Name()] = sub
	}

	b.WriteString("nav:\n")
	for _, name := range order {
		sub, ok := childByName[name]
		if !ok {
			continue
		}
		// Parents render as <name>/index.md (a directory in the nav); leaves
		// render as <name>.md.
		if sub.HasAvailableSubCommands() {
			fmt.Fprintf(&b, "  - %s\n", name)
		} else {
			fmt.Fprintf(&b, "  - %s.md\n", name)
		}
	}
	b.WriteString("  - ...\n")
	return b.String()
}

// outputPath returns the markdown file path for a command within outDir.
// A command with visible subcommands (or the root) gets <path>/index.md;
// a leaf gets <parent-path>/<name>.md.
func outputPath(c *cobra.Command, outDir string) string {
	segs := strings.Fields(c.CommandPath())
	tail := segs[1:] // drop the root program name ("kcp")

	if !c.HasParent() || c.HasAvailableSubCommands() {
		parts := append([]string{outDir}, tail...)
		parts = append(parts, "index.md")
		return filepath.Join(parts...)
	}
	parts := append([]string{outDir}, tail[:len(tail)-1]...)
	parts = append(parts, tail[len(tail)-1]+".md")
	return filepath.Join(parts...)
}

// cobraBasename is the default filename Cobra embeds in SEE ALSO links for
// a given command: `<CommandPath with spaces→_>.md`. linkHandler receives this.
func cobraBasename(c *cobra.Command) string {
	return strings.ReplaceAll(c.CommandPath(), " ", "_") + ".md"
}

// buildLinkMap maps each command's Cobra default SEE ALSO filename to the
// real output path we'll write, so linkHandler can rewrite links from any
// page to any other without knowing the caller's location beforehand.
func buildLinkMap(root *cobra.Command, outDir string) map[string]string {
	m := map[string]string{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		m[cobraBasename(c)] = outputPath(c, outDir)
		for _, sub := range c.Commands() {
			if !sub.IsAvailableCommand() || sub.IsAdditionalHelpTopicCommand() {
				continue
			}
			walk(sub)
		}
	}
	walk(root)
	return m
}

func emit(c *cobra.Command, outDir string, linkMap map[string]string) error {
	path := outputPath(c, outDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if _, err := fmt.Fprintf(f, "---\ntitle: %s\n---\n\n", c.CommandPath()); err != nil {
		return err
	}

	// For parent commands (anything rendered as index.md), emit a sibling
	// `.pages` so mkdocs-material's navigation.indexes picks up the full
	// command path (e.g. "kcp create-asset migrate-acls") as the section
	// label instead of a titleized directory name ("Migrate acls"). The
	// root gets "Command Reference" so the top-nav tab doesn't read "kcp"
	// (awesome-pages: a directory's own .pages title beats any override
	// from the parent nav, so this has to be fixed at the source).
	if filepath.Base(path) == "index.md" {
		title := c.CommandPath()
		if !c.HasParent() {
			title = "Command Reference"
		}
		pagesPath := filepath.Join(filepath.Dir(path), ".pages")
		pagesContent := buildPagesContent(c, title)
		if err := os.WriteFile(pagesPath, []byte(pagesContent), 0o644); err != nil {
			return err
		}
	}

	linkHandler := func(cobraName string) string {
		target, ok := linkMap[cobraName]
		if !ok {
			return cobraName
		}
		rel, err := filepath.Rel(filepath.Dir(path), target)
		if err != nil {
			return cobraName
		}
		return filepath.ToSlash(rel)
	}

	if err := doc.GenMarkdownCustom(c, f, linkHandler); err != nil {
		return err
	}

	for _, sub := range c.Commands() {
		if !sub.IsAvailableCommand() || sub.IsAdditionalHelpTopicCommand() {
			continue
		}
		if err := emit(sub, outDir, linkMap); err != nil {
			return err
		}
	}
	return nil
}

// walkAndInjectIAM mirrors emit's pruning rule (!IsAvailableCommand() ||
// IsAdditionalHelpTopicCommand()) so it never tries to inject into a file
// emit didn't write. Hidden subtrees and help-topic commands are skipped
// for themselves AND for their descendants.
func walkAndInjectIAM(c *cobra.Command, outDir string) error {
	// Root has no parent and emit always writes it, so let the root through
	// unconditionally; for any non-root command, mirror emit's own pruning
	// so a future caller that hands us a hidden or help-topic subtree can't
	// try to inject into a file emit never wrote.
	if c.HasParent() && (!c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand()) {
		return nil
	}

	if perms := strings.TrimSpace(c.Annotations[iampolicy.AnnotationKey]); perms != "" {
		path := outputPath(c, outDir)
		if err := injectIAMSection(path, perms); err != nil {
			return fmt.Errorf("inject %s: %w", path, err)
		}
	}
	for _, sub := range c.Commands() {
		if !sub.IsAvailableCommand() || sub.IsAdditionalHelpTopicCommand() {
			continue
		}
		if err := walkAndInjectIAM(sub, outDir); err != nil {
			return err
		}
	}
	return nil
}

// injectIAMSection inserts an "### AWS IAM Permissions" section immediately
// before the generated "### SEE ALSO" block. If SEE ALSO is absent (no parent
// cross-reference), the section is appended at the end.
func injectIAMSection(path, perms string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(b)
	section := "### AWS IAM Permissions\n\n" + strings.TrimRight(perms, "\n") + "\n\n"

	const marker = "### SEE ALSO"
	if i := strings.Index(content, marker); i >= 0 {
		content = content[:i] + section + content[i:]
	} else {
		content = strings.TrimRight(content, "\n") + "\n\n" + section
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "gen-docs:", err)
	os.Exit(1)
}
