package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Event struct {
	Timestamp time.Time
	Type      string
}

type Session struct {
	Start    time.Time
	End      time.Time
	Duration time.Duration
	Type     string
}

func main() {
	fmt.Println("=== Computer Boot and Shutdown History ===")
	fmt.Println()

	events, err := getSystemEvents()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(events) == 0 {
		fmt.Println("No system events found.")
		return
	}

	sessions := calculateSessions(events)

	if len(sessions) == 0 {
		fmt.Println("Cannot calculate work sessions.")
		return
	}

	displaySessions(sessions)
	displaySummary(sessions)
}

func getSystemEvents() ([]Event, error) {
	// First, get the list of all boots with timestamps
	bootCmd := exec.Command("journalctl", "--list-boots", "--no-pager", "--output=short-iso")
	bootOutput, err := bootCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("cannot read boot list: %v", err)
	}

	events := []Event{}

	// Parse each boot from --list-boots
	bootScanner := bufio.NewScanner(strings.NewReader(string(bootOutput)))
	bootScanner.Scan() // Skip header

	bootInfos := []struct {
		ID        string
		StartTime time.Time
		EndTime   time.Time
	}{}

	for bootScanner.Scan() {
		line := bootScanner.Text()
		// Format: IDX BOOT_ID FIRST_ENTRY LAST_ENTRY
		// Example: -10 3460c36536374bb48bb910bae80c34b6 Tue 2025-10-28 16:28:42 CET Thu 2025-10-30 00:14:40 CET

		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 3 {
			continue
		}

		bootID := parts[0]

		// Find separator between dates (usually "—" or several spaces)
		// We're looking for pattern: date + time + timezone, then next date
		dateRegex := regexp.MustCompile(`(\w{3} \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} \w+)`)
		dates := dateRegex.FindAllString(line, -1)

		if len(dates) >= 2 {
			// Parse start time
			startTime, err := time.Parse("Mon 2006-01-02 15:04:05 MST", dates[0])
			if err != nil {
				continue
			}

			// Parse end time
			endTime, err := time.Parse("Mon 2006-01-02 15:04:05 MST", dates[1])
			if err != nil {
				continue
			}

			bootInfos = append(bootInfos, struct {
				ID        string
				StartTime time.Time
				EndTime   time.Time
			}{
				ID:        bootID,
				StartTime: startTime,
				EndTime:   endTime,
			})

			// Add boot event
			events = append(events, Event{
				Timestamp: startTime,
				Type:      "boot",
			})

			// Add shutdown event (if boot has ended)
			// Check if this is not the current boot
			if endTime.Before(time.Now().Add(-1 * time.Minute)) {
				events = append(events, Event{
					Timestamp: endTime,
					Type:      "shutdown",
				})
			}
		}
	}

	// Now try to detect suspend/resume for all boots
	suspendEvents := detectSuspendResume("")
	events = append(events, suspendEvents...)

	// Sort chronologically
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.Before(events[j].Timestamp)
	})

	// Remove duplicates
	events = deduplicateEvents(events)

	return events, nil
}

func detectSuspendResume(bootID string) []Event {
	events := []Event{}

	timestampRegex := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[+-]\d{2}:\d{2})`)

	// Use journalctl to find suspend events
	var cmd *exec.Cmd
	if bootID == "" {
		cmd = exec.Command("journalctl", "--no-pager", "-o", "short-iso", "-u", "systemd-suspend.service")
	} else {
		cmd = exec.Command("journalctl", "-b", bootID, "--no-pager", "-o", "short-iso", "-u", "systemd-suspend.service")
	}

	output, err := cmd.CombinedOutput()

	if err == nil && len(output) > 0 {
		scanner := bufio.NewScanner(strings.NewReader(string(output)))

		for scanner.Scan() {
			line := scanner.Text()

			// Filter only lines with "System Suspend"
			if !strings.Contains(line, "System Suspend") {
				continue
			}

			matches := timestampRegex.FindStringSubmatch(line)
			if len(matches) < 2 {
				continue
			}

			timestampStr := matches[1]
			timestamp, err := time.Parse("2006-01-02T15:04:05-07:00", timestampStr)
			if err != nil {
				continue
			}

			eventType := ""

			// Suspend - "Starting System Suspend"
			if strings.Contains(line, "Starting System Suspend") {
				eventType = "suspend"
			}

			// Resume - "Finished System Suspend"
			if strings.Contains(line, "Finished System Suspend") {
				eventType = "resume"
			}

			if eventType != "" {
				events = append(events, Event{
					Timestamp: timestamp,
					Type:      eventType,
				})
			}
		}
	}

	// Check hibernate too
	if bootID == "" {
		cmd = exec.Command("journalctl", "--no-pager", "-o", "short-iso", "-u", "systemd-hibernate.service")
	} else {
		cmd = exec.Command("journalctl", "-b", bootID, "--no-pager", "-o", "short-iso", "-u", "systemd-hibernate.service")
	}

	output, err = cmd.CombinedOutput()
	if err == nil && len(output) > 0 {
		scanner := bufio.NewScanner(strings.NewReader(string(output)))

		for scanner.Scan() {
			line := scanner.Text()

			// Filter only lines with "System Hibernate"
			if !strings.Contains(line, "System Hibernate") {
				continue
			}

			matches := timestampRegex.FindStringSubmatch(line)
			if len(matches) < 2 {
				continue
			}

			timestampStr := matches[1]
			timestamp, err := time.Parse("2006-01-02T15:04:05-07:00", timestampStr)
			if err != nil {
				continue
			}

			eventType := ""

			// Hibernate
			if strings.Contains(line, "Starting System Hibernate") {
				eventType = "hibernate"
			}

			// Wake from hibernate
			if strings.Contains(line, "Finished System Hibernate") {
				eventType = "resume"
			}

			if eventType != "" {
				events = append(events, Event{
					Timestamp: timestamp,
					Type:      eventType,
				})
			}
		}
	}

	return events
}

func deduplicateEvents(events []Event) []Event {
	if len(events) == 0 {
		return events
	}

	result := []Event{events[0]}

	for i := 1; i < len(events); i++ {
		lastEvent := result[len(result)-1]
		currentEvent := events[i]

		// If the same event within 2 minutes, ignore
		if currentEvent.Type == lastEvent.Type &&
			currentEvent.Timestamp.Sub(lastEvent.Timestamp) < 2*time.Minute {
			continue
		}

		result = append(result, currentEvent)
	}

	return result
}

func calculateSessions(events []Event) []Session {
	sessions := []Session{}

	var sessionStart *Event
	var sessionType string

	for i := 0; i < len(events); i++ {
		event := events[i]

		switch event.Type {
		case "boot", "resume":
			// A new activity session begins
			if sessionStart != nil {
				// Close previous session (was improperly terminated)
				sessions = append(sessions, Session{
					Start:    sessionStart.Timestamp,
					End:      event.Timestamp,
					Duration: event.Timestamp.Sub(sessionStart.Timestamp),
					Type:     sessionType + " → " + event.Type,
				})
			}
			sessionStart = &event
			if event.Type == "boot" {
				sessionType = "boot"
			} else {
				sessionType = "resume"
			}

		case "shutdown", "suspend", "hibernate":
			// Activity session ends
			if sessionStart != nil {
				endType := ""
				switch event.Type {
				case "shutdown":
					endType = "shutdown"
				case "suspend":
					endType = "suspend"
				case "hibernate":
					endType = "hibernate"
				}

				sessions = append(sessions, Session{
					Start:    sessionStart.Timestamp,
					End:      event.Timestamp,
					Duration: event.Timestamp.Sub(sessionStart.Timestamp),
					Type:     sessionType + " → " + endType,
				})
				sessionStart = nil
				sessionType = ""
			}
		}
	}

	// If there's an open session, mark as "still active"
	if sessionStart != nil {
		now := time.Now()
		sessions = append(sessions, Session{
			Start:    sessionStart.Timestamp,
			End:      now,
			Duration: now.Sub(sessionStart.Timestamp),
			Type:     sessionType + " → (still active)",
		})
	}

	return sessions
}

func displaySessions(sessions []Session) {
	fmt.Println("Computer work sessions:")
	fmt.Println()
	fmt.Printf("%-25s | %-25s | %-20s | %s\n", "Start", "End", "Uptime", "Type")
	fmt.Println(strings.Repeat("-", 110))

	for _, session := range sessions {
		fmt.Printf("%-25s | %-25s | %-20s | %s\n",
			session.Start.Format("2006-01-02 15:04:05"),
			session.End.Format("2006-01-02 15:04:05"),
			formatDuration(session.Duration),
			session.Type,
		)
	}
	fmt.Println()
}

func displaySummary(sessions []Session) {
	if len(sessions) == 0 {
		return
	}

	totalDuration := time.Duration(0)
	for _, session := range sessions {
		totalDuration += session.Duration
	}

	avgDuration := totalDuration / time.Duration(len(sessions))

	fmt.Println("\n=== Summary ===")
	fmt.Printf("Number of sessions: %d\n", len(sessions))
	fmt.Printf("Total uptime: %s\n", formatDuration(totalDuration))
	fmt.Printf("Average session time: %s\n", formatDuration(avgDuration))

	// Longest and shortest session
	var longest, shortest Session
	if len(sessions) > 0 {
		longest = sessions[0]
		shortest = sessions[0]

		for _, session := range sessions[1:] {
			if session.Duration > longest.Duration {
				longest = session
			}
			if session.Duration < shortest.Duration {
				shortest = session
			}
		}

		fmt.Printf("\nLongest session: %s (%s)\n",
			formatDuration(longest.Duration),
			longest.Start.Format("2006-01-02 15:04"),
		)
		fmt.Printf("Shortest session: %s (%s)\n",
			formatDuration(shortest.Duration),
			shortest.Start.Format("2006-01-02 15:04"),
		)
	}
}

func formatDuration(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	} else if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	} else {
		return fmt.Sprintf("%ds", seconds)
	}
}
