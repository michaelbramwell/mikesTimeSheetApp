package main

import (
	"fmt"
	"sort"
	"strings"
)

// buildDayComment constructs the Projectworks comment string for a single day's activities.
func buildDayComment(activities []Activity) string {
	var comments []string
	for _, a := range activities {
		prefix := fmt.Sprintf("[%s]", a.Source)
		if a.Source == "Jira" && !strings.HasPrefix(a.Description, "[") {
			parts := strings.SplitN(a.Description, "|", 2)
			if len(parts) == 2 {
				ticket := strings.TrimSpace(parts[0])
				title := strings.TrimSpace(parts[1])
				prefix = fmt.Sprintf("[%s]", ticket)
				a.Description = title
			}
		}
		comments = append(comments, fmt.Sprintf("- %s %s", prefix, strings.TrimSpace(a.Description)))
	}
	result := strings.Join(comments, "\n")
	if len(result) > 1000 {
		result = result[:997] + "..."
	}
	return result
}

func processAndPostActivities(activities []Activity, cfg PWConfig, dryRun bool) {
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

	token, existingEntries, err := FetchPWContext(cfg, dates[0])
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
			fmt.Printf("  Hours: 8 (480 mins)\n")
			fmt.Printf("  UserTaskHourID: %v\n", idPtr)
			fmt.Printf("  Comment:\n%s\n", fullComment)
		} else {
			fmt.Printf("\nPosting timesheet for %s (8 hours)...\n", date)
			err := PostPWTimeEntry(cfg, token, date, 480, fullComment, idPtr)
			if err != nil {
				fmt.Printf("  Error posting: %v\n", err)
			} else {
				fmt.Printf("  Success! (UserTaskHourID: %v)\n", idPtr)
			}
		}
	}

	if !dryRun {
		weekStart := parseDateToWeekStart(dates[0])
		fmt.Printf("\nView timesheet: https://diversus.projectworksapp.com/Timesheet/Timesheet?userID=%s&window=week%%3B%s\n", cfg.UserID, weekStart)
	}
}
