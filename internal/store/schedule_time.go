package store

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func validateScheduleSpec(spec records.ScheduleSpec) error {
	invalid := records.MergeError("invalid_request")
	if spec.RetryLimit < 0 || spec.RetryLimit > 2 || spec.RetryLimit == 0 && spec.RecoveryWindowMinutes != 0 || spec.RetryLimit > 0 && (spec.RecoveryWindowMinutes < 1 || spec.RecoveryWindowMinutes > 1440) {
		return invalid
	}
	if _, err := uuid.Parse(spec.Project); err != nil {
		return invalid
	}
	if (spec.Executor != "traex" && spec.Executor != "processing") || len(spec.Model) > 128 || spec.Model != strings.TrimSpace(spec.Model) || strings.ContainsAny(spec.Model, "\n\r\t") || spec.MaterialLimit < 1 || spec.MaterialLimit > 16 || spec.WindowMinutes < 1 || spec.WindowMinutes > 1440 || len(spec.Destinations) > 8 || !spec.Review && (spec.Global || len(spec.Destinations) > 0) {
		return invalid
	}
	if spec.Executor == "traex" && spec.Processing != nil {
		return invalid
	}
	if spec.Timezone == "" || spec.Timezone == "Local" {
		return invalid
	}
	if _, err := time.LoadLocation(spec.Timezone); err != nil {
		return invalid
	}
	switch spec.Frequency {
	case "daily", "weekly":
		if len(spec.LocalTime) != 5 || spec.IntervalHours != 0 {
			return invalid
		}
		if _, err := time.Parse("15:04", spec.LocalTime); err != nil {
			return invalid
		}
		if spec.Weekday < 0 || spec.Weekday > 6 || spec.Frequency == "daily" && spec.Weekday != 0 {
			return invalid
		}
	case "interval":
		if spec.IntervalHours < 1 || spec.IntervalHours > 720 || spec.LocalTime != "" || spec.Weekday != 0 {
			return invalid
		}
	default:
		return invalid
	}
	return nil
}

// NextScheduleRun is strictly in the future. Civil days are visited once:
// a DST fold cannot create a second slot; a gap advances to the first reachable
// minute at or after the user's local time on that civil day.
func NextScheduleRun(spec records.ScheduleSpec, anchorMS int64, after time.Time) (int64, error) {
	if err := validateScheduleSpec(spec); err != nil {
		return 0, err
	}
	if spec.Frequency == "interval" {
		step := int64(spec.IntervalHours) * int64(time.Hour/time.Millisecond)
		if anchorMS > after.UnixMilli() {
			return anchorMS, nil
		}
		return anchorMS + ((after.UnixMilli()-anchorMS)/step+1)*step, nil
	}
	loc, _ := time.LoadLocation(spec.Timezone)
	clock, _ := time.Parse("15:04", spec.LocalTime)
	target := clock.Hour()*60 + clock.Minute()
	y, m, d := after.In(loc).Date()
	civil := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	for offset := 0; offset < 16; offset++ {
		day := civil.AddDate(0, 0, offset)
		if spec.Frequency == "weekly" && int(day.Weekday()) != spec.Weekday {
			continue
		}
		y, m, d := day.Date()
		wantedCivil := time.Date(y, m, d, target/60, target%60, 0, 0, time.UTC)
		noon := time.Date(y, m, d, 12, 0, 0, 0, loc)
		for minute := -18 * 60; minute <= 18*60; minute++ {
			candidate := noon.Add(time.Duration(minute) * time.Minute)
			cy, cm, cd := candidate.In(loc).Date()
			local := candidate.In(loc)
			civilMinute := time.Date(cy, cm, cd, local.Hour(), local.Minute(), 0, 0, time.UTC)
			if !civilMinute.Before(wantedCivil) {
				if candidate.After(after) {
					return candidate.UnixMilli(), nil
				}
				break
			}
		}
	}
	return 0, records.MergeError("invalid_request")
}
