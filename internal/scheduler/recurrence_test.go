package scheduler

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}

func utc(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
}

func TestWallClockDST(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	london := mustLoc(t, "Europe/London")
	lordHowe := mustLoc(t, "Australia/Lord_Howe") // 30 minute DST shift

	tests := []struct {
		name string
		loc  *time.Location
		y    int
		mo   time.Month
		d    int
		h    int
		mi   int
		want time.Time
	}{
		{"ordinary winter time", ny, 2026, 1, 15, 7, 30, utc(2026, 1, 15, 12, 30)},
		{"ordinary summer time", ny, 2026, 7, 15, 7, 30, utc(2026, 7, 15, 11, 30)},
		// 2026-03-08: clocks jump from 02:00 EST to 03:00 EDT. 02:30 does not exist.
		{"spring forward gap resolves to 03:00", ny, 2026, 3, 8, 2, 30, utc(2026, 3, 8, 7, 0)},
		{"just before the gap", ny, 2026, 3, 8, 1, 59, utc(2026, 3, 8, 6, 59)},
		{"first minute after the gap", ny, 2026, 3, 8, 3, 0, utc(2026, 3, 8, 7, 0)},
		// 2026-11-01: 02:00 EDT falls back to 01:00 EST. 01:30 happens twice.
		{"fall back picks first occurrence", ny, 2026, 11, 1, 1, 30, utc(2026, 11, 1, 5, 30)},
		{"after fall back", ny, 2026, 11, 1, 3, 0, utc(2026, 11, 1, 8, 0)},
		// 2026-03-29: 01:00 GMT jumps to 02:00 BST.
		{"london gap", london, 2026, 3, 29, 1, 30, utc(2026, 3, 29, 1, 0)},
		// 2026-10-25: 02:00 BST falls back to 01:00 GMT.
		{"london fold", london, 2026, 10, 25, 1, 30, utc(2026, 10, 25, 0, 30)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wallClock(tt.y, tt.mo, tt.d, tt.h, tt.mi, tt.loc)
			if !got.Equal(tt.want) {
				t.Errorf("wallClock = %v, want %v", got.UTC(), tt.want)
			}
		})
	}

	t.Run("half hour DST shift gap", func(t *testing.T) {
		// 2026-10-04 in Lord Howe: 02:00 +10:30 jumps to 02:30 +11:00.
		got := wallClock(2026, 10, 4, 2, 15, lordHowe)
		l := got.In(lordHowe)
		if l.Hour() != 2 || l.Minute() != 30 {
			t.Errorf("resolved to %v, want local 02:30", l)
		}
		if before := got.Add(-time.Second).In(lordHowe); before.Hour() != 1 || before.Minute() != 59 {
			t.Errorf("instant before = %v, want local 01:59", before)
		}
	})
}

func TestDayRuleNext(t *testing.T) {
	ny := mustLoc(t, "America/New_York")

	tests := []struct {
		name  string
		rule  string
		after time.Time
		want  time.Time
	}{
		{"later today", "daily@18:00", utc(2026, 10, 11, 12, 0), utc(2026, 10, 11, 22, 0)},
		{"already passed goes tomorrow", "daily@07:00", utc(2026, 10, 11, 12, 0), utc(2026, 10, 12, 11, 0)},
		{"strictly after", "daily@08:00", utc(2026, 10, 11, 12, 0), utc(2026, 10, 12, 12, 0)},
		// 2026-10-11 is a Sunday.
		{"weekdays skip the weekend", "weekdays@09:00", utc(2026, 10, 11, 12, 0), utc(2026, 10, 12, 13, 0)},
		{"weekly picks next listed day", "weekly:wed,fri@18:00", utc(2026, 10, 11, 12, 0), utc(2026, 10, 14, 22, 0)},
		// Daily 02:30 across the spring-forward night: it must not be skipped.
		{"daily across the gap", "daily@02:30", utc(2026, 3, 7, 12, 0), utc(2026, 3, 8, 7, 0)},
		{"day after the gap is normal", "daily@02:30", utc(2026, 3, 8, 7, 0), utc(2026, 3, 9, 6, 30)},
		// Daily 01:30 across the fall-back night runs once, then the next day.
		{"daily across the fold runs once", "daily@01:30", utc(2026, 10, 31, 12, 0), utc(2026, 11, 1, 5, 30)},
		{"after the fold's first run", "daily@01:30", utc(2026, 11, 1, 5, 30), utc(2026, 11, 2, 6, 30)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := ParseRule(tt.rule)
			if err != nil {
				t.Fatal(err)
			}
			got, err := r.Next(tt.after, ny)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("Next = %v, want %v", got.UTC(), tt.want)
			}
		})
	}
}

func TestEveryRule(t *testing.T) {
	r, err := ParseRule("every:90m")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := r.Next(utc(2026, 1, 1, 0, 0), time.UTC)
	if want := utc(2026, 1, 1, 1, 30); !got.Equal(want) {
		t.Errorf("Next = %v, want %v", got, want)
	}
}

func TestCronNext(t *testing.T) {
	ny := mustLoc(t, "America/New_York")

	tests := []struct {
		name  string
		expr  string
		after time.Time
		want  time.Time
	}{
		{"weekday mornings", "cron:30 7 * * 1-5", utc(2026, 10, 11, 12, 0), utc(2026, 10, 12, 11, 30)},
		{"every 15 minutes", "cron:*/15 * * * *", utc(2026, 10, 11, 12, 1), utc(2026, 10, 11, 12, 15)},
		{"step then list", "cron:0 9,17 * * *", utc(2026, 10, 11, 14, 0), utc(2026, 10, 11, 21, 0)},
		{"sunday as 0", "cron:0 10 * * 0", utc(2026, 10, 12, 0, 0), utc(2026, 10, 18, 14, 0)},
		{"sunday as 7", "cron:0 10 * * 7", utc(2026, 10, 12, 0, 0), utc(2026, 10, 18, 14, 0)},
		// Clocks fall back on 2026-11-01, so 08:00 that day is EST (UTC-5).
		{"first of month", "cron:0 8 1 * *", utc(2026, 10, 11, 0, 0), utc(2026, 11, 1, 13, 0)},
		{"dom or dow when both set", "cron:0 8 13 * 1", utc(2026, 10, 11, 12, 0), utc(2026, 10, 12, 12, 0)},
		{"leap day", "cron:0 12 29 2 *", utc(2026, 10, 11, 0, 0), utc(2028, 2, 29, 17, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := ParseRule(tt.expr)
			if err != nil {
				t.Fatal(err)
			}
			got, err := r.Next(tt.after, ny)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("Next = %v, want %v", got.UTC(), tt.want)
			}
		})
	}
}

func TestParseRuleErrors(t *testing.T) {
	for _, s := range []string{
		"", "sometimes", "daily@25:00", "daily@7", "weekly:funday@10:00", "weekly:mon", "weekly:@10:00",
		"every:5s", "every:soon", "cron:* * * *", "cron:61 * * * *", "cron:* 24 * * *", "cron:*/0 * * * *",
		"cron:5-1 * * * *", "cron:a * * * *",
	} {
		t.Run(s, func(t *testing.T) {
			if _, err := ParseRule(s); err == nil {
				t.Errorf("ParseRule(%q) succeeded, want error", s)
			}
		})
	}
}

// Successive runs must always move forward, across every DST transition of the
// year, for each rule kind.
func TestNextIsStrictlyIncreasing(t *testing.T) {
	zones := []string{"America/New_York", "Europe/London", "Australia/Lord_Howe", "Asia/Tokyo", "Pacific/Auckland"}
	rules := []string{"daily@02:30", "daily@01:30", "weekdays@00:30", "every:6h", "cron:*/45 1-3 * * *"}

	for _, z := range zones {
		loc := mustLoc(t, z)
		for _, rs := range rules {
			rule, err := ParseRule(rs)
			if err != nil {
				t.Fatal(err)
			}
			t.Run(z+"/"+rs, func(t *testing.T) {
				cur := utc(2026, 1, 1, 0, 0)
				end := utc(2027, 1, 1, 0, 0)
				for steps := 0; cur.Before(end) && steps < 5000; steps++ {
					next, err := rule.Next(cur, loc)
					if err != nil {
						t.Fatal(err)
					}
					if !next.After(cur) {
						t.Fatalf("Next(%v) = %v, not after input", cur, next)
					}
					cur = next
				}
			})
		}
	}
}

func TestParseMisfire(t *testing.T) {
	good := map[string]string{
		"": "run_once", "run_once": "run_once", "skip": "skip", "expire_after:30m": "expire_after:30m0s",
	}
	for in, want := range good {
		m, err := ParseMisfire(in)
		if err != nil || m.String() != want {
			t.Errorf("ParseMisfire(%q) = %v, %v; want %q", in, m, err, want)
		}
	}
	for _, bad := range []string{"later", "expire_after:", "expire_after:-5m", "expire_after:soon"} {
		if _, err := ParseMisfire(bad); err == nil {
			t.Errorf("ParseMisfire(%q) succeeded", bad)
		}
	}
}
