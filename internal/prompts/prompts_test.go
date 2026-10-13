// SPDX-License-Identifier: AGPL-3.0-only

package prompts

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestLibraryIsConsistent checks every prompt: a unique snake_case name, a
// known section, and blanks that match its arguments one to one.
func TestLibraryIsConsistent(t *testing.T) {
	name := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	seen := map[string]bool{}
	for _, p := range Library {
		if !name.MatchString(p.Name) || seen[p.Name] {
			t.Errorf("%q: name must be unique snake_case", p.Name)
		}
		seen[p.Name] = true
		if !slices.Contains(Sections, p.Section) {
			t.Errorf("%s: unknown section %q", p.Name, p.Section)
		}
		if p.Title == "" || p.When == "" || p.Text == "" {
			t.Errorf("%s: title, when and text are required", p.Name)
		}
		used := map[string]bool{}
		for _, m := range blank.FindAllStringSubmatch(p.Text, -1) {
			used[m[1]] = true
		}
		for _, a := range p.Args {
			if !used[a.Name] {
				t.Errorf("%s: argument %q is not used in the text", p.Name, a.Name)
			}
			if a.Description == "" || a.Placeholder == "" {
				t.Errorf("%s: argument %q needs a description and a placeholder", p.Name, a.Name)
			}
			delete(used, a.Name)
		}
		for n := range used {
			t.Errorf("%s: blank {%s} has no argument", p.Name, n)
		}
	}
}

func TestFill(t *testing.T) {
	p, ok := Find("after_break")
	if !ok {
		t.Fatal("after_break missing")
	}
	if _, err := p.Fill(map[string]string{"break_start": "2026-09-01"}); err == nil {
		t.Fatal("a required argument left empty must be an error")
	}
	got, err := p.Fill(map[string]string{"break_start": "2026-09-01", "break_end": " 2026-09-14 "})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "I stopped training from 2026-09-01 to 2026-09-14. Compare") || strings.Contains(got, "\n") {
		t.Fatalf("got %q", got)
	}

	p, _ = Find("sleep_habits")
	got, err = p.Fill(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "before 23:00?") {
		t.Fatalf("an optional blank must take its default, got %q", got)
	}
}

func TestDocText(t *testing.T) {
	p, _ := Find("new_habit")
	got := p.DocText()
	if !strings.HasPrefix(got, "I started [stopping caffeine after noon / training in the morning / a new bedtime] on [date].") {
		t.Fatalf("got %q", got)
	}
}

// TestRulesKeepWellnessFraming guards the two rules the library stands on.
func TestRulesKeepWellnessFraming(t *testing.T) {
	all := strings.Join(Rules, " ")
	for _, want := range []string{"do not diagnose", "clinician", "Never put a zero", "own history"} {
		if !strings.Contains(all, want) {
			t.Errorf("rules lack %q", want)
		}
	}
}
