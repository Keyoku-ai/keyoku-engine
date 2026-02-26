package keyoku

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ScheduleType represents the kind of schedule.
type ScheduleType string

const (
	ScheduleInterval ScheduleType = "interval"
	ScheduleDaily    ScheduleType = "daily"
	ScheduleWeekly   ScheduleType = "weekly"
	ScheduleWeekdays ScheduleType = "weekdays"
	ScheduleMonthly  ScheduleType = "monthly"
	ScheduleOnce     ScheduleType = "once"
)

// Schedule represents a parsed cron/schedule specification.
type Schedule struct {
	Type     ScheduleType
	Raw      string         // the original tag string
	Interval time.Duration  // for interval type
	Hour     int            // 0-23, for daily/weekly/weekdays/monthly
	Minute   int            // 0-59
	Weekday  time.Weekday   // for weekly type
	MonthDay int            // 1-31, for monthly type
	OneShot  time.Time      // for once type
	Location *time.Location // timezone (nil means local)
}

var weekdayMap = map[string]time.Weekday{
	"sun":       time.Sunday,
	"sunday":    time.Sunday,
	"mon":       time.Monday,
	"monday":    time.Monday,
	"tue":       time.Tuesday,
	"tuesday":   time.Tuesday,
	"wed":       time.Wednesday,
	"wednesday": time.Wednesday,
	"thu":       time.Thursday,
	"thursday":  time.Thursday,
	"fri":       time.Friday,
	"friday":    time.Friday,
	"sat":       time.Saturday,
	"saturday":  time.Saturday,
}

// ParseSchedule parses a cron tag string into a Schedule.
//
// Supported formats:
//
//	cron:hourly                          → every hour (interval)
//	cron:daily                           → every 24h (interval, backwards compat)
//	cron:daily:HH:MM                    → every day at HH:MM local time
//	cron:daily:HH:MM:TZ                 → every day at HH:MM in timezone TZ
//	cron:weekly                          → every 7 days (interval, backwards compat)
//	cron:weekly:DAY:HH:MM               → every DAY at HH:MM
//	cron:weekdays:HH:MM                 → Mon-Fri at HH:MM
//	cron:monthly                         → every 30 days (interval, backwards compat)
//	cron:monthly:D:HH:MM                → day D of month at HH:MM
//	cron:every:DURATION                  → every Go duration (e.g., 4h, 30m)
//	cron:once:ISO8601                    → one-shot at specific time
func ParseSchedule(tag string) (*Schedule, error) {
	if !strings.HasPrefix(tag, "cron:") {
		return nil, fmt.Errorf("schedule tag must start with 'cron:': %q", tag)
	}

	rest := strings.TrimPrefix(tag, "cron:")
	parts := strings.SplitN(rest, ":", -1)
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("empty schedule tag: %q", tag)
	}

	keyword := strings.ToLower(parts[0])

	switch keyword {
	case "hourly":
		return &Schedule{Type: ScheduleInterval, Raw: tag, Interval: 1 * time.Hour}, nil

	case "daily":
		return parseDaily(tag, parts[1:])

	case "weekly":
		return parseWeekly(tag, parts[1:])

	case "weekdays":
		return parseWeekdays(tag, parts[1:])

	case "monthly":
		return parseMonthly(tag, parts[1:])

	case "every":
		return parseEvery(tag, parts[1:])

	case "once":
		return parseOnce(tag, parts[1:])

	default:
		return nil, fmt.Errorf("unknown schedule keyword %q in tag %q", keyword, tag)
	}
}

// ParseScheduleFromTags scans a tag slice for the first cron:* tag and parses it.
func ParseScheduleFromTags(tags []string) (*Schedule, error) {
	for _, tag := range tags {
		if strings.HasPrefix(tag, "cron:") {
			return ParseSchedule(tag)
		}
	}
	return nil, fmt.Errorf("no cron tag found")
}

// NextRun calculates the next run time after the given time.
// Returns zero time if the schedule has no next run (e.g., expired one-shot).
func (s *Schedule) NextRun(after time.Time) time.Time {
	loc := s.Location
	if loc == nil {
		loc = time.Local
	}

	switch s.Type {
	case ScheduleInterval:
		return after.Add(s.Interval)

	case ScheduleDaily:
		return nextDailyRun(after, s.Hour, s.Minute, loc)

	case ScheduleWeekly:
		return nextWeeklyRun(after, s.Weekday, s.Hour, s.Minute, loc)

	case ScheduleWeekdays:
		return nextWeekdayRun(after, s.Hour, s.Minute, loc)

	case ScheduleMonthly:
		return nextMonthlyRun(after, s.MonthDay, s.Hour, s.Minute, loc)

	case ScheduleOnce:
		if s.OneShot.After(after) {
			return s.OneShot
		}
		return time.Time{} // expired

	default:
		return time.Time{}
	}
}

// IsDue returns true if the schedule is due for execution given the last run time.
func (s *Schedule) IsDue(lastRun time.Time, now time.Time) bool {
	next := s.NextRun(lastRun)
	if next.IsZero() {
		return false
	}
	return !now.Before(next) // now >= next
}

// --- internal parsers ---

func parseDaily(raw string, parts []string) (*Schedule, error) {
	// No time specified → backwards compat interval mode
	if len(parts) == 0 {
		return &Schedule{Type: ScheduleInterval, Raw: raw, Interval: 24 * time.Hour}, nil
	}

	// Need at least HH and MM: parts = ["HH", "MM"] or ["HH", "MM", "TZ"]
	if len(parts) < 2 {
		return nil, fmt.Errorf("daily schedule requires HH:MM, got %q", raw)
	}

	hour, minute, err := parseHHMM(parts[0], parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid time in %q: %w", raw, err)
	}

	var loc *time.Location
	if len(parts) >= 3 {
		// Remaining parts are the timezone (may contain colons, e.g., shouldn't but join them)
		tzName := strings.Join(parts[2:], "/")
		loc, err = time.LoadLocation(tzName)
		if err != nil {
			return nil, fmt.Errorf("invalid timezone %q in %q: %w", tzName, raw, err)
		}
	}

	return &Schedule{
		Type:     ScheduleDaily,
		Raw:      raw,
		Hour:     hour,
		Minute:   minute,
		Location: loc,
	}, nil
}

func parseWeekly(raw string, parts []string) (*Schedule, error) {
	// No day specified → backwards compat interval mode
	if len(parts) == 0 {
		return &Schedule{Type: ScheduleInterval, Raw: raw, Interval: 7 * 24 * time.Hour}, nil
	}

	// Need DAY, HH, MM: parts = ["mon", "09", "00"]
	if len(parts) < 3 {
		// Might be just "weekly:monday" (old format, backwards compat as interval)
		if len(parts) == 1 {
			return &Schedule{Type: ScheduleInterval, Raw: raw, Interval: 7 * 24 * time.Hour}, nil
		}
		return nil, fmt.Errorf("weekly schedule requires DAY:HH:MM, got %q", raw)
	}

	weekday, ok := weekdayMap[strings.ToLower(parts[0])]
	if !ok {
		return nil, fmt.Errorf("invalid weekday %q in %q", parts[0], raw)
	}

	hour, minute, err := parseHHMM(parts[1], parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid time in %q: %w", raw, err)
	}

	return &Schedule{
		Type:    ScheduleWeekly,
		Raw:     raw,
		Weekday: weekday,
		Hour:    hour,
		Minute:  minute,
	}, nil
}

func parseWeekdays(raw string, parts []string) (*Schedule, error) {
	if len(parts) < 2 {
		return nil, fmt.Errorf("weekdays schedule requires HH:MM, got %q", raw)
	}

	hour, minute, err := parseHHMM(parts[0], parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid time in %q: %w", raw, err)
	}

	return &Schedule{
		Type:   ScheduleWeekdays,
		Raw:    raw,
		Hour:   hour,
		Minute: minute,
	}, nil
}

func parseMonthly(raw string, parts []string) (*Schedule, error) {
	// No day specified → backwards compat interval mode
	if len(parts) == 0 {
		return &Schedule{Type: ScheduleInterval, Raw: raw, Interval: 30 * 24 * time.Hour}, nil
	}

	// Need D, HH, MM: parts = ["1", "09", "00"]
	if len(parts) < 3 {
		return nil, fmt.Errorf("monthly schedule requires D:HH:MM, got %q", raw)
	}

	day, err := strconv.Atoi(parts[0])
	if err != nil || day < 1 || day > 31 {
		return nil, fmt.Errorf("invalid month day %q in %q", parts[0], raw)
	}

	hour, minute, err := parseHHMM(parts[1], parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid time in %q: %w", raw, err)
	}

	return &Schedule{
		Type:     ScheduleMonthly,
		Raw:      raw,
		MonthDay: day,
		Hour:     hour,
		Minute:   minute,
	}, nil
}

func parseEvery(raw string, parts []string) (*Schedule, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("every schedule requires a duration, got %q", raw)
	}

	durStr := strings.Join(parts, ":")
	d, err := time.ParseDuration(durStr)
	if err != nil {
		return nil, fmt.Errorf("invalid duration %q in %q: %w", durStr, raw, err)
	}

	return &Schedule{Type: ScheduleInterval, Raw: raw, Interval: d}, nil
}

func parseOnce(raw string, parts []string) (*Schedule, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("once schedule requires a datetime, got %q", raw)
	}

	// Rejoin parts since ISO8601 contains colons (e.g., 2026-03-01T08:00:00)
	dtStr := strings.Join(parts, ":")

	// Try multiple formats
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02",
	}

	for _, fmt := range formats {
		if t, err := time.Parse(fmt, dtStr); err == nil {
			return &Schedule{Type: ScheduleOnce, Raw: raw, OneShot: t}, nil
		}
	}

	return nil, fmt.Errorf("could not parse datetime %q in %q", dtStr, raw)
}

// --- time helpers ---

func parseHHMM(hourStr, minuteStr string) (int, int, error) {
	hour, err := strconv.Atoi(hourStr)
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf("invalid hour %q", hourStr)
	}
	minute, err := strconv.Atoi(minuteStr)
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("invalid minute %q", minuteStr)
	}
	return hour, minute, nil
}

func nextDailyRun(after time.Time, hour, minute int, loc *time.Location) time.Time {
	y, m, d := after.In(loc).Date()
	candidate := time.Date(y, m, d, hour, minute, 0, 0, loc)
	if !candidate.After(after) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

func nextWeeklyRun(after time.Time, weekday time.Weekday, hour, minute int, loc *time.Location) time.Time {
	y, m, d := after.In(loc).Date()
	candidate := time.Date(y, m, d, hour, minute, 0, 0, loc)

	// Walk forward to find the next matching weekday
	for i := 0; i < 7; i++ {
		if candidate.Weekday() == weekday && candidate.After(after) {
			return candidate
		}
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

func nextWeekdayRun(after time.Time, hour, minute int, loc *time.Location) time.Time {
	y, m, d := after.In(loc).Date()
	candidate := time.Date(y, m, d, hour, minute, 0, 0, loc)

	// Walk forward to find the next weekday (Mon-Fri)
	for i := 0; i < 7; i++ {
		wd := candidate.Weekday()
		if wd >= time.Monday && wd <= time.Friday && candidate.After(after) {
			return candidate
		}
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

func nextMonthlyRun(after time.Time, monthDay, hour, minute int, loc *time.Location) time.Time {
	y, m, _ := after.In(loc).Date()
	candidate := time.Date(y, m, monthDay, hour, minute, 0, 0, loc)

	// If the day doesn't exist in this month (e.g., Feb 31), time.Date normalizes
	// it forward. Check if it rolled to the next month unexpectedly.
	if candidate.Day() != monthDay {
		// Day overflowed (e.g., requested day 31 in a 30-day month).
		// Skip to next month and try again.
		candidate = time.Date(y, m+1, monthDay, hour, minute, 0, 0, loc)
	}

	if !candidate.After(after) {
		// Try next month
		candidate = time.Date(y, m+1, monthDay, hour, minute, 0, 0, loc)
		if candidate.Day() != monthDay {
			candidate = time.Date(y, m+2, monthDay, hour, minute, 0, 0, loc)
		}
	}

	return candidate
}
