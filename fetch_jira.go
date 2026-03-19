package main

import (
	"fmt"
	"time"

	"github.com/andygrunwald/go-jira"
)

func fetchJiraIssues(jiraURL, email, token, startDateStr, endDateStr string, localStart, localEnd time.Time) []Activity {
	var activities []Activity
	fmt.Println("\nJIRA ACTIVITY:")

	tp := jira.BasicAuthTransport{
		Username: email,
		Password: token,
	}

	client, err := jira.NewClient(tp.Client(), jiraURL)
	if err != nil {
		fmt.Printf("  Error creating Jira client: %v\n", err)
		return activities
	}

	// Query 1: Issues updated during the period (recently touched tickets).
	jql := fmt.Sprintf(`updated >= "%s" AND updated <= "%s" AND (assignee = currentUser() OR reporter = currentUser()) ORDER BY updated DESC`, startDateStr, endDateStr)
	fmt.Printf("  JQL (updated): %s\n", jql)

	issues, _, err := client.Issue.SearchV2JQL(jql, &jira.SearchOptionsV2{
		MaxResults: 50,
		Fields:     []string{"summary", "status", "updated"},
	})

	if err != nil {
		fmt.Printf("  Error searching Jira issues: %v\n", err)
		return activities
	}

	// Query 2: In-progress issues assigned to the user, updated within the last 7 days.
	// Ensures active work items appear even if not updated today, while excluding stale tickets.
	staleCutoff := time.Now().AddDate(0, 0, -7).UTC().Format("2006-01-02")
	jqlInProgress := fmt.Sprintf(`assignee = currentUser() AND status = "In Progress" AND updated >= "%s" ORDER BY updated DESC`, staleCutoff)
	fmt.Printf("  JQL (in-progress): %s\n", jqlInProgress)

	inProgressIssues, _, err := client.Issue.SearchV2JQL(jqlInProgress, &jira.SearchOptionsV2{
		MaxResults: 50,
		Fields:     []string{"summary", "status", "updated"},
	})
	if err != nil {
		fmt.Printf("  Error searching in-progress Jira issues: %v\n", err)
		// Non-fatal: continue with just the updated-based results
	}

	// Merge and deduplicate issues (updated-based first, then in-progress)
	seen := make(map[string]bool)
	var allIssues []jira.Issue
	for _, issue := range issues {
		if !seen[issue.Key] {
			seen[issue.Key] = true
			allIssues = append(allIssues, issue)
		}
	}
	for _, issue := range inProgressIssues {
		if !seen[issue.Key] {
			seen[issue.Key] = true
			allIssues = append(allIssues, issue)
		}
	}

	if len(allIssues) == 0 {
		fmt.Println("  No recent Jira activity found.")
	} else {
		localStartDate := localStart.Format("2006-01-02")
		localEndDate := localEnd.Format("2006-01-02")
		fmt.Printf("  Found %d issues (updated: %d, in-progress: %d, after dedup: %d)\n",
			len(allIssues), len(issues), len(inProgressIssues), len(allIssues))
		for _, issue := range allIssues {
			t := time.Time(issue.Fields.Updated).In(localStart.Location())
			localDate := t.Format("2006-01-02")
			statusName := "Unknown"
			if issue.Fields.Status != nil {
				statusName = issue.Fields.Status.Name
			}

			// If this issue was only found via the in-progress query (not in the updated window),
			// always include it with the period's start date for grouping.
			isInProgressOnly := !seenInUpdated(issue.Key, issues)
			if !isInProgressOnly {
				// From updated query: apply local date range filter
				if localDate < localStartDate || localDate > localEndDate {
					continue
				}
			}

			activityDate := localDate
			if isInProgressOnly {
				activityDate = localStartDate
			}
			fmt.Printf("  - [%s] %s | %s (Status: %s)\n", t.Format("2006-01-02 15:04"), issue.Key, issue.Fields.Summary, statusName)
			activities = append(activities, Activity{
				Date:        activityDate,
				Time:        t.Format("15:04"),
				Source:      "Jira",
				Description: fmt.Sprintf("%s | %s (Status: %s)", issue.Key, issue.Fields.Summary, statusName),
				Minutes:     30,
			})
		}
	}
	if len(activities) > 0 {
		const minsPerIssue = 30
		totalEstMins := len(activities) * minsPerIssue
		fmt.Printf("  Total Jira Items: %d (~%d mins each, ~%d mins total)\n", len(activities), minsPerIssue, totalEstMins)
	}
	fmt.Printf("-----------------------------------------\n")
	return activities
}

// seenInUpdated checks if a given issue key exists in the updated-query results.
func seenInUpdated(key string, updatedIssues []jira.Issue) bool {
	for _, issue := range updatedIssues {
		if issue.Key == key {
			return true
		}
	}
	return false
}
