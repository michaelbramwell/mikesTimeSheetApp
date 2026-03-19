package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/andygrunwald/go-jira"
	"github.com/joho/godotenv"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/models/odataerrors"
	graphusers "github.com/microsoftgraph/msgraph-sdk-go/users"
)

// ticketRe matches Jira-style ticket keys like CDS-123 or PROJ-42.
var ticketRe = regexp.MustCompile(`\b([A-Z]+-\d+)\b`)

// commitEntry holds a parsed git commit for time estimation purposes.
type commitEntry struct {
	ts     time.Time
	date   string
	ticket string // first ticket key found in message, "" if none
}

// extractTicketKey returns the first Jira-style ticket key found in msg, or "".
func extractTicketKey(msg string) string {
	m := ticketRe.FindStringSubmatch(msg)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

// calcGitSessionMins estimates minutes spent given a sorted slice of commit
// timestamps. Rules:
//   - The first commit of each session gets 15 min of padding (pre-commit work).
//   - Consecutive commits ≤ sessionGap apart are in the same session; the gap
//     between them is counted as work time.
//   - A gap > sessionGap starts a new session (another 15 min padding).
func calcGitSessionMins(timestamps []time.Time) int {
	if len(timestamps) == 0 {
		return 0
	}
	sort.Slice(timestamps, func(i, j int) bool {
		return timestamps[i].Before(timestamps[j])
	})
	const sessionGap = 2 * time.Hour
	const padding = 15 // mins per session start
	total := padding   // first commit always gets padding
	for i := 1; i < len(timestamps); i++ {
		gap := timestamps[i].Sub(timestamps[i-1])
		if gap <= sessionGap {
			total += int(gap.Minutes())
		} else {
			total += padding // new session
		}
	}
	// Round up to nearest 15-minute increment (Projectworks requirement).
	if r := total % 15; r != 0 {
		total += 15 - r
	}
	return total
}

// formatMins renders a minute count as e.g. "15m", "1h", "1h30m".
func formatMins(mins int) string {
	h := mins / 60
	m := mins % 60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dm", m)
	}
}

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
	var pwTaskID int
	if _, err := fmt.Sscanf(pwTaskIDStr, "%d", &pwTaskID); err != nil {
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
	_, err := fmt.Scanln(&choice)
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

	// 5. Post to Projectworks
	cfg := PWConfig{
		BaseURL: pwBaseURL,
		Cookie:  os.Getenv("PW_COOKIE"),
		UserID:  os.Getenv("PW_USER_ID"),
		TaskID:  pwTaskID,
	}
	processAndPostActivities(allActivities, cfg, *dryRun)
}

func fetchGitCommits(since, until, author, searchDir string) []Activity {
	var activities []Activity
	fmt.Println("GIT COMMITS:")

	// git --until is exclusive (treats the value as midnight of that day), so
	// advance the end date by one day to include all commits on the `until` date.
	untilTime, err := time.ParseInLocation("2006-01-02", until, time.Local)
	if err == nil {
		until = untilTime.AddDate(0, 0, 1).Format("2006-01-02")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Error getting home dir: %v\n", err)
		return activities
	}

	// Expand leading ~ in searchDir
	if len(searchDir) >= 2 && searchDir[:2] == "~/" {
		searchDir = filepath.Join(homeDir, searchDir[2:])
	} else if searchDir == "~" {
		searchDir = homeDir
	}

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

	// allEntries collects every commit across all repos for time estimation.
	var allEntries []commitEntry

	totalCommits := 0
	for _, repoDir := range gitDirs {
		cmd := exec.Command("git", "-C", repoDir, "log", "--author="+author,
			fmt.Sprintf("--since=%s 00:00", since),
			fmt.Sprintf("--until=%s 00:00", until),
			"--format=%ad | %s", "--date=format:%Y-%m-%d %H:%M", "--all")

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

				parts := strings.SplitN(line, " | ", 2)
				date := ""
				timeStr := ""
				desc := line
				var ts time.Time
				if len(parts) == 2 {
					// parts[0] is "YYYY-MM-DD HH:MM"
					datetime := parts[0]
					if len(datetime) >= 16 {
						date = datetime[:10]
						timeStr = datetime[11:16]
						ts, _ = time.ParseInLocation("2006-01-02 15:04", datetime[:16], time.Local)
					} else {
						date = datetime
					}
					desc = fmt.Sprintf("[%s] %s %s", projectName, timeStr, parts[1])
					allEntries = append(allEntries, commitEntry{
						ts:     ts,
						date:   date,
						ticket: extractTicketKey(parts[1]),
					})
				} else {
					desc = fmt.Sprintf("[%s] %s", projectName, line)
				}
				activities = append(activities, Activity{
					Date:        date,
					Time:        timeStr,
					Source:      "Git",
					Description: desc,
				})

				totalCommits++
			}
		}
	}

	if totalCommits == 0 {
		fmt.Println("  No commits found in this period.")
	} else {
		// --- Real time estimation from commit timestamps ---

		// Group all commit timestamps by date.
		byDate := make(map[string][]time.Time)
		for _, e := range allEntries {
			if !e.ts.IsZero() {
				byDate[e.date] = append(byDate[e.date], e.ts)
			}
		}

		// Sort dates for consistent output.
		var dates []string
		for d := range byDate {
			dates = append(dates, d)
		}
		sort.Strings(dates)

		// dayMins maps date -> estimated minutes for that day.
		dayMins := make(map[string]int)
		grandTotalMins := 0
		fmt.Printf("\nTotal Commits: %d\nEstimated Git Time:\n", totalCommits)
		for _, d := range dates {
			mins := calcGitSessionMins(byDate[d])
			dayMins[d] = mins
			grandTotalMins += mins
			fmt.Printf("  %s: ~%s (%d commit(s))\n", d, formatMins(mins), len(byDate[d]))

			// Per-ticket breakdown for this day.
			ticketTs := make(map[string][]time.Time)
			for _, e := range allEntries {
				if e.date == d && e.ticket != "" && !e.ts.IsZero() {
					ticketTs[e.ticket] = append(ticketTs[e.ticket], e.ts)
				}
			}
			if len(ticketTs) > 0 {
				var tickets []string
				for k := range ticketTs {
					tickets = append(tickets, k)
				}
				sort.Strings(tickets)
				for _, tk := range tickets {
					tsMins := calcGitSessionMins(ticketTs[tk])
					fmt.Printf("    [%s]: ~%s (%d commit(s))\n", tk, formatMins(tsMins), len(ticketTs[tk]))
				}
			}
		}
		fmt.Printf("  Grand Total Git Time: ~%s\n", formatMins(grandTotalMins))

		// Stamp the day total on the first git Activity for each date;
		// subsequent commits on the same day get Minutes=0 so buildDayComment
		// sums correctly (the total is already represented once).
		firstSeen := make(map[string]bool)
		for i := range activities {
			d := activities[i].Date
			if !firstSeen[d] {
				firstSeen[d] = true
				activities[i].Minutes = dayMins[d]
			}
		}
	}
	fmt.Printf("-----------------------------------------\n")
	return activities
}

func fetchMeetings(ctx context.Context, client *msgraphsdk.GraphServiceClient, start, end string) []Activity {
	var activities []Activity
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
		return activities
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
				dateStr := startTime.Format("2006-01-02")
				timeRange := fmt.Sprintf("%s-%s", startTime.Format("15:04"), endTime.Format("15:04"))
				fmt.Printf("  - [%s %s] %-40s (%v)\n", dateStr, timeRange, subject, duration)

				// Skip lunch meetings
				if strings.Contains(strings.ToLower(subject), "lunch") {
					continue
				}

				// Format duration as e.g. "30m" or "1h30m"
				durationStr := ""
				h := int(duration.Hours())
				m := int(duration.Minutes()) % 60
				if h > 0 && m > 0 {
					durationStr = fmt.Sprintf("%dh%dm", h, m)
				} else if h > 0 {
					durationStr = fmt.Sprintf("%dh", h)
				} else {
					durationStr = fmt.Sprintf("%dm", m)
				}

				activities = append(activities, Activity{
					Date:        dateStr,
					Time:        timeRange,
					Source:      "Meeting",
					Description: fmt.Sprintf("%s %s (%s)", timeRange, subject, durationStr),
					Minutes:     int(duration.Minutes()),
				})
			}
		}
	}

	fmt.Printf("-----------------------------------------\n")
	fmt.Printf("Total Meeting Time: %v\n", totalDuration)
	return activities
}

func fetchChats(ctx context.Context, client *msgraphsdk.GraphServiceClient, start, end string) []Activity {
	var activities []Activity
	fmt.Printf("\nCHATS:\n")

	// Parse date range for filtering
	startTime, _ := time.Parse(time.RFC3339, start)
	endTime, _ := time.Parse(time.RFC3339, end)

	// Step 1: fetch all chats (non-meeting types), ordered by most recently updated
	top := int32(50)
	requestParameters := &graphusers.ItemChatsRequestBuilderGetRequestConfiguration{
		QueryParameters: &graphusers.ItemChatsRequestBuilderGetQueryParameters{
			Top:    &top,
			Select: []string{"id", "topic", "chatType", "lastUpdatedDateTime"},
		},
	}

	chatCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	chats, err := client.Me().Chats().Get(chatCtx, requestParameters)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Println("  - Fetching chats timed out.")
			return activities
		}
		printError(err)
		return activities
	}

	if chats.GetValue() == nil {
		fmt.Println("  - No active chats found.")
		return activities
	}

	// Step 2: for each non-meeting chat, look for messages in the date range
	// Track which chat+date combos we've already added to avoid duplicates
	seen := make(map[string]bool)
	matched := 0

	msgTop := int32(50)

	for _, chat := range chats.GetValue() {
		// Skip meeting chats — already captured via calendar
		// ChatType enum: 0=oneOnOne, 1=group, 2=meeting, 3=unknownFutureValue
		if chat.GetChatType() != nil {
			chatTypeVal := int(*chat.GetChatType())
			if chatTypeVal == 2 { // meeting
				continue
			}
		}

		if chat.GetId() == nil {
			continue
		}
		chatID := *chat.GetId()

		// Pre-filter: skip chats not updated within or shortly after the period
		// Allow up to 7 days after end to catch chats spanning period boundary
		if chat.GetLastUpdatedDateTime() != nil {
			if chat.GetLastUpdatedDateTime().Before(startTime) {
				continue
			}
		}

		topic := ""
		if chat.GetTopic() != nil && *chat.GetTopic() != "" {
			topic = *chat.GetTopic()
		} else if chat.GetChatType() != nil {
			chatTypeVal := int(*chat.GetChatType())
			if chatTypeVal == 0 {
				// 1:1 chat — fetch member names for a meaningful label
				membCtx, membCancel := context.WithTimeout(ctx, 5*time.Second)
				members, err := client.Me().Chats().ByChatId(chatID).Members().Get(membCtx, nil)
				membCancel()
				if err == nil && members != nil {
					var names []string
					for _, m := range members.GetValue() {
						if m.GetDisplayName() != nil && *m.GetDisplayName() != "" {
							names = append(names, *m.GetDisplayName())
						}
					}
					if len(names) > 0 {
						topic = strings.Join(names, ", ")
					} else {
						topic = "1:1 Chat"
					}
				} else {
					topic = "1:1 Chat"
				}
			} else {
				topic = "Group Chat"
			}
		} else {
			topic = "Chat"
		}

		// Fetch recent messages for this chat, filter client-side by date
		// (Graph API does not support $filter on createdDateTime for chat messages)
		msgCtx, msgCancel := context.WithTimeout(ctx, 10*time.Second)
		msgs, err := client.Me().Chats().ByChatId(chatID).Messages().Get(msgCtx, &graphusers.ItemChatsItemMessagesRequestBuilderGetRequestConfiguration{
			QueryParameters: &graphusers.ItemChatsItemMessagesRequestBuilderGetQueryParameters{
				Top: &msgTop,
			},
		})
		msgCancel()

		if err != nil || msgs == nil || len(msgs.GetValue()) == 0 {
			continue
		}

		// Group messages by date — one activity entry per day per chat
		for _, msg := range msgs.GetValue() {
			if msg.GetCreatedDateTime() == nil {
				continue
			}

			msgTime := *msg.GetCreatedDateTime()
			if msgTime.Before(startTime) || msgTime.After(endTime) {
				continue
			}

			dateStr := msgTime.Format("2006-01-02")
			key := chatID + "|" + dateStr
			if seen[key] {
				continue
			}
			seen[key] = true

			timeStr := msgTime.Format("15:04")
			fmt.Printf("    - [%s %s] %s\n", dateStr, timeStr, topic)

			activities = append(activities, Activity{
				Date:        dateStr,
				Time:        timeStr,
				Source:      "Chat",
				Description: fmt.Sprintf("%s %s", timeStr, topic),
				Minutes:     15,
			})
			matched++
		}
	}

	if matched == 0 {
		fmt.Println("  - No chats found in this period.")
	} else {
		const minsPerChat = 15
		totalEstMins := matched * minsPerChat
		fmt.Printf("  - Found %d chat activity/activities in this period (~%d mins each, ~%d mins total).\n", matched, minsPerChat, totalEstMins)
	}
	return activities
}

// formatEmailDescription builds the Activity description for a sent email.
func formatEmailDescription(to, subject string) string {
	if subject == "" {
		subject = "(no subject)"
	}
	if to == "" {
		to = "unknown"
	}
	return fmt.Sprintf("Email to %s: %s", to, subject)
}

func fetchSentEmails(ctx context.Context, client *msgraphsdk.GraphServiceClient, start, end time.Time) []Activity {
	var activities []Activity
	fmt.Println("\nSENT EMAILS:")

	// Build OData filter: sentDateTime within the local day boundaries (expressed as UTC)
	// We use the same UTC-midnight approach as the rest of the Azure fetches.
	startFilter := start.Format("2006-01-02") + "T00:00:00Z"
	endFilter := end.Format("2006-01-02") + "T23:59:59Z"
	filter := fmt.Sprintf("sentDateTime ge %s and sentDateTime le %s", startFilter, endFilter)

	top := int32(100)
	orderby := []string{"sentDateTime asc"}
	selectFields := []string{"subject", "sentDateTime", "toRecipients"}

	mailCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	msgs, err := client.Me().MailFolders().ByMailFolderId("SentItems").Messages().Get(mailCtx, &graphusers.ItemMailFoldersItemMessagesRequestBuilderGetRequestConfiguration{
		QueryParameters: &graphusers.ItemMailFoldersItemMessagesRequestBuilderGetQueryParameters{
			Filter:  &filter,
			Top:     &top,
			Orderby: orderby,
			Select:  selectFields,
		},
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Println("  - Fetching sent emails timed out.")
			return activities
		}
		printError(err)
		return activities
	}

	if msgs == nil || len(msgs.GetValue()) == 0 {
		fmt.Println("  - No sent emails found in this period.")
		return activities
	}

	for _, msg := range msgs.GetValue() {
		subject := "(no subject)"
		if msg.GetSubject() != nil && *msg.GetSubject() != "" {
			subject = *msg.GetSubject()
		}

		sentAt := time.Time{}
		if msg.GetSentDateTime() != nil {
			sentAt = *msg.GetSentDateTime()
		}
		dateStr := sentAt.Format("2006-01-02")
		timeStr := sentAt.Format("15:04")

		// Build recipient list
		var recipients []string
		for _, r := range msg.GetToRecipients() {
			if r.GetEmailAddress() != nil && r.GetEmailAddress().GetName() != nil {
				recipients = append(recipients, *r.GetEmailAddress().GetName())
			}
		}
		toStr := strings.Join(recipients, ", ")
		if toStr == "" {
			toStr = "unknown"
		}

		desc := formatEmailDescription(toStr, subject)
		fmt.Printf("  - [%s %s] %s\n", dateStr, timeStr, desc)

		activities = append(activities, Activity{
			Date:        dateStr,
			Time:        timeStr,
			Source:      "Email",
			Description: desc,
			Minutes:     15,
		})
	}

	fmt.Printf("  - Found %d sent email(s) in this period (~15 mins each, ~%d mins total).\n", len(activities), len(activities)*15)
	fmt.Printf("-----------------------------------------\n")
	return activities
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
				t, _ := time.Parse(time.RFC3339, issue.CreatedAt)
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
				t, _ := time.Parse(time.RFC3339, issue.UpdatedAt)
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
