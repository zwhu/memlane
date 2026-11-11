package desktop

import "time"

// Display only. Exact receipt timestamps remain unchanged in technical details.
func backupDisplayTime(raw string) string { return backupDisplayTimeIn(raw, time.Local) }
func backupDisplayTimeIn(raw string, location *time.Location) string {
	if raw == "" {
		return "时间未记录"
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed.In(location).Format("2006-01-02 15:04")
	}
	// Existing UI verification receipts store an already-local timestamp.
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if parsed, err := time.ParseInLocation(layout, raw, location); err == nil {
			return parsed.Format("2006-01-02 15:04")
		}
	}
	return "时间待核对"
}
