# mikesTimeSheetApp

Personal timesheet automation tool. Collects work activity from Git, Jira, GitHub PRs, Outlook calendar, Teams chats, and sent emails — then posts daily timesheet entries to Projectworks (8h/day).

## Prerequisites

- Go 1.24+
- `gh` CLI (for GitHub PR fetching)
- Git

## Setup

```sh
cp .env.template .env
# Edit .env and fill in all values (see comments in the file)
```

## Build & Run

```sh
go build -o mikesTimeSheetApp .
./mikesTimeSheetApp
```

Or run directly:

```sh
go run .
```

The app will prompt you to select a time period, then collect activity and post to Projectworks. Microsoft Graph sources (calendar, Teams, email) will open a browser tab for OAuth on first run.

## CLI Flags

| Flag | Effect |
|------|--------|
| `-noAzure` | Skip calendar, Teams chats, and sent emails (no browser OAuth popup) |
| `-noGitHub` | Skip GitHub PR search |
| `-noJira` | Skip Jira issue search |
| `-dry-run` | Print what would be posted without sending to Projectworks |

## Tests

```sh
go test ./...
```

## Notes

- `PW_COOKIE` is a browser session cookie — copy it from DevTools after logging into Projectworks. It expires and must be refreshed manually.
- `GIT_AUTHOR` must exactly match your git commit author name (`git log --format='%an'`).
- `GIT_SEARCH_DIR` is walked 4 levels deep to find git repos.
