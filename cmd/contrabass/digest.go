package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/junhoyeo/contrabass/internal/logging"
)

var digestCmd = &cobra.Command{
	Use:   "digest",
	Short: "Generate a session digest from JSONL run logs",
	Long: `Reads contrabass-runs.jsonl and produces a human-readable
markdown summary of agent runs for a given period (default: today).`,
	RunE: runDigest,
}

func init() {
	digestCmd.Flags().String("file", "contrabass-runs.jsonl", "path to JSONL run log")
	digestCmd.Flags().String("since", "today", "period filter: today, yesterday, 7d, all, or YYYY-MM-DD")
	digestCmd.Flags().String("project", "", "filter by project name (empty = all)")
}

func runDigest(cmd *cobra.Command, args []string) error {
	filePath, _ := cmd.Flags().GetString("file")
	sincePeriod, _ := cmd.Flags().GetString("since")
	projectFilter, _ := cmd.Flags().GetString("project")

	sinceTime, err := parseSince(sincePeriod)
	if err != nil {
		return err
	}

	records, err := readJSONL(filePath, sinceTime, projectFilter)
	if err != nil {
		return err
	}

	if len(records) == 0 {
		fmt.Println("No runs found for the given period.")
		return nil
	}

	printDigest(records, sinceTime)
	return nil
}

func parseSince(s string) (time.Time, error) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	switch strings.ToLower(s) {
	case "today":
		return today, nil
	case "yesterday":
		return today.AddDate(0, 0, -1), nil
	case "7d", "week":
		return today.AddDate(0, 0, -7), nil
	case "30d", "month":
		return today.AddDate(0, 0, -30), nil
	case "all", "":
		return time.Time{}, nil
	default:
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid --since value %q (use: today, yesterday, 7d, all, or YYYY-MM-DD)", s)
		}
		return t, nil
	}
}

func readJSONL(path string, since time.Time, projectFilter string) ([]logging.RunRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	var records []logging.RunRecord
	scanner := bufio.NewScanner(f)
	// Allow large lines (some run records may be verbose).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var rec logging.RunRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // skip malformed lines
		}

		if !since.IsZero() && rec.Timestamp.Before(since) {
			continue
		}
		if projectFilter != "" && rec.Project != projectFilter {
			continue
		}

		records = append(records, rec)
	}

	return records, scanner.Err()
}

func printDigest(records []logging.RunRecord, since time.Time) {
	// Sort by timestamp.
	sort.Slice(records, func(i, j int) bool {
		return records[i].Timestamp.Before(records[j].Timestamp)
	})

	// Aggregate stats.
	type projectStats struct {
		Succeeded int
		Failed    int
		Retried   int
		PRsMerged int
		TotalMs   int64
		TokensIn  int64
		TokensOut int64
	}

	byProject := make(map[string]*projectStats)
	var totalSucceeded, totalFailed, totalRetried, totalMerged int

	for _, r := range records {
		proj := r.Project
		if proj == "" {
			proj = "(default)"
		}
		stats, ok := byProject[proj]
		if !ok {
			stats = &projectStats{}
			byProject[proj] = stats
		}

		switch {
		case strings.Contains(strings.ToLower(r.Phase), "succeeded"):
			stats.Succeeded++
			totalSucceeded++
		default:
			stats.Failed++
			totalFailed++
		}
		if r.Attempt > 1 {
			stats.Retried++
			totalRetried++
		}
		if r.PRMerged {
			stats.PRsMerged++
			totalMerged++
		}
		stats.TotalMs += r.DurationMs
		stats.TokensIn += r.TokensIn
		stats.TokensOut += r.TokensOut
	}

	// Print markdown.
	periodLabel := since.Format("2006-01-02")
	if since.IsZero() {
		periodLabel = "all time"
	}

	fmt.Printf("# Contrabass Digest — %s\n\n", periodLabel)
	fmt.Printf("**Runs:** %d total | ✅ %d succeeded | ❌ %d failed | 🔄 %d retried | 🔀 %d merged\n\n",
		len(records), totalSucceeded, totalFailed, totalRetried, totalMerged)

	// Per-project breakdown.
	if len(byProject) > 1 {
		fmt.Print("## By Project\n\n")
		fmt.Println("| Project | ✅ | ❌ | 🔄 | 🔀 | Avg Duration | Tokens |")
		fmt.Println("|---------|----|----|----|----|-------------|--------|")
		for proj, stats := range byProject {
			total := stats.Succeeded + stats.Failed
			avgMs := int64(0)
			if total > 0 {
				avgMs = stats.TotalMs / int64(total)
			}
			fmt.Printf("| %s | %d | %d | %d | %d | %s | %dK in / %dK out |\n",
				proj, stats.Succeeded, stats.Failed, stats.Retried, stats.PRsMerged,
				formatDuration(avgMs), stats.TokensIn/1000, stats.TokensOut/1000)
		}
		fmt.Println()
	}

	// Run timeline.
	fmt.Print("## Run Timeline\n\n")
	for _, r := range records {
		status := "❌"
		if strings.Contains(strings.ToLower(r.Phase), "succeeded") {
			status = "✅"
		}
		merged := ""
		if r.PRMerged {
			merged = " → merged"
		}
		proj := ""
		if r.Project != "" {
			proj = fmt.Sprintf("[%s] ", r.Project)
		}
		errMsg := ""
		if r.Error != "" {
			errMsg = fmt.Sprintf(" — `%s`", r.Error)
		}
		fmt.Printf("- `%s` %s%s **#%s** %s (attempt %d, %s)%s%s\n",
			r.Timestamp.Format("15:04:05"),
			proj, status, r.IssueID, r.IssueTitle,
			r.Attempt, formatDuration(r.DurationMs),
			merged, errMsg)
	}

	// Anomalies.
	anomalies := detectAnomalies(records)
	if len(anomalies) > 0 {
		fmt.Print("\n## Anomalies\n\n")
		for _, a := range anomalies {
			fmt.Printf("- ⚠️ %s\n", a)
		}
	}
}

func detectAnomalies(records []logging.RunRecord) []string {
	var anomalies []string

	// Detect issues with >2 attempts.
	issueCounts := make(map[string]int)
	for _, r := range records {
		key := r.Project + "#" + r.IssueID
		issueCounts[key]++
	}
	for key, count := range issueCounts {
		if count > 2 {
			anomalies = append(anomalies, fmt.Sprintf("Issue %s had %d attempts (possible flaky or stuck issue)", key, count))
		}
	}

	// Detect runs with 0 tokens that succeeded (suspicious).
	for _, r := range records {
		if strings.Contains(strings.ToLower(r.Phase), "succeeded") && r.TokensIn == 0 && r.TokensOut == 0 {
			anomalies = append(anomalies, fmt.Sprintf("Issue #%s succeeded with 0 tokens (agent may not have done work)", r.IssueID))
		}
	}

	// Detect very short successful runs (<30s).
	for _, r := range records {
		if strings.Contains(strings.ToLower(r.Phase), "succeeded") && r.DurationMs > 0 && r.DurationMs < 30000 {
			anomalies = append(anomalies, fmt.Sprintf("Issue #%s succeeded in only %s (suspiciously fast)", r.IssueID, formatDuration(r.DurationMs)))
		}
	}

	return anomalies
}

func formatDuration(ms int64) string {
	if ms <= 0 {
		return "n/a"
	}
	d := time.Duration(ms) * time.Millisecond
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
}
