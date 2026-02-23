package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/models/odataerrors"
	graphusers "github.com/microsoftgraph/msgraph-sdk-go/users"
)

func main() {
	// Plug these in once you create the App Registration in Azure
	tenantID := "5f17a163-9197-454b-b84d-b500694da509"
	clientID := "db867cf2-0f35-4409-9ea3-f02cbd753881"

	startDateTime := "2026-02-16T00:00:00Z"
	endDateTime := "2026-02-23T00:00:00Z"
	startDateStr := "2026-02-16"
	endDateStr := "2026-02-23"

	fmt.Printf("\n=========================================\n")
	fmt.Printf(" TIMESHEET: %s to %s\n", startDateStr, endDateStr)
	fmt.Printf("=========================================\n\n")

	// 1. Fetch Git Commits (Doesn't require Azure Auth)
	fetchGitCommits(startDateStr, endDateStr)

	// 2. Authenticate with Azure
	if tenantID == "YOUR_TENANT_ID" || clientID == "YOUR_CLIENT_ID" {
		fmt.Println("\nAzure credentials not set. Please update YOUR_TENANT_ID and YOUR_CLIENT_ID in main.go.")
		fmt.Println("Skipping Graph API fetch (Meetings & Chats).")
		return
	}

	fmt.Println("\nAuthenticating with Azure via browser...")
	cred, err := azidentity.NewInteractiveBrowserCredential(&azidentity.InteractiveBrowserCredentialOptions{
		TenantID: tenantID,
		ClientID: clientID,
	})
	if err != nil {
		log.Printf("Authentication failed: %v\n", err)
		return
	}

	scopes := []string{"Calendars.Read", "Chat.Read"}
	client, err := msgraphsdk.NewGraphServiceClientWithCredentials(cred, scopes)
	if err != nil {
		log.Printf("Failed to create Graph client: %v\n", err)
		return
	}

	ctx := context.Background()

	// 3. Fetch Meetings
	fetchMeetings(ctx, client, startDateTime, endDateTime)

	// 4. Fetch Chats
	fetchChats(ctx, client)
}

func fetchGitCommits(since, until string) {
	fmt.Println("GIT COMMITS:")
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Error getting home dir: %v\n", err)
		return
	}

	searchDir := filepath.Join(homeDir, "dev", "diversus")

	// Find all .git directories up to 3 levels deep
	var gitDirs []string
	filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && info.Name() == ".git" {
			gitDirs = append(gitDirs, filepath.Dir(path))
			return filepath.SkipDir
		}

		// Optimization: Don't go too deep
		rel, _ := filepath.Rel(searchDir, path)
		if strings.Count(rel, string(os.PathSeparator)) > 4 {
			return filepath.SkipDir
		}
		return nil
	})

	totalCommits := 0
	for _, repoDir := range gitDirs {
		cmd := exec.Command("git", "-C", repoDir, "log", "--author=Mike Bramwell",
			fmt.Sprintf("--since=%s", since),
			fmt.Sprintf("--until=%s", until),
			"--format=%ad | %s", "--date=short", "--all")

		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		if err != nil {
			continue
		}

		output := strings.TrimSpace(out.String())
		if output != "" {
			projectName := filepath.Base(repoDir)
			fmt.Printf("\nProject: %s\n", projectName)
			lines := strings.Split(output, "\n")
			for _, line := range lines {
				fmt.Printf("  - %s\n", line)
				totalCommits++
			}
		}
	}

	if totalCommits == 0 {
		fmt.Println("  No commits found in this period.")
	} else {
		fmt.Printf("\nTotal Commits: %d\n", totalCommits)
	}
	fmt.Printf("-----------------------------------------\n")
}

func fetchMeetings(ctx context.Context, client *msgraphsdk.GraphServiceClient, start, end string) {
	fmt.Println("\nMEETINGS:")

	requestParameters := &graphusers.ItemCalendarViewRequestBuilderGetRequestConfiguration{
		QueryParameters: &graphusers.ItemCalendarViewRequestBuilderGetQueryParameters{
			StartDateTime: &start,
			EndDateTime:   &end,
			Select:        []string{"subject", "start", "end", "isCancelled"},
			Orderby:       []string{"start/dateTime"},
		},
	}

	events, err := client.Me().CalendarView().Get(ctx, requestParameters)
	if err != nil {
		printError(err)
		return
	}

	var totalDuration time.Duration

	if events.GetValue() == nil || len(events.GetValue()) == 0 {
		fmt.Println("  No meetings found.")
	} else {
		for _, event := range events.GetValue() {
			if event.GetIsCancelled() != nil && *event.GetIsCancelled() {
				continue
			}

			subject := ""
			if event.GetSubject() != nil {
				subject = *event.GetSubject()
			}

			startStr, endStr := "", ""
			if event.GetStart() != nil && event.GetStart().GetDateTime() != nil {
				startStr = *event.GetStart().GetDateTime() + "Z"
			}
			if event.GetEnd() != nil && event.GetEnd().GetDateTime() != nil {
				endStr = *event.GetEnd().GetDateTime() + "Z"
			}

			startTime, err1 := time.Parse(time.RFC3339Nano, startStr)
			endTime, err2 := time.Parse(time.RFC3339Nano, endStr)

			if err1 == nil && err2 == nil {
				duration := endTime.Sub(startTime)
				totalDuration += duration
				fmt.Printf("  - %-40s : %v\n", subject, duration)
			}
		}
	}

	fmt.Printf("-----------------------------------------\n")
	fmt.Printf("Total Meeting Time: %v\n", totalDuration)
}

func fetchChats(ctx context.Context, client *msgraphsdk.GraphServiceClient) {
	fmt.Printf("\nCHATS:\n")

	top := int32(10)
	requestParameters := &graphusers.ItemChatsRequestBuilderGetRequestConfiguration{
		QueryParameters: &graphusers.ItemChatsRequestBuilderGetQueryParameters{
			Top: &top,
		},
	}

	chatCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	chats, err := client.Me().Chats().Get(chatCtx, requestParameters)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Println("  - Fetching chats timed out after 10 seconds.")
			return
		}
		printError(err)
		return
	}

	if chats.GetValue() != nil {
		fmt.Printf("  - Found %d active chat threads recently.\n", len(chats.GetValue()))
	} else {
		fmt.Println("  - No active chats found.")
	}
}

func printError(err error) {
	if odataErr, ok := err.(*odataerrors.ODataError); ok {
		if terr := odataErr.GetErrorEscaped(); terr != nil && terr.GetMessage() != nil {
			fmt.Printf("Graph API Error: %s\n", *terr.GetMessage())
			return
		}
	}
	fmt.Printf("Error: %v\n", err)
}
