package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/models/odataerrors"
	graphusers "github.com/microsoftgraph/msgraph-sdk-go/users"
)

// chatTypeMeeting is the Microsoft Graph ChatType enum value for meeting chats.
// 0=oneOnOne, 1=group, 2=meeting, 3=unknownFutureValue
const chatTypeMeeting = 2

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

				activities = append(activities, Activity{
					Date:        dateStr,
					Time:        timeRange,
					Source:      "Meeting",
					Description: fmt.Sprintf("%s %s (%s)", timeRange, subject, formatMins(int(duration.Minutes()))),
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
			if chatTypeVal == chatTypeMeeting {
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
	var odataErr *odataerrors.ODataError
	if errors.As(err, &odataErr) {
		if terr := odataErr.GetErrorEscaped(); terr != nil && terr.GetMessage() != nil {
			fmt.Printf("Graph API Error: %s\n", *terr.GetMessage())
			return
		}
	}
	fmt.Printf("Error: %v\n", err)
}
