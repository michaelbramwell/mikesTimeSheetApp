package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/joho/godotenv"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
)

func main() {
	// Load .env file if present (existing env vars take precedence).
	// Errors are silently ignored so the app works fine when env vars are
	// injected directly (e.g. in CI or when the file doesn't exist yet).
	_ = godotenv.Load()

	noAzure := flag.Bool("noazure", false, "Disable Azure (Meetings/Chats) fetch")
	noGitHub := flag.Bool("nogithub", false, "Disable GitHub fetch")
	noJira := flag.Bool("nojira", false, "Disable Jira fetch")
	dryRun := flag.Bool("dry-run", false, "Print what would be posted to Projectworks without sending")
	flag.Parse()

	// Azure Credentials from Environment Variables
	tenantID := os.Getenv("AZURE_TENANT_ID")
	clientID := os.Getenv("AZURE_CLIENT_ID")

	// Jira Credentials from Environment Variables
	jiraURL := os.Getenv("JIRA_URL")
	jiraEmail := os.Getenv("JIRA_EMAIL")
	jiraToken := os.Getenv("JIRA_TOKEN")

	// Git configuration from Environment Variables
	gitAuthor := os.Getenv("GIT_AUTHOR")
	if gitAuthor == "" {
		log.Fatal("GIT_AUTHOR env var is required (e.g. 'Jane Smith')")
	}
	gitSearchDir := os.Getenv("GIT_SEARCH_DIR")
	if gitSearchDir == "" {
		log.Fatal("GIT_SEARCH_DIR env var is required (e.g. '~/dev/myorg')")
	}

	// Projectworks configuration from Environment Variables
	pwBaseURL := os.Getenv("PW_BASE_URL")
	if pwBaseURL == "" {
		log.Fatal("PW_BASE_URL env var is required (e.g. 'https://myorg.projectworksapp.com')")
	}
	pwTaskIDStr := os.Getenv("PW_TASK_ID")
	if pwTaskIDStr == "" {
		log.Fatal("PW_TASK_ID env var is required")
	}
	pwTaskID, err := strconv.Atoi(pwTaskIDStr)
	if err != nil {
		log.Fatalf("PW_TASK_ID must be a number, got %q", pwTaskIDStr)
	}

	// Determine Date Range Interactively
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := today.AddDate(0, 0, -1)

	offset := int(time.Monday - now.Weekday())
	if offset > 0 {
		offset = -6
	}
	thisMonday := today.AddDate(0, 0, offset)

	options := []struct {
		Label string
		Start time.Time
		End   time.Time
	}{
		{"Today", today, today},
		{"Yesterday", yesterday, yesterday},
		{"Monday", thisMonday, thisMonday},
		{"Tuesday", thisMonday.AddDate(0, 0, 1), thisMonday.AddDate(0, 0, 1)},
		{"Wednesday", thisMonday.AddDate(0, 0, 2), thisMonday.AddDate(0, 0, 2)},
		{"Thursday", thisMonday.AddDate(0, 0, 3), thisMonday.AddDate(0, 0, 3)},
		{"Friday", thisMonday.AddDate(0, 0, 4), thisMonday.AddDate(0, 0, 4)},
		{"This week", thisMonday, today},
		{"Last week", thisMonday.AddDate(0, 0, -7), thisMonday.AddDate(0, 0, -1)},
		{"Two weeks ago", thisMonday.AddDate(0, 0, -14), thisMonday.AddDate(0, 0, -8)},
	}

	fmt.Println("Select timesheet period:")
	for i, opt := range options {
		if opt.Start == opt.End {
			fmt.Printf("%d) %s (%s)\n", i+1, opt.Label, opt.Start.Format("2006-01-02"))
		} else {
			fmt.Printf("%d) %s (%s to %s)\n", i+1, opt.Label, opt.Start.Format("2006-01-02"), opt.End.Format("2006-01-02"))
		}
	}
	fmt.Print("Choice [1-10] (default 9): ")

	var choice int
	_, err = fmt.Scanln(&choice)
	if err != nil || choice < 1 || choice > 10 {
		choice = 9 // default to last week
	}

	selectedOpt := options[choice-1]
	startDateTime := selectedOpt.Start.Format("2006-01-02") + "T00:00:00Z"
	endDateTime := selectedOpt.End.Format("2006-01-02") + "T23:59:59Z"
	startDateStr := selectedOpt.Start.Format("2006-01-02")
	endDateStr := selectedOpt.End.Format("2006-01-02")

	fmt.Printf("\n=========================================\n")
	fmt.Printf(" TIMESHEET: %s to %s\n", startDateStr, endDateStr)
	fmt.Printf("=========================================\n\n")

	// All fetch functions are independent — run them concurrently.
	// The Azure block is a single goroutine that authenticates once then fans
	// out the three Graph API calls (Meetings, Chats, Emails) concurrently.
	var (
		mu            sync.Mutex
		allActivities []Activity
	)

	collect := func(activities []Activity) {
		mu.Lock()
		allActivities = append(allActivities, activities...)
		mu.Unlock()
	}

	var wg sync.WaitGroup

	// Git — local disk, always runs
	wg.Add(1)
	go func() {
		defer wg.Done()
		collect(fetchGitCommits(startDateStr, endDateStr, gitAuthor, gitSearchDir))
	}()

	// Jira
	if *noJira {
		fmt.Println("Skipping Jira fetch (-noJira flag set).")
	} else if jiraURL == "" || jiraEmail == "" || jiraToken == "" {
		fmt.Println("Jira credentials not set. Skipping Jira fetch.")
	} else {
		jiraStart := selectedOpt.Start.UTC().Format("2006-01-02 15:04")
		jiraEnd := selectedOpt.End.Add(24*time.Hour - time.Minute).UTC().Format("2006-01-02 15:04")
		wg.Add(1)
		go func() {
			defer wg.Done()
			collect(fetchJiraIssues(jiraURL, jiraEmail, jiraToken, jiraStart, jiraEnd, selectedOpt.Start, selectedOpt.End))
		}()
	}

	// GitHub
	if *noGitHub {
		fmt.Println("Skipping GitHub fetch (-noGitHub flag set).")
	} else if _, errGH := exec.LookPath("gh"); errGH != nil {
		fmt.Println("GitHub CLI ('gh') not found in PATH. Skipping GitHub fetch.")
	} else {
		wg.Add(1)
		go func() {
			defer wg.Done()
			collect(fetchGitHubActivity(startDateStr, endDateStr))
		}()
	}

	// Azure: auth once, then fan out Meetings + Chats + Emails concurrently
	if *noAzure {
		fmt.Println("Skipping Azure Graph API fetch (-noAzure flag set).")
	} else if tenantID == "" || clientID == "" {
		fmt.Println("Azure credentials not set. Skipping Graph API fetch (Meetings, Chats & Emails).")
	} else {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fmt.Println("Authenticating with Azure via browser...")
			cred, err := azidentity.NewInteractiveBrowserCredential(&azidentity.InteractiveBrowserCredentialOptions{
				TenantID: tenantID,
				ClientID: clientID,
			})
			if err != nil {
				log.Printf("Authentication failed: %v\n", err)
				return
			}
			scopes := []string{"Calendars.Read", "Chat.Read", "Mail.Read"}
			client, err := msgraphsdk.NewGraphServiceClientWithCredentials(cred, scopes)
			if err != nil {
				log.Printf("Failed to create Graph client: %v\n", err)
				return
			}
			ctx := context.Background()

			var graphWg sync.WaitGroup

			graphWg.Add(1)
			go func() {
				defer graphWg.Done()
				collect(fetchMeetings(ctx, client, startDateTime, endDateTime))
			}()

			graphWg.Add(1)
			go func() {
				defer graphWg.Done()
				collect(fetchChats(ctx, client, startDateTime, endDateTime))
			}()

			graphWg.Add(1)
			go func() {
				defer graphWg.Done()
				collect(fetchSentEmails(ctx, client, selectedOpt.Start, selectedOpt.End))
			}()

			graphWg.Wait()
		}()
	}

	wg.Wait()

	// Post to Projectworks
	cfg := pwConfig{
		BaseURL: pwBaseURL,
		Cookie:  os.Getenv("PW_COOKIE"),
		UserID:  os.Getenv("PW_USER_ID"),
		TaskID:  pwTaskID,
	}
	processAndPostActivities(allActivities, cfg, *dryRun, startDateStr, endDateStr)
}
