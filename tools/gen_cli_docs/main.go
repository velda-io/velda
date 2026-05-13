// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// gen_cli_docs generates Markdown reference documentation for the Velda CLI
// from the cobra command tree and writes it to docs/reference/cli/.
//
// Usage:
//
//	go run ./tools/gen_cli_docs [output-dir]
//
// If output-dir is omitted it defaults to docs/reference/cli.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	// Import the cmd package to register all commands onto the root command.
	"velda.io/velda/client/cmd"
)

func main() {
	outDir := "docs/reference/cli"
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating output directory %s: %v\n", outDir, err)
		os.Exit(1)
	}

	// Initialise config flags so the root command is consistent, but skip
	// the actual config file loading that happens in PersistentPreRun.
	root := cmd.RootCmd

	// Strip PersistentPreRun so that cobra/doc traversal does not execute it.
	stripPreRun(root)

	// Disable completion-command auto-generation noise in the docs.
	root.CompletionOptions.DisableDefaultCmd = true
	root.DisableAutoGenTag = true

	// Provide a custom link handler so that generated links use relative paths
	// expected by MkDocs (e.g. velda_instance_create.md instead of the default).
	linkHandler := func(name string) string {
		// cobra/doc produces names like "velda_instance_create.md"
		return name
	}

	filePrepender := func(filename string) string {
		// Strip directory and extension to derive the command path shown as the
		// YAML front-matter title.
		base := filepath.Base(filename)
		base = strings.TrimSuffix(base, ".md")
		title := strings.ReplaceAll(base, "_", " ")
		return fmt.Sprintf("---\ntitle: \"%s\"\n---\n\n", title)
	}

	if err := doc.GenMarkdownTreeCustom(root, outDir, filePrepender, linkHandler); err != nil {
		fmt.Fprintf(os.Stderr, "error generating docs: %v\n", err)
		os.Exit(1)
	}

	if err := writeSummary(root, filepath.Join(outDir, "summary.md")); err != nil {
		fmt.Fprintf(os.Stderr, "error generating summary: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("CLI reference docs written to %s/\n", outDir)
	fmt.Printf("CLI literate nav written to %s\n", filepath.Join(outDir, "summary.md"))
}

// stripPreRun removes PersistentPreRun from cmd and all its descendants so
// that cobra/doc traversal does not attempt to load config files.
func stripPreRun(c *cobra.Command) {
	c.PersistentPreRun = nil
	c.PersistentPreRunE = nil
	c.PreRun = nil
	c.PreRunE = nil
	for _, sub := range c.Commands() {
		stripPreRun(sub)
	}
}

// writeSummary writes a mkdocs-literate-nav summary file for the command tree.
func writeSummary(root *cobra.Command, summaryPath string) error {
	var b strings.Builder
	b.WriteString("# CLI Navigation\n\n")
	for _, sub := range root.Commands() {
		if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
			continue
		}
		writeSummaryNode(&b, sub, "")
	}

	return os.WriteFile(summaryPath, []byte(b.String()), 0o644)
}

func writeSummaryNode(b *strings.Builder, c *cobra.Command, indent string) {
	if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
		return
	}

	filename := commandFilename(c)
	b.WriteString(fmt.Sprintf("%s* [%s](%s)\n", indent, c.Name(), filename))

	for _, sub := range c.Commands() {
		if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
			continue
		}
		writeSummaryNode(b, sub, indent+"    ")
	}
}

// commandFilename returns the expected filename cobra/doc produces for cmd.
func commandFilename(c *cobra.Command) string {
	parts := []string{}
	for p := c; p != nil; p = p.Parent() {
		parts = append([]string{p.Name()}, parts...)
	}
	return strings.Join(parts, "_") + ".md"
}
