package main

import (
	"fmt"
	"sort"
	"strings"
)

const (
	maxCommentLen  = 1000
	workdayMinutes = 480
)

// commentLine holds a fully-rendered line plus the mutable description portion.
// Only the description is shortened during smart truncation; the prefix (ticket/source),
// timestamps, and time codes are always preserved.
type commentLine struct {
	prefix   string // e.g. "[Git]" or "[DTP-1164]"
	desc     string // free-text that may be shortened
	timecode string // e.g. "(1h15m)" or "" — always kept
	isTotal  bool   // section-total lines are never shortened
}

// render returns the line as it will appear in the comment.
func (c commentLine) render() string {
	if c.isTotal {
		return c.prefix // total lines are already fully formed
	}
	line := fmt.Sprintf("- %s %s", c.prefix, c.desc)
	if c.timecode != "" {
		line += " " + c.timecode
	}
	return line
}

// buildDayComment constructs the Projectworks comment string for a single day's activities.
// Activities are grouped by source. Each item shows its duration if known, and a
// section-total line is appended after each source group.
// If the result exceeds maxCommentLen, descriptions are shortened (keeping prefixes,
// timestamps, ticket keys, and time codes) until it fits.
func buildDayComment(activities []Activity) string {
	if len(activities) == 0 {
		return ""
	}

	// Preserve source order of first appearance.
	var sourceOrder []string
	bySource := make(map[string][]Activity)
	for _, a := range activities {
		if _, exists := bySource[a.Source]; !exists {
			sourceOrder = append(sourceOrder, a.Source)
		}
		bySource[a.Source] = append(bySource[a.Source], a)
	}

	var cls []commentLine
	for _, src := range sourceOrder {
		group := bySource[src]
		sourceTotal := 0
		for _, a := range group {
			prefix := fmt.Sprintf("[%s]", a.Source)
			desc := strings.TrimSpace(a.Description)

			if a.Source == "Jira" && !strings.HasPrefix(desc, "[") {
				parts := strings.SplitN(desc, "|", 2)
				if len(parts) == 2 {
					ticket := strings.TrimSpace(parts[0])
					prefix = fmt.Sprintf("[%s]", ticket)
					desc = strings.TrimSpace(parts[1])
				}
			}

			timecode := ""
			if a.Minutes > 0 {
				timecode = fmt.Sprintf("(%s)", formatMins(a.Minutes))
			}
			cls = append(cls, commentLine{prefix: prefix, desc: desc, timecode: timecode})
			sourceTotal += a.Minutes
		}
		if sourceTotal > 0 {
			cls = append(cls, commentLine{
				prefix:  fmt.Sprintf("- [%s total] ~%s", src, formatMins(sourceTotal)),
				isTotal: true,
			})
		}
	}

	// Fast path: fits within limit.
	joined := func() string {
		parts := make([]string, len(cls))
		for i, c := range cls {
			parts[i] = c.render()
		}
		return strings.Join(parts, "\n")
	}
	result := joined()
	if len(result) <= maxCommentLen {
		return result
	}

	// Smart truncation: shorten descriptions proportionally until we fit.
	// Total lines and timecodes are never touched.
	// Count how many chars the non-truncatable parts occupy.
	overhead := 0
	descCount := 0
	for _, c := range cls {
		if c.isTotal {
			overhead += len(c.render()) + 1 // +1 for newline
			continue
		}
		// "- [prefix] " + " (timecode)" + newline
		overhead += len(fmt.Sprintf("- %s  %s", c.prefix, c.timecode)) + 1
		descCount++
	}

	if descCount == 0 {
		// Nothing to shorten; hard-truncate as last resort.
		return result[:maxCommentLen-3] + "..."
	}

	// Budget split evenly across all description slots.
	descBudget := (maxCommentLen - overhead) / descCount
	if descBudget < 0 {
		descBudget = 0
	}

	for i := range cls {
		if cls[i].isTotal {
			continue
		}
		d := cls[i].desc
		if len(d) > descBudget {
			if descBudget > 1 {
				cls[i].desc = d[:descBudget-1] + "~"
			} else {
				cls[i].desc = "~"
			}
		}
	}

	result = joined()
	// Final hard cap in case rounding left us slightly over.
	if len(result) > maxCommentLen {
		result = result[:maxCommentLen-3] + "..."
	}
	return result
}

func processAndPostActivities(activities []Activity, cfg pwConfig, dryRun bool) {
	fmt.Println("\n=========================================")
	fmt.Println(" PROJECTWORKS SYNC")
	fmt.Println("=========================================")

	if cfg.Cookie == "" {
		fmt.Println("PW_COOKIE not set, skipping Projectworks sync.")
		return
	}

	// Group by date
	byDate := make(map[string][]Activity)
	for _, a := range activities {
		if a.Date != "" {
			byDate[a.Date] = append(byDate[a.Date], a)
		}
	}

	// Sort dates
	var dates []string
	for d := range byDate {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	if len(dates) == 0 {
		fmt.Println("No activities found to sync.")
		return
	}

	token, existingEntries, err := fetchPWContext(cfg, dates[0])
	if err != nil {
		fmt.Printf("Error fetching PW context: %v\n", err)
		return
	}

	for _, date := range dates {
		dayActivities := byDate[date]
		if len(dayActivities) == 0 {
			continue
		}

		fullComment := buildDayComment(dayActivities)

		existingID, hasExisting := existingEntries[date]
		var idPtr *int
		if hasExisting {
			idPtr = &existingID
		}

		if dryRun {
			fmt.Printf("\n[DRY RUN] Would post to Projectworks for %s:\n", date)
			fmt.Printf("  TaskID: %d\n", cfg.TaskID)
			fmt.Printf("  Hours: 8 (%d mins)\n", workdayMinutes)
			fmt.Printf("  UserTaskHourID: %v\n", idPtr)
			fmt.Printf("  Comment:\n%s\n", fullComment)
		} else {
			fmt.Printf("\nPosting timesheet for %s (8 hours)...\n", date)
			err := postPWTimeEntry(cfg, token, date, workdayMinutes, fullComment, idPtr)
			if err != nil {
				fmt.Printf("  Error posting: %v\n", err)
			} else {
				fmt.Printf("  Success! (UserTaskHourID: %v)\n", idPtr)
			}
		}
	}

	if !dryRun {
		weekStart, err := parseDateToWeekStart(dates[0])
		if err == nil {
			fmt.Printf("\nView timesheet: %s/Timesheet/Timesheet?userID=%s&window=week%%3B%s\n", cfg.BaseURL, cfg.UserID, weekStart)
		}
	}
}
