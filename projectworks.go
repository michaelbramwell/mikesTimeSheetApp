package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const pwActionSave = 2

type pwConfig struct {
	BaseURL string
	Cookie  string
	UserID  string
	TaskID  int
}

func parseDateToWeekStart(dateStr string) (string, error) {
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return "", fmt.Errorf("parseDateToWeekStart: invalid date %q: %w", dateStr, err)
	}
	offset := int(time.Monday - t.Weekday())
	if offset > 0 {
		offset = -6
	}
	startOfWeek := t.AddDate(0, 0, offset)
	return startOfWeek.Format("2006-01-02"), nil
}

func fetchPWContext(cfg pwConfig, dateStr string) (string, map[string]int, error) {
	weekStart, err := parseDateToWeekStart(dateStr)
	if err != nil {
		return "", nil, err
	}
	url := fmt.Sprintf("%s/Timesheet/Timesheet?userID=%s&window=week%%3B%s", cfg.BaseURL, cfg.UserID, weekStart)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", nil, fmt.Errorf("building PW GET request: %w", err)
	}
	req.Header.Set("Cookie", cfg.Cookie)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)")
	req.Header.Set("Accept", "text/html, */*; q=0.01")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Capture redirects — a redirect to /Login indicates an expired cookie
			if strings.Contains(req.URL.Path, "/Login") || strings.Contains(req.URL.Path, "/login") {
				return fmt.Errorf("redirected to login page")
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "redirected to login page") {
			return "", nil, fmt.Errorf("PW_COOKIE has expired — copy a fresh cookie from your browser's DevTools (Network tab) and update PW_COOKIE in run.sh")
		}
		return "", nil, fmt.Errorf("error fetching PW context: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("error reading PW context body: %v", err)
	}
	html := string(bodyBytes)

	// 1. Extract RequestVerificationToken
	tokenRegex := regexp.MustCompile(`<input name="__RequestVerificationToken" type="hidden" value="([^"]+)" />`)
	tokenMatch := tokenRegex.FindStringSubmatch(html)
	if len(tokenMatch) < 2 {
		// If the page looks like a login page, the cookie has expired
		if strings.Contains(html, "login") || strings.Contains(html, "Login") || strings.Contains(html, "password") {
			return "", nil, fmt.Errorf("PW_COOKIE has expired — copy a fresh cookie from your browser's DevTools (Network tab) and update PW_COOKIE in run.sh")
		}
		return "", nil, fmt.Errorf("could not find RequestVerificationToken in Projectworks response")
	}
	token := tokenMatch[1]

	// 2. Find existing entries for the target task.
	// We look for tr with data-taskID matching cfg.TaskID
	taskRegexStr := fmt.Sprintf(`data-taskID="%d".*?</tr>`, cfg.TaskID)
	taskRegex := regexp.MustCompile("(?s)" + taskRegexStr)
	taskMatch := taskRegex.FindString(html)

	existingEntries := make(map[string]int)

	if taskMatch != "" {
		cellRegex := regexp.MustCompile(`data-cellDetails='([^']+)'`)
		cellMatches := cellRegex.FindAllStringSubmatch(taskMatch, -1)

		for _, match := range cellMatches {
			var cellDetails struct {
				Date            string `json:"date"`
				UserTaskHoursID *int   `json:"userTaskHoursID"`
			}
			jsonStr := strings.ReplaceAll(match[1], "&quot;", "\"")
			if err := json.Unmarshal([]byte(jsonStr), &cellDetails); err == nil {
				if cellDetails.UserTaskHoursID != nil {
					dateOnly := cellDetails.Date[:10] // "2026-02-16T00:00:00" -> "2026-02-16"
					existingEntries[dateOnly] = *cellDetails.UserTaskHoursID
				}
			}
		}
	}

	return token, existingEntries, nil
}

func postPWTimeEntry(cfg pwConfig, token string, dateStr string, minutes int, comment string, existingID *int) error {
	url := cfg.BaseURL + "/Timesheet/SaveChanges"

	payload := map[string]interface{}{
		"taskID":               cfg.TaskID,
		"userID":               cfg.UserID,
		"action":               pwActionSave,
		"userTaskHourID":       existingID,
		"editDate":             dateStr,
		"originalMinutes":      0,
		"originalComment":      "",
		"originalCustomValues": map[string]interface{}{},
		"minutes":              minutes,
		"comment":              comment,
		"customValues":         map[string]interface{}{},
	}
	if existingID != nil {
		payload["originalMinutes"] = minutes // Might be ignored but good to set
		payload["originalComment"] = ""      // Or whatever was there
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshalling PW payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("building PW POST request: %w", err)
	}
	req.Header.Set("Cookie", cfg.Cookie)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("RequestVerificationToken", token)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	return nil
}
