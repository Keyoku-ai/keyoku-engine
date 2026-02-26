package keyoku

import (
	"testing"
	"time"
)

func TestParseSchedule(t *testing.T) {
	tests := []struct {
		tag      string
		wantType ScheduleType
		wantErr  bool
	}{
		// Interval types (backwards compat)
		{"cron:hourly", ScheduleInterval, false},
		{"cron:daily", ScheduleInterval, false},
		{"cron:weekly", ScheduleInterval, false},
		{"cron:monthly", ScheduleInterval, false},
		{"cron:every:4h", ScheduleInterval, false},
		{"cron:every:30m", ScheduleInterval, false},
		{"cron:every:1h30m", ScheduleInterval, false},

		// Backwards compat: weekly with just day name → interval
		{"cron:weekly:monday", ScheduleInterval, false},

		// Daily with time
		{"cron:daily:08:00", ScheduleDaily, false},
		{"cron:daily:23:59", ScheduleDaily, false},
		{"cron:daily:00:00", ScheduleDaily, false},

		// Daily with timezone
		{"cron:daily:08:00:America/New_York", ScheduleDaily, false},
		{"cron:daily:08:00:UTC", ScheduleDaily, false},

		// Weekly with day and time
		{"cron:weekly:mon:09:00", ScheduleWeekly, false},
		{"cron:weekly:friday:17:00", ScheduleWeekly, false},
		{"cron:weekly:sun:06:30", ScheduleWeekly, false},

		// Weekdays
		{"cron:weekdays:08:00", ScheduleWeekdays, false},
		{"cron:weekdays:09:30", ScheduleWeekdays, false},

		// Monthly
		{"cron:monthly:1:09:00", ScheduleMonthly, false},
		{"cron:monthly:15:12:00", ScheduleMonthly, false},
		{"cron:monthly:31:08:00", ScheduleMonthly, false},

		// Once
		{"cron:once:2026-03-01T08:00:00", ScheduleOnce, false},
		{"cron:once:2026-03-01T08:00", ScheduleOnce, false},
		{"cron:once:2026-03-01", ScheduleOnce, false},

		// Errors
		{"not-a-cron-tag", ScheduleInterval, true},
		{"cron:", ScheduleInterval, true},
		{"cron:unknown", ScheduleInterval, true},
		{"cron:daily:25:00", ScheduleDaily, true},   // invalid hour
		{"cron:daily:08:60", ScheduleDaily, true},   // invalid minute
		{"cron:weekly:xyz:09:00", ScheduleWeekly, true}, // invalid day
		{"cron:monthly:0:09:00", ScheduleMonthly, true}, // day 0
		{"cron:monthly:32:09:00", ScheduleMonthly, true}, // day 32
		{"cron:every:", ScheduleInterval, true},
		{"cron:every:notaduration", ScheduleInterval, true},
		{"cron:once:", ScheduleOnce, true},
		{"cron:weekdays:08", ScheduleWeekdays, true}, // missing minute
		{"cron:daily:08:00:Invalid/Timezone", ScheduleDaily, true},
	}

	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			sched, err := ParseSchedule(tt.tag)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseSchedule(%q) expected error, got %+v", tt.tag, sched)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSchedule(%q) unexpected error: %v", tt.tag, err)
			}
			if sched.Type != tt.wantType {
				t.Errorf("ParseSchedule(%q).Type = %q, want %q", tt.tag, sched.Type, tt.wantType)
			}
			if sched.Raw != tt.tag {
				t.Errorf("ParseSchedule(%q).Raw = %q, want %q", tt.tag, sched.Raw, tt.tag)
			}
		})
	}
}

func TestParseSchedule_IntervalValues(t *testing.T) {
	tests := []struct {
		tag      string
		wantDur  time.Duration
	}{
		{"cron:hourly", 1 * time.Hour},
		{"cron:daily", 24 * time.Hour},
		{"cron:weekly", 7 * 24 * time.Hour},
		{"cron:monthly", 30 * 24 * time.Hour},
		{"cron:every:4h", 4 * time.Hour},
		{"cron:every:30m", 30 * time.Minute},
		{"cron:every:1h30m", 90 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			sched, err := ParseSchedule(tt.tag)
			if err != nil {
				t.Fatalf("ParseSchedule(%q) error: %v", tt.tag, err)
			}
			if sched.Interval != tt.wantDur {
				t.Errorf("Interval = %v, want %v", sched.Interval, tt.wantDur)
			}
		})
	}
}

func TestParseSchedule_DailyValues(t *testing.T) {
	sched, err := ParseSchedule("cron:daily:08:30")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if sched.Hour != 8 || sched.Minute != 30 {
		t.Errorf("got %d:%d, want 8:30", sched.Hour, sched.Minute)
	}
	if sched.Location != nil {
		t.Error("expected nil location for local time")
	}

	sched, err = ParseSchedule("cron:daily:08:30:America/New_York")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if sched.Location == nil {
		t.Fatal("expected non-nil location")
	}
	if sched.Location.String() != "America/New_York" {
		t.Errorf("location = %q, want America/New_York", sched.Location.String())
	}
}

func TestParseSchedule_WeeklyValues(t *testing.T) {
	sched, err := ParseSchedule("cron:weekly:mon:09:00")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if sched.Weekday != time.Monday {
		t.Errorf("Weekday = %v, want Monday", sched.Weekday)
	}
	if sched.Hour != 9 || sched.Minute != 0 {
		t.Errorf("got %d:%d, want 9:00", sched.Hour, sched.Minute)
	}
}

func TestParseSchedule_MonthlyValues(t *testing.T) {
	sched, err := ParseSchedule("cron:monthly:15:12:00")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if sched.MonthDay != 15 {
		t.Errorf("MonthDay = %d, want 15", sched.MonthDay)
	}
	if sched.Hour != 12 || sched.Minute != 0 {
		t.Errorf("got %d:%d, want 12:00", sched.Hour, sched.Minute)
	}
}

func TestParseScheduleFromTags(t *testing.T) {
	// Found
	sched, err := ParseScheduleFromTags([]string{"important", "cron:daily:08:00", "work"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if sched.Type != ScheduleDaily {
		t.Errorf("Type = %q, want daily", sched.Type)
	}

	// Not found
	_, err = ParseScheduleFromTags([]string{"important", "work"})
	if err == nil {
		t.Error("expected error for no cron tag")
	}

	// Empty
	_, err = ParseScheduleFromTags(nil)
	if err == nil {
		t.Error("expected error for nil tags")
	}
}

func TestSchedule_NextRun_Interval(t *testing.T) {
	sched := &Schedule{Type: ScheduleInterval, Interval: 4 * time.Hour}
	after := time.Date(2026, 2, 26, 10, 0, 0, 0, time.UTC)
	next := sched.NextRun(after)
	want := time.Date(2026, 2, 26, 14, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextRun = %v, want %v", next, want)
	}
}

func TestSchedule_NextRun_Daily(t *testing.T) {
	loc := time.UTC
	sched := &Schedule{Type: ScheduleDaily, Hour: 8, Minute: 0, Location: loc}

	// Before 8am → should fire today at 8am
	after := time.Date(2026, 2, 26, 6, 0, 0, 0, loc)
	next := sched.NextRun(after)
	want := time.Date(2026, 2, 26, 8, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Errorf("before 8am: NextRun = %v, want %v", next, want)
	}

	// After 8am → should fire tomorrow at 8am
	after = time.Date(2026, 2, 26, 10, 0, 0, 0, loc)
	next = sched.NextRun(after)
	want = time.Date(2026, 2, 27, 8, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Errorf("after 8am: NextRun = %v, want %v", next, want)
	}

	// Exactly at 8am → should fire tomorrow
	after = time.Date(2026, 2, 26, 8, 0, 0, 0, loc)
	next = sched.NextRun(after)
	want = time.Date(2026, 2, 27, 8, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Errorf("at 8am: NextRun = %v, want %v", next, want)
	}
}

func TestSchedule_NextRun_Weekly(t *testing.T) {
	loc := time.UTC
	sched := &Schedule{Type: ScheduleWeekly, Weekday: time.Monday, Hour: 9, Minute: 0, Location: loc}

	// Thursday → next Monday
	after := time.Date(2026, 2, 26, 10, 0, 0, 0, loc) // Thursday
	next := sched.NextRun(after)
	want := time.Date(2026, 3, 2, 9, 0, 0, 0, loc) // Monday
	if !next.Equal(want) {
		t.Errorf("Thursday: NextRun = %v (weekday=%v), want %v (Monday)", next, next.Weekday(), want)
	}

	// Monday before 9am → today at 9am
	after = time.Date(2026, 3, 2, 7, 0, 0, 0, loc) // Monday 7am
	next = sched.NextRun(after)
	want = time.Date(2026, 3, 2, 9, 0, 0, 0, loc) // Monday 9am
	if !next.Equal(want) {
		t.Errorf("Monday 7am: NextRun = %v, want %v", next, want)
	}

	// Monday after 9am → next Monday
	after = time.Date(2026, 3, 2, 12, 0, 0, 0, loc) // Monday noon
	next = sched.NextRun(after)
	want = time.Date(2026, 3, 9, 9, 0, 0, 0, loc) // next Monday
	if !next.Equal(want) {
		t.Errorf("Monday noon: NextRun = %v, want %v", next, want)
	}
}

func TestSchedule_NextRun_Weekdays(t *testing.T) {
	loc := time.UTC
	sched := &Schedule{Type: ScheduleWeekdays, Hour: 8, Minute: 0, Location: loc}

	// Friday after 8am → next Monday
	after := time.Date(2026, 2, 27, 10, 0, 0, 0, loc) // Friday
	next := sched.NextRun(after)
	want := time.Date(2026, 3, 2, 8, 0, 0, 0, loc) // Monday
	if !next.Equal(want) {
		t.Errorf("Friday 10am: NextRun = %v (weekday=%v), want %v (Monday)", next, next.Weekday(), want)
	}

	// Saturday → next Monday
	after = time.Date(2026, 2, 28, 10, 0, 0, 0, loc) // Saturday
	next = sched.NextRun(after)
	want = time.Date(2026, 3, 2, 8, 0, 0, 0, loc) // Monday
	if !next.Equal(want) {
		t.Errorf("Saturday: NextRun = %v, want %v", next, want)
	}

	// Wednesday before 8am → today
	after = time.Date(2026, 2, 25, 6, 0, 0, 0, loc) // Wednesday 6am
	next = sched.NextRun(after)
	want = time.Date(2026, 2, 25, 8, 0, 0, 0, loc) // Wednesday 8am
	if !next.Equal(want) {
		t.Errorf("Wed 6am: NextRun = %v, want %v", next, want)
	}
}

func TestSchedule_NextRun_Monthly(t *testing.T) {
	loc := time.UTC
	sched := &Schedule{Type: ScheduleMonthly, MonthDay: 15, Hour: 9, Minute: 0, Location: loc}

	// Before the 15th → this month
	after := time.Date(2026, 2, 10, 0, 0, 0, 0, loc)
	next := sched.NextRun(after)
	want := time.Date(2026, 2, 15, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Errorf("Feb 10: NextRun = %v, want %v", next, want)
	}

	// After the 15th → next month
	after = time.Date(2026, 2, 20, 0, 0, 0, 0, loc)
	next = sched.NextRun(after)
	want = time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Errorf("Feb 20: NextRun = %v, want %v", next, want)
	}
}

func TestSchedule_NextRun_Once(t *testing.T) {
	target := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	sched := &Schedule{Type: ScheduleOnce, OneShot: target}

	// Before target → returns target
	after := time.Date(2026, 2, 26, 0, 0, 0, 0, time.UTC)
	next := sched.NextRun(after)
	if !next.Equal(target) {
		t.Errorf("before: NextRun = %v, want %v", next, target)
	}

	// After target → returns zero (expired)
	after = time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	next = sched.NextRun(after)
	if !next.IsZero() {
		t.Errorf("after: NextRun = %v, want zero", next)
	}
}

func TestSchedule_IsDue(t *testing.T) {
	loc := time.UTC
	sched := &Schedule{Type: ScheduleDaily, Hour: 8, Minute: 0, Location: loc}

	// Last run: yesterday 8am. Now: today 7:59am → not due
	lastRun := time.Date(2026, 2, 25, 8, 0, 0, 0, loc)
	now := time.Date(2026, 2, 26, 7, 59, 0, 0, loc)
	if sched.IsDue(lastRun, now) {
		t.Error("should NOT be due at 7:59am")
	}

	// Last run: yesterday 8am. Now: today 8:00am → due
	now = time.Date(2026, 2, 26, 8, 0, 0, 0, loc)
	if !sched.IsDue(lastRun, now) {
		t.Error("SHOULD be due at 8:00am")
	}

	// Last run: yesterday 8am. Now: today 8:01am → due
	now = time.Date(2026, 2, 26, 8, 1, 0, 0, loc)
	if !sched.IsDue(lastRun, now) {
		t.Error("SHOULD be due at 8:01am")
	}

	// Last run: today 8am. Now: today 10am → not due (already ran today)
	lastRun = time.Date(2026, 2, 26, 8, 0, 0, 0, loc)
	now = time.Date(2026, 2, 26, 10, 0, 0, 0, loc)
	if sched.IsDue(lastRun, now) {
		t.Error("should NOT be due — already ran today")
	}
}

func TestSchedule_IsDue_Interval(t *testing.T) {
	sched := &Schedule{Type: ScheduleInterval, Interval: 4 * time.Hour}

	lastRun := time.Date(2026, 2, 26, 10, 0, 0, 0, time.UTC)

	// 3h59m later → not due
	now := time.Date(2026, 2, 26, 13, 59, 0, 0, time.UTC)
	if sched.IsDue(lastRun, now) {
		t.Error("should NOT be due at 3h59m")
	}

	// 4h later → due
	now = time.Date(2026, 2, 26, 14, 0, 0, 0, time.UTC)
	if !sched.IsDue(lastRun, now) {
		t.Error("SHOULD be due at 4h")
	}
}

func TestSchedule_IsDue_Once(t *testing.T) {
	target := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	sched := &Schedule{Type: ScheduleOnce, OneShot: target}

	// Before target, last run before target → due when now >= target
	lastRun := time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	if !sched.IsDue(lastRun, now) {
		t.Error("SHOULD be due at target time")
	}

	// After acknowledgment (last run after target) → not due
	lastRun = time.Date(2026, 3, 1, 8, 1, 0, 0, time.UTC)
	now = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	if sched.IsDue(lastRun, now) {
		t.Error("should NOT be due — already acknowledged")
	}
}

// TestBackwardsCompatibility verifies that the old formats still work
// identically to the original parseCronTag behavior.
func TestBackwardsCompatibility(t *testing.T) {
	tests := []struct {
		tag      string
		wantDur  time.Duration
	}{
		{"cron:hourly", 1 * time.Hour},
		{"cron:daily", 24 * time.Hour},
		{"cron:weekly", 7 * 24 * time.Hour},
		{"cron:weekly:monday", 7 * 24 * time.Hour},
		{"cron:monthly", 30 * 24 * time.Hour},
		{"cron:every:4h", 4 * time.Hour},
		{"cron:every:30m", 30 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			sched, err := ParseSchedule(tt.tag)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if sched.Type != ScheduleInterval {
				t.Errorf("Type = %q, want interval", sched.Type)
			}
			if sched.Interval != tt.wantDur {
				t.Errorf("Interval = %v, want %v", sched.Interval, tt.wantDur)
			}

			// Verify IsDue matches old behavior: due when elapsed >= interval
			lastRun := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			notDue := lastRun.Add(tt.wantDur - 1*time.Second)
			isDue := lastRun.Add(tt.wantDur)

			if sched.IsDue(lastRun, notDue) {
				t.Error("should NOT be due before interval elapsed")
			}
			if !sched.IsDue(lastRun, isDue) {
				t.Error("SHOULD be due when interval elapsed")
			}
		})
	}
}
