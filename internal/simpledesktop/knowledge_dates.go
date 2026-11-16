package simpledesktop

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var knowledgeDatesMu sync.Mutex

func sourceCalendarDate(value string) (string, bool) {
	t, err := time.Parse("2006-01-02", value)
	return t.Format("2006-01-02"), err == nil && len(value) == 10
}

// A calendar date has no time zone or invented hour. Manual knowledge uses its
// original creation date; session-derived knowledge needs source provenance.
func (d Document) knowledgeDay(location *time.Location) (string, bool) {
	// A patched lesson without linked QAs explicitly reports unknown provenance.
	// Its latest session context must not masquerade as a first occurrence.
	if metadataString(d, "source_date_status") == "unconfirmed" && metadataString(d, "first_source_date") == "" {
		return "", false
	}
	for _, value := range []string{d.FirstSourceDate, metadataString(d, "first_source_date"), metadataString(d, "session_date")} {
		if day, ok := sourceCalendarDate(value); ok {
			return day, true
		}
	}
	for _, set := range nodeSets(d) {
		if set == "session_learnings" {
			return "", false
		}
	}
	if at, ok := officialTime(d.Created); ok {
		return at.In(location).Format("2006-01-02"), true
	}
	return "", false
}

func metadataString(d Document, key string) string {
	value, _ := d.Metadata[key].(string)
	return value
}

func dateOrder(value string) time.Time {
	if day, ok := sourceCalendarDate(value); ok {
		// Calendar ordinal for sorting only; this is never shown as a timestamp.
		at, _ := time.Parse("2006-01-02", day)
		return at
	}
	at, _ := officialTime(value)
	return at
}

// Keep only the earliest evidenced source day, keyed by project and stable
// lesson identity. No text, vector, session body or inferred date is stored.
func (c Client) rememberKnowledgeDays(rows []Document) error {
	base := c.Config.ClientStateFile
	if base == "" {
		base = c.Config.ConfigFile
	}
	if base == "" {
		return nil
	}
	identity, err := readingIdentity(Client{Config: c.Config})
	if err != nil {
		return err
	}
	knowledgeDatesMu.Lock()
	defer knowledgeDatesMu.Unlock()
	path := filepath.Join(filepath.Dir(base), fmt.Sprintf("knowledge-source-dates-%x.json", sha256.Sum256([]byte(identity))))
	state := struct {
		Version int               `json:"schema_version"`
		Days    map[string]string `json:"days"`
	}{Version: 1, Days: map[string]string{}}
	if info, e := os.Lstat(path); e == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 8*1024*1024 {
			return fmt.Errorf("记忆来源日期记录不可用")
		}
		data, e := os.ReadFile(path)
		if e != nil || json.Unmarshal(data, &state) != nil || state.Version != 1 || state.Days == nil {
			return fmt.Errorf("记忆来源日期记录无效")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	changed := false
	for n := range rows {
		d := &rows[n]
		if !d.knowledge() {
			continue
		}
		day, ok := d.knowledgeDay(time.Local)
		if !ok {
			continue
		}
		stable := d.ID
		if key := metadataString(*d, "state_key"); key != "" {
			stable = metadataString(*d, "lesson_type") + "/" + key
		}
		key := fmt.Sprintf("%x", sha256.Sum256([]byte(d.Dataset+"/"+stable)))
		old := state.Days[key]
		if old != "" {
			if _, valid := sourceCalendarDate(old); !valid {
				return fmt.Errorf("记忆来源日期记录无效")
			}
		}
		if old == "" || day < old {
			state.Days[key], changed = day, true
		}
		d.FirstSourceDate = state.Days[key]
	}
	if !changed {
		return nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".knowledge-dates-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err = temporary.Write(data); err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}
