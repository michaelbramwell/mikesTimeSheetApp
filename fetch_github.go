package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// GHPR is the JSON model for a single result from `gh search prs`.
type GHPR struct {
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Number    int    `json:"number"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func fetchGitHubActivity(startDateStr, endDateStr string) []Activity {
	var activities []Activity
	fmt.Println("\nGITHUB ACTIVITY:")

	raisedCount := 0
	reviewedCount := 0

	// Fetch PRs created by the user
	authorCmd := exec.Command("gh", "search", "prs", "--author=@me", fmt.Sprintf("--created=%s..%s", startDateStr, endDateStr), "--json", "repository,number,title,url,createdAt")
	var authorOut bytes.Buffer
	authorCmd.Stdout = &authorOut
	err := authorCmd.Run()
	if err != nil {
		fmt.Printf("  Error fetching authored PRs using gh cli: %v\n", err)
	} else {
		var authored []GHPR
		if err := json.Unmarshal(authorOut.Bytes(), &authored); err != nil {
			fmt.Printf("  Error parsing authored PRs: %v\n", err)
		} else if len(authored) > 0 {
			fmt.Printf("  Raised %d PR(s):\n", len(authored))
			for _, issue := range authored {
				t, err := time.Parse(time.RFC3339, issue.CreatedAt)
				if err != nil {
					fmt.Printf("  Warning: skipping PR #%d, bad createdAt %q: %v\n", issue.Number, issue.CreatedAt, err)
					continue
				}
				fmt.Printf("  - [%s] [%s] #%d %s (%s)\n", t.Format("2006-01-02 15:04"), issue.Repository.NameWithOwner, issue.Number, issue.Title, issue.URL)

				activities = append(activities, Activity{
					Date:        t.Format("2006-01-02"),
					Time:        t.Format("15:04"),
					Source:      "GitHub",
					Description: fmt.Sprintf("Raised PR #%d: %s", issue.Number, issue.Title),
					Minutes:     60,
				})
			}
			raisedCount = len(authored)
		} else {
			fmt.Println("  No PRs raised.")
		}
	}

	// Fetch PRs commented on or reviewed by the user
	commentCmd := exec.Command("gh", "search", "prs", "--commenter=@me", fmt.Sprintf("--updated=%s..%s", startDateStr, endDateStr), "--json", "repository,number,title,url,updatedAt", "--", "-author:@me")
	var commentOut bytes.Buffer
	commentCmd.Stdout = &commentOut
	err = commentCmd.Run()
	if err != nil {
		fmt.Printf("  Error fetching commented PRs using gh cli: %v\n", err)
	} else {
		var commented []GHPR
		if err := json.Unmarshal(commentOut.Bytes(), &commented); err != nil {
			fmt.Printf("  Error parsing commented PRs: %v\n", err)
		} else if len(commented) > 0 {
			fmt.Printf("  \n  Commented/Reviewed %d PR(s):\n", len(commented))
			for _, issue := range commented {
				t, err := time.Parse(time.RFC3339, issue.UpdatedAt)
				if err != nil {
					fmt.Printf("  Warning: skipping PR #%d, bad updatedAt %q: %v\n", issue.Number, issue.UpdatedAt, err)
					continue
				}
				fmt.Printf("  - [%s] [%s] #%d %s (%s)\n", t.Format("2006-01-02 15:04"), issue.Repository.NameWithOwner, issue.Number, issue.Title, issue.URL)

				activities = append(activities, Activity{
					Date:        t.Format("2006-01-02"),
					Time:        t.Format("15:04"),
					Source:      "GitHub",
					Description: fmt.Sprintf("Reviewed PR #%d: %s", issue.Number, issue.Title),
					Minutes:     30,
				})
			}
			reviewedCount = len(commented)
		} else {
			fmt.Println("  \n  No PRs commented on/reviewed.")
		}
	}

	const minsPerRaisedPR = 60
	const minsPerReviewedPR = 30
	totalEstMins := raisedCount*minsPerRaisedPR + reviewedCount*minsPerReviewedPR
	if raisedCount > 0 || reviewedCount > 0 {
		fmt.Printf("  Estimated: %d raised (~%d mins each) + %d reviewed (~%d mins each) = ~%d mins total\n",
			raisedCount, minsPerRaisedPR, reviewedCount, minsPerReviewedPR, totalEstMins)
	}
	fmt.Printf("-----------------------------------------\n")
	return activities
}
