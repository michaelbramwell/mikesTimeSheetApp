# mikesTimeSheetApp — AI Context

## What This App Does

Personal timesheet automation tool. It:

1. Asks interactively which time period to process (Today, Yesterday, individual weekdays, This Week, Last Week, Two Weeks Ago)
2. Collects work activity from 6 sources across that period
3. Posts aggregated activities to Projectworks as timesheet entries — one per day, always 480 minutes (8h), with a rich comment

## Project Structure

```
main.go                  # Entry point + all data-fetching functions
models.go                # Activity struct (shared data type)
process_activities.go    # Activity processing + Projectworks posting
projectworks.go          # Projectworks HTTP client (scrape CSRF + POST)
main_test.go             # Unit tests
go.mod / go.sum          # Go 1.24 module
run.sh                   # GITIGNORED — contains all secrets/env vars
```

Patch files (`*.patch`) and `*.orig` / `*.new` snapshots are historical development artifacts.

## Core Data Type

```go
// models.go
type Activity struct {
    Date        string  // "YYYY-MM-DD"
    Time        string  // "HH:MM" or "HH:MM-HH:MM"
    Source      string  // "Git" | "Jira" | "GitHub" | "Meeting" | "Chat" | "Email"
    Description string
}
```

## Data Sources

| Source | Function | How |
|--------|----------|-----|
| Git commits | `fetchGitCommits(since, until, author, searchDir)` | Walks `GIT_SEARCH_DIR` (4 levels deep), runs `git log --author=GIT_AUTHOR` per repo |
| Jira issues | `fetchJiraIssues(...)` | JQL via Basic Auth against your `JIRA_URL` — finds issues updated in range where user is assignee/creator/watcher/reporter |
| GitHub PRs | `fetchGitHubActivity(...)` | `gh search prs` subprocess — raised + reviewed PRs |
| Meetings | `fetchMeetings(ctx, client, start, end)` | Microsoft Graph `CalendarView` — skips cancelled and "lunch" events |
| Teams chats | `fetchChats(ctx, client, start, end)` | Microsoft Graph — top 50 chats, top 50 messages per chat, deduped to one Activity per chat per day |
| Sent emails | `fetchSentEmails(ctx, client, start, end)` | Microsoft Graph — `SentItems` mail folder, filtered by date, up to 100 emails; one Activity per email ("Email to Name: Subject") |

## Projectworks Integration

- **Auth:** Session cookie (`PW_COOKIE` env var) scraped from browser — must be refreshed manually when it expires
- **CSRF:** `FetchPWContext()` scrapes `__RequestVerificationToken` from the timesheet HTML page
- **Existing entries:** Scrapes `<tr data-taskID=PW_TASK_ID>` + `data-cellDetails` JSON to detect existing entries (update vs. create)
- **POST:** `/Timesheet/SaveChanges` with `taskID=PW_TASK_ID`, `userID`, `minutes=480`, `comment`
- **Comment format:** `- [Source] Description` lines, truncated to 1000 chars; Jira tickets use `[TICKET-KEY]` instead of `[Jira]`
- **URL:** `PW_BASE_URL` (e.g. `https://yourorg.projectworksapp.com`)

## Environment Variables (set in `run.sh`)

| Var | Purpose |
|-----|---------|
| `AZURE_TENANT_ID` | Microsoft Entra tenant ID |
| `AZURE_CLIENT_ID` | Azure App Registration client ID |
| `JIRA_URL` | Your Jira instance URL (e.g. `https://yourorg.atlassian.net`) |
| `JIRA_EMAIL` | Your Atlassian account email |
| `JIRA_TOKEN` | Jira API token |
| `GIT_AUTHOR` | Your git author name (must match git commit author exactly) |
| `GIT_SEARCH_DIR` | Root directory to search for git repos (e.g. `~/dev/myorg`) |
| `PW_BASE_URL` | Projectworks base URL (e.g. `https://yourorg.projectworksapp.com`) |
| `PW_COOKIE` | Projectworks session cookie (expires, needs manual refresh) |
| `PW_USER_ID` | Your Projectworks user ID |
| `PW_TASK_ID` | Your Projectworks task ID |

## CLI Flags

| Flag | Effect |
|------|--------|
| `-noAzure` | Skip Meetings + Chats + Sent Emails (no browser OAuth popup) |
| `-noGitHub` | Skip GitHub PR search |
| `-noJira` | Skip Jira issue search |
| `-dry-run` | Print what would be posted; don't actually send to Projectworks |

## Known Issues / Open TODOs

### Timezone bug in Jira fetch (`main.go:511`)
The JQL query uses bare `YYYY-MM-DD` date strings with no timezone:
```go
jql := fmt.Sprintf(`updated >= "%s" AND updated <= "%s" ...`, startDateStr, endDateStr)
```
Jira interprets bare dates in its **server's** configured timezone, not the user's local timezone. If the Jira server is in a different TZ (e.g. UTC while Mike is in AWST/UTC+8), the effective query window is shifted — activity near the day boundaries can be missed or appear from the wrong day. Fix: append explicit UTC offset or use `>=  "YYYY-MM-DD HH:MM" timezone("Australia/Perth")` in JQL.

### Azure hardcodes UTC midnight for date range (`main.go:85-86`)
```go
startDateTime := selectedOpt.Start.Format("2006-01-02") + "T00:00:00Z"
endDateTime   := selectedOpt.End.Format("2006-01-02") + "T23:59:59Z"
```
The local date is used but the time is anchored to UTC (`Z`). For users in UTC+8 this shifts the window by 8 hours.

### Jira results capped at 50, no pagination (`main.go:514`)
Busy weeks with >50 updated issues will silently drop results.

### Projectworks cookie expires
`PW_COOKIE` is a raw browser session cookie. There is no refresh mechanism — when it expires the app silently fails to post (it detects the login redirect but just errors out).

## Azure / Microsoft Graph Auth

Uses `azidentity.NewInteractiveBrowserCredential` — opens a browser tab for OAuth on first run. Scopes: `Calendars.Read`, `Chat.Read`, `Mail.Read`. Controlled by `AZURE_TENANT_ID` + `AZURE_CLIENT_ID`.
