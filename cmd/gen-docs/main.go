// Command gen-docs regenerates the per-command markdown reference under
// docs/command-reference/ from the Cobra command tree. It is not a user-facing
// kcp subcommand — invoke via `make docs-gen`.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/confluentinc/kcp/cmd"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

const iamAnnotationKey = "aws_iam_permissions"

func main() {
	outDir := flag.String("out", "docs/command-reference", "output directory for generated markdown")
	flag.Parse()

	if err := os.RemoveAll(*outDir); err != nil {
		die(err)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		die(err)
	}

	root := cmd.RootCmd
	root.DisableAutoGenTag = true

	frontmatter := func(filename string) string {
		base := strings.TrimSuffix(filepath.Base(filename), ".md")
		title := strings.ReplaceAll(base, "_", " ")
		return fmt.Sprintf("---\ntitle: %s\n---\n\n", title)
	}

	linkHandler := func(name string) string { return name }

	if err := doc.GenMarkdownTreeCustom(root, *outDir, frontmatter, linkHandler); err != nil {
		die(err)
	}

	if err := walkAndInjectIAM(root, *outDir); err != nil {
		die(err)
	}

	fmt.Printf("gen-docs: wrote command reference to %s\n", *outDir)
}

func walkAndInjectIAM(c *cobra.Command, outDir string) error {
	if !c.Hidden {
		if perms := strings.TrimSpace(c.Annotations[iamAnnotationKey]); perms != "" {
			path := filepath.Join(outDir, mdFilename(c))
			if err := injectIAMSection(path, perms); err != nil {
				return fmt.Errorf("inject %s: %w", path, err)
			}
		}
	}
	for _, sub := range c.Commands() {
		if err := walkAndInjectIAM(sub, outDir); err != nil {
			return err
		}
	}
	return nil
}

func mdFilename(c *cobra.Command) string {
	parts := strings.Fields(c.CommandPath())
	return strings.Join(parts, "_") + ".md"
}

// injectIAMSection inserts an "## AWS IAM Permissions" section immediately
// before the generated "### SEE ALSO" block. If SEE ALSO is absent (e.g. no
// parent cross-reference), the section is appended at the end.
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
