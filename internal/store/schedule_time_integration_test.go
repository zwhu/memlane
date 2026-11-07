package store

// Clock fixtures verify calendar/DST policy only, not model or App execution.
import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestScheduleCalendarAndDSTPolicy(t *testing.T) {
	parse := func(v string) time.Time {
		t.Helper()
		at, e := time.Parse(time.RFC3339, v)
		if e != nil {
			t.Fatal(e)
		}
		return at
	}
	base := records.ScheduleSpec{Project: uuid.NewString(), Executor: "traex", Timezone: "Asia/Shanghai", Frequency: "daily", LocalTime: "10:00", MaterialLimit: 8, WindowMinutes: 60}
	cases := []struct{ name, zone, clock, after, want string }{
		{"exact_next_day", "Asia/Shanghai", "10:00", "2026-01-02T02:00:00Z", "2026-01-03T02:00:00Z"},
		{"spring_gap", "America/New_York", "02:30", "2026-03-08T05:00:00Z", "2026-03-08T07:00:00Z"},
		{"gap_cross_midnight", "America/Nuuk", "23:30", "2026-03-28T03:00:00Z", "2026-03-29T01:00:00Z"},
		{"fold_first", "America/New_York", "01:30", "2026-11-01T04:00:00Z", "2026-11-01T05:30:00Z"},
		{"fold_no_duplicate", "America/New_York", "01:30", "2026-11-01T06:00:00Z", "2026-11-02T06:30:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := base
			spec.Timezone = c.zone
			spec.LocalTime = c.clock
			ms, e := NextScheduleRun(spec, 0, parse(c.after))
			mustMerge(t, e)
			if ms != parse(c.want).UnixMilli() {
				t.Fatal(time.UnixMilli(ms), c.want)
			}
		})
	}
	weekly := base
	weekly.Frequency = "weekly"
	weekly.Weekday = int(time.Monday)
	ms, e := NextScheduleRun(weekly, 0, parse("2026-01-02T00:00:00Z"))
	mustMerge(t, e)
	if ms != parse("2026-01-05T02:00:00Z").UnixMilli() {
		t.Fatal("wrong weekly slot", time.UnixMilli(ms))
	}
	interval := base
	interval.Frequency = "interval"
	interval.LocalTime = ""
	interval.IntervalHours = 2
	ms, e = NextScheduleRun(interval, parse("2026-01-01T00:00:00Z").UnixMilli(), parse("2026-01-01T03:37:00Z"))
	mustMerge(t, e)
	if ms != parse("2026-01-01T04:00:00Z").UnixMilli() {
		t.Fatal("wrong anchored interval", time.UnixMilli(ms))
	}
}

func TestSchedulePolicyNeedsExplicitFrequencyAndLimits(t *testing.T) {
	spec := records.ScheduleSpec{Project: uuid.NewString(), Executor: "traex", Timezone: "UTC"}
	if validateScheduleSpec(spec) == nil {
		t.Fatal("empty policy authorized background execution")
	}
	spec.Frequency = "daily"
	spec.LocalTime = "24:00"
	spec.MaterialLimit = 16
	spec.WindowMinutes = 60
	if validateScheduleSpec(spec) == nil {
		t.Fatal("invalid local time accepted")
	}
	spec.LocalTime = "10:00"
	spec.Global = true
	if validateScheduleSpec(spec) == nil {
		t.Fatal("global candidate enabled without review")
	}
	spec.Global = false
	spec.MaterialLimit = 17
	if validateScheduleSpec(spec) == nil {
		t.Fatal("formal input limit bypassed")
	}
}
