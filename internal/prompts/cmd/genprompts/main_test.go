// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/soma-intelligence/open-health-sync/internal/prompts"
)

// TestCommittedFilesAreCurrent fails when a generated file is not what the
// generator writes. Fix it with: go run ./internal/prompts/cmd/genprompts
func TestCommittedFilesAreCurrent(t *testing.T) {
	for path, want := range Generate() {
		got, err := os.ReadFile(filepath.Join("..", "..", "..", "..", path))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is out of date: run `go run ./internal/prompts/cmd/genprompts`", path)
		}
	}
}

func TestGeneratedFilesHoldNoEmDash(t *testing.T) {
	for path, body := range Generate() {
		if bytes.ContainsRune(body, rune(0x2014)) {
			t.Errorf("%s holds an em dash", path)
		}
	}
}

// TestMCPDocListsEveryPrompt fails when docs/mcp.md misses a prompt of the
// library or lists one that is gone.
func TestMCPDocListsEveryPrompt(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "docs", "mcp.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	i := strings.Index(doc, "## Prompts")
	if i < 0 {
		t.Fatal("docs/mcp.md has no Prompts section")
	}
	listed := map[string]bool{}
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|").FindAllStringSubmatch(doc[i:], -1) {
		listed[m[1]] = true
	}
	for _, p := range prompts.Library {
		if !listed[p.Name] {
			t.Errorf("docs/mcp.md does not list the prompt %s", p.Name)
		}
		delete(listed, p.Name)
	}
	for n := range listed {
		t.Errorf("docs/mcp.md lists %s, which is not in the library", n)
	}
}
