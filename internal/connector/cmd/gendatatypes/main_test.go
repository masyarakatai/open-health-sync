// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	_ "github.com/soma-intelligence/open-health-sync/connectors/all"
	"github.com/soma-intelligence/open-health-sync/internal/connector"
)

// TestCommittedDocIsCurrent fails when the generated section of
// docs/connectors/README.md is not what the generator writes. Fix it with:
// go run ./internal/connector/cmd/gendatatypes
func TestCommittedDocIsCurrent(t *testing.T) {
	block, err := Block()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile("../../../../docs/connectors/README.md")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Splice(doc, block)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, doc) {
		t.Fatal("docs/connectors/README.md data types section is out of date: run `go run ./internal/connector/cmd/gendatatypes`")
	}
}

// TestBlockCoversEveryRegisteredConnector fails when the generated table
// leaves out a connector the registry serves, so the section cannot fall
// behind the code without its test noticing.
func TestBlockCoversEveryRegisteredConnector(t *testing.T) {
	block, err := Block()
	if err != nil {
		t.Fatal(err)
	}
	s := string(block)
	all := connector.All()
	if len(all) == 0 {
		t.Fatal("no connectors registered")
	}
	for _, c := range all {
		if !strings.Contains(s, "`"+c.Slug()+"`") {
			t.Errorf("generated table has no row for %q", c.Slug())
		}
	}
}
