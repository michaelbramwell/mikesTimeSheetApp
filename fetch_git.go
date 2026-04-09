package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
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

func fetchGitCommits(since, until, author, searchDir string, searchDepth int) []Activity {
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

	// Find all .git directories up to searchDepth levels deep.
	var gitDirs []string
	if err := filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && info.Name() == ".git" {
			gitDirs = append(gitDirs, filepath.Dir(path))
			return filepath.SkipDir
		}

		// Optimization: Don't go too deep
		rel, _ := filepath.Rel(searchDir, path)
		if strings.Count(rel, string(os.PathSeparator)) >= searchDepth {
			return filepath.SkipDir
		}
		return nil
	}); err != nil {
		fmt.Printf("Warning: git search dir walk failed: %v\n", err)
	}

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

	// --- GitReview: commits by others in the same repos during the same period ---
	activities = append(activities, fetchGitReviews(since, until, author, gitDirs)...)

	return activities
}

// fetchGitReviews scans the already-discovered gitDirs for commits NOT authored
// by the given author during the same date window. These represent work that
// was pushed by teammates and that the current user may have reviewed or merged.
func fetchGitReviews(since, until, author string, gitDirs []string) []Activity {
	var activities []Activity
	fmt.Println("GIT REVIEWS (commits by others in your repos):")

	totalReviews := 0
	for _, repoDir := range gitDirs {
		// Exclude commits by the current author to get teammate commits only.
		cmd := exec.Command("git", "-C", repoDir, "log",
			fmt.Sprintf("--since=%s 00:00", since),
			fmt.Sprintf("--until=%s 00:00", until),
			"--format=%ad | %an | %s", "--date=format:%Y-%m-%d %H:%M", "--all",
			"--invert-grep", "--grep=^Merge branch", // skip bare merge commits
		)

		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			continue
		}

		output := strings.TrimSpace(out.String())
		if output == "" {
			continue
		}

		projectName := filepath.Base(repoDir)
		lines := strings.Split(output, "\n")

		var reviewLines []string
		for _, line := range lines {
			parts := strings.SplitN(line, " | ", 3)
			if len(parts) < 3 {
				continue
			}
			commitAuthor := strings.TrimSpace(parts[1])
			// Skip commits by the current user — those are already captured above.
			if strings.EqualFold(commitAuthor, author) {
				continue
			}

			datetime := parts[0]
			subject := parts[2]
			date := ""
			timeStr := ""
			if len(datetime) >= 16 {
				date = datetime[:10]
				timeStr = datetime[11:16]
			} else {
				date = datetime
			}

			desc := fmt.Sprintf("[%s] reviewed: %s (%s) %s", projectName, subject, commitAuthor, timeStr)
			reviewLines = append(reviewLines, fmt.Sprintf("  - %s", desc))
			activities = append(activities, Activity{
				Date:        date,
				Time:        timeStr,
				Source:      "GitReview",
				Description: desc,
				Minutes:     15, // flat estimate per reviewed commit
			})
			totalReviews++
		}

		if len(reviewLines) > 0 {
			fmt.Printf("\nProject: %s\n", projectName)
			for _, l := range reviewLines {
				fmt.Println(l)
			}
		}
	}

	if totalReviews == 0 {
		fmt.Println("  No teammate commits found in this period.")
	} else {
		fmt.Printf("\nTotal teammate commits found: %d (flat ~15m each for review estimate)\n", totalReviews)
	}
	fmt.Printf("-----------------------------------------\n")
	return activities
}
