package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// generateAISummary formats all activities into a structured prompt and sends
// it to OpenCode (via `opencode run`) to produce a natural-language daily
// summary of what was authored, reviewed, and estimated time per task.
//
// Returns the AI-generated summary string, or an error message if the call
// fails. Always prints progress to stdout.
func generateAISummary(activities []Activity) string {
	prompt := buildSummaryPrompt(activities)
	if prompt == "" {
		return "(no activities to summarise)"
	}

	fmt.Println("\nGenerating AI summary via OpenCode...")

	cmd := exec.Command("opencode", "run", prompt)
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return fmt.Sprintf("(AI summary unavailable: %v — %s)", err, strings.TrimSpace(errOut.String()))
	}

	result := strings.TrimSpace(out.String())
	if result == "" {
		return "(AI returned an empty response)"
	}
	return result
}

// buildSummaryPrompt constructs the structured prompt sent to OpenCode.
// Activities are grouped by date, then split into authored (Git/GitHub raised)
// vs reviewed (GitReview/GitHub reviewed) with time estimates.
func buildSummaryPrompt(activities []Activity) string {
	if len(activities) == 0 {
		return ""
	}

	// Group by date.
	type dayData struct {
		authored []Activity
		reviewed []Activity
		other    []Activity
	}
	byDate := make(map[string]*dayData)
	var dates []string

	for _, a := range activities {
		if _, ok := byDate[a.Date]; !ok {
			byDate[a.Date] = &dayData{}
			dates = append(dates, a.Date)
		}
		d := byDate[a.Date]
		switch {
		case a.Source == "Git":
			d.authored = append(d.authored, a)
		case a.Source == "GitReview":
			d.reviewed = append(d.reviewed, a)
		case a.Source == "GitHub" && strings.HasPrefix(a.Description, "Raised"):
			d.authored = append(d.authored, a)
		case a.Source == "GitHub" && strings.HasPrefix(a.Description, "Reviewed"):
			d.reviewed = append(d.reviewed, a)
		default:
			d.other = append(d.other, a)
		}
	}

	sort.Sort(sort.Reverse(sort.StringSlice(dates)))

	var sb strings.Builder
	sb.WriteString(`You are a timesheet assistant. Given the following work activities grouped by date, produce a concise natural-language daily summary for each day.

For each day include:
1. What was authored/created (commits, PRs raised)
2. What was reviewed (PR reviews, teammate commits observed)
3. Estimated time spent on each task or theme (use the Minutes field; 0 = unknown)
4. A brief overall summary of the day's focus

Keep each day's summary to 3-5 sentences. Use plain text, no markdown headers.

--- ACTIVITY DATA ---
`)

	for _, date := range dates {
		d := byDate[date]
		t, _ := time.Parse("2006-01-02", date)
		dayOfWeek := t.Weekday().String()
		sb.WriteString(fmt.Sprintf("\nDate: %s (%s)\n", date, dayOfWeek))

		if len(d.authored) > 0 {
			sb.WriteString("  Authored:\n")
			for _, a := range d.authored {
				mins := ""
				if a.Minutes > 0 {
					mins = fmt.Sprintf(" (~%s)", formatMins(a.Minutes))
				}
				sb.WriteString(fmt.Sprintf("    [%s] %s%s\n", a.Source, a.Description, mins))
			}
		}

		if len(d.reviewed) > 0 {
			sb.WriteString("  Reviewed:\n")
			for _, a := range d.reviewed {
				mins := ""
				if a.Minutes > 0 {
					mins = fmt.Sprintf(" (~%s)", formatMins(a.Minutes))
				}
				sb.WriteString(fmt.Sprintf("    [%s] %s%s\n", a.Source, a.Description, mins))
			}
		}

		if len(d.other) > 0 {
			sb.WriteString("  Other:\n")
			for _, a := range d.other {
				mins := ""
				if a.Minutes > 0 {
					mins = fmt.Sprintf(" (~%s)", formatMins(a.Minutes))
				}
				sb.WriteString(fmt.Sprintf("    [%s] %s%s\n", a.Source, a.Description, mins))
			}
		}
	}

	sb.WriteString("\n--- END ACTIVITY DATA ---\n")
	return sb.String()
}
