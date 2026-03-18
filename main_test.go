package main

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// buildDayComment tests
// ---------------------------------------------------------------------------

func TestBuildDayComment_Git(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Time: "09:15", Source: "Git", Description: "[myrepo] 09:15 fix null pointer"},
	}
	got := buildDayComment(activities)
	want := "- [Git] [myrepo] 09:15 fix null pointer"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildDayComment_Jira(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Time: "10:00", Source: "Jira", Description: "PROJ-123 | Add dark mode (Status: In Progress)"},
	}
	got := buildDayComment(activities)
	want := "- [PROJ-123] Add dark mode (Status: In Progress)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildDayComment_GitHub(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Time: "11:00", Source: "GitHub", Description: "Raised PR #42: implement feature X"},
		{Date: "2026-02-17", Time: "14:00", Source: "GitHub", Description: "Reviewed PR #99: fix login bug"},
	}
	got := buildDayComment(activities)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %s", len(lines), got)
	}
	if !strings.Contains(lines[0], "Raised PR #42") {
		t.Errorf("line 0 missing PR raise: %s", lines[0])
	}
	if !strings.Contains(lines[1], "Reviewed PR #99") {
		t.Errorf("line 1 missing PR review: %s", lines[1])
	}
}

func TestBuildDayComment_Meeting(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Time: "09:00-09:30", Source: "Meeting", Description: "09:00-09:30 Standup (30m)"},
	}
	got := buildDayComment(activities)
	want := "- [Meeting] 09:00-09:30 Standup (30m)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildDayComment_Chat(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Time: "15:30", Source: "Chat", Description: "15:30 Alice Smith"},
	}
	got := buildDayComment(activities)
	want := "- [Chat] 15:30 Alice Smith"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildDayComment_Email(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Time: "10:30", Source: "Email", Description: "Email to Alice Smith: Project Update"},
	}
	got := buildDayComment(activities)
	want := "- [Email] Email to Alice Smith: Project Update"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildDayComment_EmailMultipleRecipients(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Time: "11:00", Source: "Email", Description: "Email to Alice Smith, Bob Jones: Team Update"},
	}
	got := buildDayComment(activities)
	if !strings.Contains(got, "Alice Smith, Bob Jones") {
		t.Errorf("expected recipient names in output, got %q", got)
	}
	if !strings.Contains(got, "Team Update") {
		t.Errorf("expected subject in output, got %q", got)
	}
}

func TestBuildDayComment_AllSources(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Source: "Git", Description: "[repo] 09:00 initial commit"},
		{Date: "2026-02-17", Source: "Jira", Description: "PROJ-1 | Do the thing (Status: Done)"},
		{Date: "2026-02-17", Source: "GitHub", Description: "Raised PR #1: new feature"},
		{Date: "2026-02-17", Source: "Meeting", Description: "10:00-10:30 Planning (30m)"},
		{Date: "2026-02-17", Source: "Chat", Description: "11:00 Bob Jones"},
		{Date: "2026-02-17", Source: "Email", Description: "Email to Alice Smith: Quarterly report"},
	}
	got := buildDayComment(activities)
	lines := strings.Split(got, "\n")
	if len(lines) != 6 {
		t.Fatalf("expected 6 lines, got %d:\n%s", len(lines), got)
	}
	if !strings.HasPrefix(lines[0], "- [Git]") {
		t.Errorf("line 0 should be Git: %s", lines[0])
	}
	if !strings.HasPrefix(lines[1], "- [PROJ-1]") {
		t.Errorf("line 1 should be Jira ticket: %s", lines[1])
	}
	if !strings.HasPrefix(lines[2], "- [GitHub]") {
		t.Errorf("line 2 should be GitHub: %s", lines[2])
	}
	if !strings.HasPrefix(lines[3], "- [Meeting]") {
		t.Errorf("line 3 should be Meeting: %s", lines[3])
	}
	if !strings.HasPrefix(lines[4], "- [Chat]") {
		t.Errorf("line 4 should be Chat: %s", lines[4])
	}
	if !strings.HasPrefix(lines[5], "- [Email]") {
		t.Errorf("line 5 should be Email: %s", lines[5])
	}
}

func TestBuildDayComment_Truncation(t *testing.T) {
	// Build a comment that exceeds 1000 characters
	long := strings.Repeat("x", 950)
	activities := []Activity{
		{Date: "2026-02-17", Source: "Git", Description: long},
		{Date: "2026-02-17", Source: "Git", Description: long},
	}
	got := buildDayComment(activities)
	if len(got) > 1000 {
		t.Errorf("expected comment truncated to <=1000 chars, got %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected truncated comment to end with '...', got: %s", got[len(got)-10:])
	}
}

func TestBuildDayComment_Empty(t *testing.T) {
	got := buildDayComment([]Activity{})
	if got != "" {
		t.Errorf("expected empty string for no activities, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Flag combination tests
// ---------------------------------------------------------------------------
// These tests verify that the correct set of sources is included/excluded
// depending on -noAzure, -noGitHub, -noJira flags. We use a helper that
// simulates the flag logic from main() and returns which sources would run.

type sourceFlags struct {
	noAzure  bool
	noGitHub bool
	noJira   bool
}

// activeSources mirrors the main() logic and returns which sources are enabled.
func activeSources(f sourceFlags) []string {
	var sources []string
	sources = append(sources, "Git") // always on
	if !f.noJira {
		sources = append(sources, "Jira")
	}
	if !f.noGitHub {
		sources = append(sources, "GitHub")
	}
	if !f.noAzure {
		sources = append(sources, "Meeting")
		sources = append(sources, "Chat")
		sources = append(sources, "Email")
	}
	return sources
}

func containsAll(got []string, want ...string) bool {
	set := make(map[string]bool, len(got))
	for _, s := range got {
		set[s] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func containsNone(got []string, none ...string) bool {
	set := make(map[string]bool, len(got))
	for _, s := range got {
		set[s] = true
	}
	for _, n := range none {
		if set[n] {
			return false
		}
	}
	return true
}

func TestFlagCombos(t *testing.T) {
	tests := []struct {
		name          string
		flags         sourceFlags
		expectPresent []string
		expectAbsent  []string
	}{
		{
			name:          "all sources enabled (no flags)",
			flags:         sourceFlags{},
			expectPresent: []string{"Git", "Jira", "GitHub", "Meeting", "Chat", "Email"},
			expectAbsent:  nil,
		},
		{
			name:          "-noAzure only",
			flags:         sourceFlags{noAzure: true},
			expectPresent: []string{"Git", "Jira", "GitHub"},
			expectAbsent:  []string{"Meeting", "Chat", "Email"},
		},
		{
			name:          "-noGitHub only",
			flags:         sourceFlags{noGitHub: true},
			expectPresent: []string{"Git", "Jira", "Meeting", "Chat", "Email"},
			expectAbsent:  []string{"GitHub"},
		},
		{
			name:          "-noJira only",
			flags:         sourceFlags{noJira: true},
			expectPresent: []string{"Git", "GitHub", "Meeting", "Chat", "Email"},
			expectAbsent:  []string{"Jira"},
		},
		{
			name:          "-noAzure -noGitHub",
			flags:         sourceFlags{noAzure: true, noGitHub: true},
			expectPresent: []string{"Git", "Jira"},
			expectAbsent:  []string{"Meeting", "Chat", "Email", "GitHub"},
		},
		{
			name:          "-noAzure -noJira",
			flags:         sourceFlags{noAzure: true, noJira: true},
			expectPresent: []string{"Git", "GitHub"},
			expectAbsent:  []string{"Meeting", "Chat", "Email", "Jira"},
		},
		{
			name:          "-noGitHub -noJira",
			flags:         sourceFlags{noGitHub: true, noJira: true},
			expectPresent: []string{"Git", "Meeting", "Chat", "Email"},
			expectAbsent:  []string{"GitHub", "Jira"},
		},
		{
			name:          "-noAzure -noGitHub -noJira (git only)",
			flags:         sourceFlags{noAzure: true, noGitHub: true, noJira: true},
			expectPresent: []string{"Git"},
			expectAbsent:  []string{"Meeting", "Chat", "Email", "GitHub", "Jira"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := activeSources(tc.flags)
			if !containsAll(got, tc.expectPresent...) {
				t.Errorf("expected sources %v to be present, got %v", tc.expectPresent, got)
			}
			if !containsNone(got, tc.expectAbsent...) {
				t.Errorf("expected sources %v to be absent, got %v", tc.expectAbsent, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseDateToWeekStart tests
// ---------------------------------------------------------------------------

func TestParseDateToWeekStart(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2026-02-17", "2026-02-16"}, // Tuesday -> Monday
		{"2026-02-16", "2026-02-16"}, // Monday  -> Monday
		{"2026-02-22", "2026-02-16"}, // Sunday  -> Monday (previous)
		{"2026-02-20", "2026-02-16"}, // Friday  -> Monday
		{"2026-02-24", "2026-02-23"}, // Tuesday -> Monday
	}
	for _, tc := range tests {
		got := parseDateToWeekStart(tc.input)
		if got != tc.want {
			t.Errorf("parseDateToWeekStart(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// formatEmailDescription tests
// ---------------------------------------------------------------------------

func TestFormatEmailDescription_Normal(t *testing.T) {
	got := formatEmailDescription("Alice Smith", "Project Update")
	want := "Email to Alice Smith: Project Update"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatEmailDescription_MultipleRecipients(t *testing.T) {
	got := formatEmailDescription("Alice Smith, Bob Jones", "Team Standup Notes")
	want := "Email to Alice Smith, Bob Jones: Team Standup Notes"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatEmailDescription_EmptySubject(t *testing.T) {
	got := formatEmailDescription("Alice Smith", "")
	want := "Email to Alice Smith: (no subject)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatEmailDescription_EmptyRecipient(t *testing.T) {
	got := formatEmailDescription("", "Hello")
	want := "Email to unknown: Hello"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatEmailDescription_BothEmpty(t *testing.T) {
	got := formatEmailDescription("", "")
	want := "Email to unknown: (no subject)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
