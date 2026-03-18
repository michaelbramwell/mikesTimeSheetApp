package main

type Activity struct {
	Date        string // YYYY-MM-DD
	Time        string // HH:MM or HH:MM - HH:MM
	Source      string // Git, Jira, GitHub, Meeting, Chat
	Description string
}
