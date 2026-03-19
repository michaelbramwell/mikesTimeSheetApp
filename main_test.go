package main

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// buildDayComment tests
// ---------------------------------------------------------------------------

func TestBuildDayComment_Git(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Time: "09:15", Source: "Git", Description: "[myrepo] 09:15 fix null pointer", Minutes: 30},
	}
	got := buildDayComment(activities)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (item + total), got %d:\n%s", len(lines), got)
	}
	if lines[0] != "- [Git] [myrepo] 09:15 fix null pointer (30m)" {
		t.Errorf("unexpected item line: %s", lines[0])
	}
	if lines[1] != "- [Git total] ~30m" {
		t.Errorf("unexpected total line: %s", lines[1])
	}
}

func TestBuildDayComment_GitNoMinutes(t *testing.T) {
	// Git activities with Minutes=0 (non-first commits in a day) produce no total line.
	activities := []Activity{
		{Date: "2026-02-17", Time: "09:15", Source: "Git", Description: "[myrepo] 09:15 fix null pointer", Minutes: 0},
	}
	got := buildDayComment(activities)
	lines := strings.Split(got, "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (no total when Minutes=0), got %d:\n%s", len(lines), got)
	}
	if lines[0] != "- [Git] [myrepo] 09:15 fix null pointer" {
		t.Errorf("unexpected line: %s", lines[0])
	}
}

func TestBuildDayComment_Jira(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Time: "10:00", Source: "Jira", Description: "PROJ-123 | Add dark mode (Status: In Progress)", Minutes: 30},
	}
	got := buildDayComment(activities)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (item + total), got %d:\n%s", len(lines), got)
	}
	if lines[0] != "- [PROJ-123] Add dark mode (Status: In Progress) (30m)" {
		t.Errorf("unexpected item line: %s", lines[0])
	}
	if lines[1] != "- [Jira total] ~30m" {
		t.Errorf("unexpected total line: %s", lines[1])
	}
}

func TestBuildDayComment_JiraMultiple(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Time: "10:00", Source: "Jira", Description: "PROJ-123 | Add dark mode (Status: In Progress)", Minutes: 30},
		{Date: "2026-02-17", Time: "11:00", Source: "Jira", Description: "PROJ-456 | Fix login (Status: Done)", Minutes: 30},
	}
	got := buildDayComment(activities)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 items + total), got %d:\n%s", len(lines), got)
	}
	if lines[2] != "- [Jira total] ~1h" {
		t.Errorf("unexpected total line: %s", lines[2])
	}
}

func TestBuildDayComment_GitHub(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Time: "11:00", Source: "GitHub", Description: "Raised PR #42: implement feature X", Minutes: 60},
		{Date: "2026-02-17", Time: "14:00", Source: "GitHub", Description: "Reviewed PR #99: fix login bug", Minutes: 30},
	}
	got := buildDayComment(activities)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 items + total), got %d:\n%s", len(lines), got)
	}
	if !strings.Contains(lines[0], "Raised PR #42") || !strings.Contains(lines[0], "(1h)") {
		t.Errorf("line 0 missing PR raise or time: %s", lines[0])
	}
	if !strings.Contains(lines[1], "Reviewed PR #99") || !strings.Contains(lines[1], "(30m)") {
		t.Errorf("line 1 missing PR review or time: %s", lines[1])
	}
	if lines[2] != "- [GitHub total] ~1h30m" {
		t.Errorf("unexpected total line: %s", lines[2])
	}
}

func TestBuildDayComment_Meeting(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Time: "09:00-09:30", Source: "Meeting", Description: "09:00-09:30 Standup (30m)", Minutes: 30},
	}
	got := buildDayComment(activities)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (item + total), got %d:\n%s", len(lines), got)
	}
	if lines[0] != "- [Meeting] 09:00-09:30 Standup (30m) (30m)" {
		t.Errorf("unexpected item line: %s", lines[0])
	}
	if lines[1] != "- [Meeting total] ~30m" {
		t.Errorf("unexpected total line: %s", lines[1])
	}
}

func TestBuildDayComment_Chat(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Time: "15:30", Source: "Chat", Description: "15:30 Alice Smith", Minutes: 15},
	}
	got := buildDayComment(activities)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (item + total), got %d:\n%s", len(lines), got)
	}
	if lines[0] != "- [Chat] 15:30 Alice Smith (15m)" {
		t.Errorf("unexpected item line: %s", lines[0])
	}
	if lines[1] != "- [Chat total] ~15m" {
		t.Errorf("unexpected total line: %s", lines[1])
	}
}

func TestBuildDayComment_Email(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Time: "10:30", Source: "Email", Description: "Email to Alice Smith: Project Update", Minutes: 15},
	}
	got := buildDayComment(activities)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (item + total), got %d:\n%s", len(lines), got)
	}
	if lines[0] != "- [Email] Email to Alice Smith: Project Update (15m)" {
		t.Errorf("unexpected item line: %s", lines[0])
	}
	if lines[1] != "- [Email total] ~15m" {
		t.Errorf("unexpected total line: %s", lines[1])
	}
}

func TestBuildDayComment_EmailMultipleRecipients(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Time: "11:00", Source: "Email", Description: "Email to Alice Smith, Bob Jones: Team Update", Minutes: 15},
	}
	got := buildDayComment(activities)
	if !strings.Contains(got, "Alice Smith, Bob Jones") {
		t.Errorf("expected recipient names in output, got %q", got)
	}
	if !strings.Contains(got, "Team Update") {
		t.Errorf("expected subject in output, got %q", got)
	}
	if !strings.Contains(got, "(15m)") {
		t.Errorf("expected duration in output, got %q", got)
	}
}

func TestBuildDayComment_AllSources(t *testing.T) {
	activities := []Activity{
		{Date: "2026-02-17", Source: "Git", Description: "[repo] 09:00 initial commit", Minutes: 30},
		{Date: "2026-02-17", Source: "Jira", Description: "PROJ-1 | Do the thing (Status: Done)", Minutes: 30},
		{Date: "2026-02-17", Source: "GitHub", Description: "Raised PR #1: new feature", Minutes: 60},
		{Date: "2026-02-17", Source: "Meeting", Description: "10:00-10:30 Planning (30m)", Minutes: 30},
		{Date: "2026-02-17", Source: "Chat", Description: "11:00 Bob Jones", Minutes: 15},
		{Date: "2026-02-17", Source: "Email", Description: "Email to Alice Smith: Quarterly report", Minutes: 15},
	}
	got := buildDayComment(activities)
	lines := strings.Split(got, "\n")
	// 6 items + 6 total lines = 12
	if len(lines) != 12 {
		t.Fatalf("expected 12 lines, got %d:\n%s", len(lines), got)
	}
	if !strings.HasPrefix(lines[0], "- [Git]") {
		t.Errorf("line 0 should be Git item: %s", lines[0])
	}
	if lines[1] != "- [Git total] ~30m" {
		t.Errorf("line 1 should be Git total: %s", lines[1])
	}
	if !strings.HasPrefix(lines[2], "- [PROJ-1]") {
		t.Errorf("line 2 should be Jira ticket: %s", lines[2])
	}
	if lines[3] != "- [Jira total] ~30m" {
		t.Errorf("line 3 should be Jira total: %s", lines[3])
	}
	if !strings.HasPrefix(lines[4], "- [GitHub]") {
		t.Errorf("line 4 should be GitHub: %s", lines[4])
	}
	if lines[5] != "- [GitHub total] ~1h" {
		t.Errorf("line 5 should be GitHub total: %s", lines[5])
	}
	if !strings.HasPrefix(lines[6], "- [Meeting]") {
		t.Errorf("line 6 should be Meeting: %s", lines[6])
	}
	if lines[7] != "- [Meeting total] ~30m" {
		t.Errorf("line 7 should be Meeting total: %s", lines[7])
	}
	if !strings.HasPrefix(lines[8], "- [Chat]") {
		t.Errorf("line 8 should be Chat: %s", lines[8])
	}
	if lines[9] != "- [Chat total] ~15m" {
		t.Errorf("line 9 should be Chat total: %s", lines[9])
	}
	if !strings.HasPrefix(lines[10], "- [Email]") {
		t.Errorf("line 10 should be Email: %s", lines[10])
	}
	if lines[11] != "- [Email total] ~15m" {
		t.Errorf("line 11 should be Email total: %s", lines[11])
	}
}

func TestBuildDayComment_Truncation(t *testing.T) {
	// Build a comment that exceeds 1000 characters
	long := strings.Repeat("x", 600)
	activities := []Activity{
		{Date: "2026-02-17", Source: "Git", Description: long},
		{Date: "2026-02-17", Source: "Git", Description: long},
	}
	got := buildDayComment(activities)
	if len(got) > 1000 {
		t.Errorf("expected comment truncated to <=1000 chars, got %d", len(got))
	}
}

func TestBuildDayComment_SmartTruncation_KeepsTimecodes(t *testing.T) {
	// Descriptions should be shortened but timecodes and prefixes preserved
	long := strings.Repeat("y", 600)
	activities := []Activity{
		{Date: "2026-02-17", Source: "Git", Description: long, Minutes: 45},
		{Date: "2026-02-17", Source: "Git", Description: long, Minutes: 0},
	}
	got := buildDayComment(activities)
	if len(got) > 1000 {
		t.Errorf("expected comment <=1000 chars, got %d", len(got))
	}
	// The timecode for the first activity must still appear
	if !strings.Contains(got, "(45m)") {
		t.Errorf("expected timecode (45m) to be preserved, got:\n%s", got)
	}
	// The [Git] prefix must still appear
	if !strings.Contains(got, "[Git]") {
		t.Errorf("expected [Git] prefix to be preserved, got:\n%s", got)
	}
}

func TestBuildDayComment_Empty(t *testing.T) {
	got := buildDayComment([]Activity{})
	if got != "" {
		t.Errorf("expected empty string for no activities, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// calcGitSessionMins tests
// ---------------------------------------------------------------------------

func TestCalcGitSessionMins_Empty(t *testing.T) {
	if got := calcGitSessionMins(nil); got != 0 {
		t.Errorf("expected 0 for empty input, got %d", got)
	}
}

func TestCalcGitSessionMins_SingleCommit(t *testing.T) {
	ts := []time.Time{mustParseTime("2026-02-17 09:00")}
	got := calcGitSessionMins(ts)
	// 15m padding, already a multiple of 15
	if got != 15 {
		t.Errorf("expected 15, got %d", got)
	}
}

func TestCalcGitSessionMins_TwoCommitsWithinSession(t *testing.T) {
	ts := []time.Time{
		mustParseTime("2026-02-17 09:00"),
		mustParseTime("2026-02-17 09:45"),
	}
	got := calcGitSessionMins(ts)
	// 15 padding + 45 gap = 60, already multiple of 15
	if got != 60 {
		t.Errorf("expected 60, got %d", got)
	}
}

func TestCalcGitSessionMins_TwoSessions(t *testing.T) {
	ts := []time.Time{
		mustParseTime("2026-02-17 09:00"),
		mustParseTime("2026-02-17 12:00"), // 3h gap > 2h threshold = new session
	}
	got := calcGitSessionMins(ts)
	// 15 padding + 15 padding (new session) = 30, already multiple of 15
	if got != 30 {
		t.Errorf("expected 30, got %d", got)
	}
}

func TestCalcGitSessionMins_RoundsUpTo15(t *testing.T) {
	ts := []time.Time{
		mustParseTime("2026-02-17 09:00"),
		mustParseTime("2026-02-17 09:17"), // 17m gap
	}
	got := calcGitSessionMins(ts)
	// 15 padding + 17 gap = 32 -> rounds up to 45
	if got != 45 {
		t.Errorf("expected 45, got %d", got)
	}
}

func TestCalcGitSessionMins_AlreadyMultipleOf15(t *testing.T) {
	ts := []time.Time{
		mustParseTime("2026-02-17 09:00"),
		mustParseTime("2026-02-17 09:30"), // 30m gap
	}
	got := calcGitSessionMins(ts)
	// 15 padding + 30 gap = 45, already multiple of 15
	if got != 45 {
		t.Errorf("expected 45, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// extractTicketKey tests
// ---------------------------------------------------------------------------

func TestExtractTicketKey_Found(t *testing.T) {
	cases := []struct {
		msg  string
		want string
	}{
		{"CDS-123 add feature", "CDS-123"},
		{"fix bug in PROJ-42 login flow", "PROJ-42"},
		{"ABC-1 initial commit", "ABC-1"},
	}
	for _, c := range cases {
		got := extractTicketKey(c.msg)
		if got != c.want {
			t.Errorf("extractTicketKey(%q) = %q, want %q", c.msg, got, c.want)
		}
	}
}

func TestExtractTicketKey_NotFound(t *testing.T) {
	cases := []string{
		"fix null pointer",
		"update readme",
		"cds-123 lowercase doesn't match",
		"123-ABC wrong order",
	}
	for _, msg := range cases {
		got := extractTicketKey(msg)
		if got != "" {
			t.Errorf("extractTicketKey(%q) = %q, want empty", msg, got)
		}
	}
}

// ---------------------------------------------------------------------------
// formatMins tests
// ---------------------------------------------------------------------------

func TestFormatMins(t *testing.T) {
	cases := []struct {
		mins int
		want string
	}{
		{15, "15m"},
		{30, "30m"},
		{60, "1h"},
		{75, "1h15m"},
		{90, "1h30m"},
		{120, "2h"},
	}
	for _, c := range cases {
		got := formatMins(c.mins)
		if got != c.want {
			t.Errorf("formatMins(%d) = %q, want %q", c.mins, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Flag combination tests
// ---------------------------------------------------------------------------

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
		got, err := parseDateToWeekStart(tc.input)
		if err != nil {
			t.Errorf("parseDateToWeekStart(%q) returned unexpected error: %v", tc.input, err)
			continue
		}
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

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func mustParseTime(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}
