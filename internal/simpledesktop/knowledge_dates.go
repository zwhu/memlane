package simpledesktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var knowledgeDatesMu sync.Mutex

var provenanceBackfillTracker = struct {
	mu        sync.Mutex
	completed map[string]bool
	inflight  map[string]chan struct{}
}{
	completed: map[string]bool{},
	inflight:  map[string]chan struct{}{},
}

func sourceCalendarDate(value string) (string, bool) {
	t, err := time.Parse("2006-01-02", value)
	return t.Format("2006-01-02"), err == nil && len(value) == 10
}

func isLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func knowledgeSourceDatesPath(cfg Config, identity string) (string, bool) {
	base := cfg.ClientStateFile
	if base == "" {
		base = cfg.ConfigFile
	}
	if base == "" || identity == "" {
		return "", false
	}
	return filepath.Join(filepath.Dir(base), fmt.Sprintf("knowledge-source-dates-%x.json", sha256.Sum256([]byte(identity)))), true
}

func readLegacyKnowledgeDatesFile(path string) (map[string]string, bool, error) {
	linfo, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !linfo.Mode().IsRegular() || linfo.Mode()&os.ModeSymlink != 0 || linfo.Mode().Perm()&0077 != 0 || linfo.Size() > 8*1024*1024 {
		return nil, true, fmt.Errorf("记忆来源日期记录不可用")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, true, err
	}
	defer f.Close()
	finfo, err := f.Stat()
	if err != nil || !os.SameFile(linfo, finfo) || !finfo.Mode().IsRegular() || finfo.Mode().Perm()&0077 != 0 || finfo.Size() > 8*1024*1024 {
		return nil, true, fmt.Errorf("记忆来源日期记录不可用")
	}
	data, err := io.ReadAll(io.LimitReader(f, 8*1024*1024+1))
	if err != nil {
		return nil, true, err
	}
	if int64(len(data)) > 8*1024*1024 {
		return nil, true, fmt.Errorf("记忆来源日期记录不可用")
	}
	var state struct {
		Version int               `json:"schema_version"`
		Days    map[string]string `json:"days"`
	}
	if json.Unmarshal(data, &state) != nil || state.Version != 1 || state.Days == nil {
		return nil, true, fmt.Errorf("记忆来源日期记录无效")
	}
	days := make(map[string]string, len(state.Days))
	for k, v := range state.Days {
		if !isLowerHex64(k) {
			return nil, true, fmt.Errorf("记忆来源日期记录无效")
		}
		day, ok := sourceCalendarDate(v)
		if !ok {
			return nil, true, fmt.Errorf("记忆来源日期记录无效")
		}
		days[k] = day
	}
	return days, true, nil
}

const (
	provenanceBackfillChunkMaxEntries = 500
	provenanceBackfillChunkMaxBytes   = 60 * 1024
)

func chunkLegacyKnowledgeDays(days map[string]string, maxEntries, maxBytes int) []map[string]string {
	if len(days) == 0 {
		return nil
	}
	if maxEntries <= 0 {
		maxEntries = provenanceBackfillChunkMaxEntries
	}
	if maxBytes <= 0 {
		maxBytes = provenanceBackfillChunkMaxBytes
	}
	keys := make([]string, 0, len(days))
	for k := range days {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var chunks []map[string]string
	cur := make(map[string]string, min(len(keys), maxEntries))
	approxBytes := 256
	for _, k := range keys {
		v := days[k]
		entryBytes := len(k) + len(v) + 12
		if len(cur) > 0 && (len(cur) >= maxEntries || approxBytes+entryBytes > maxBytes) {
			chunks = append(chunks, cur)
			cur = make(map[string]string, min(len(keys), maxEntries))
			approxBytes = 256
		}
		cur[k] = v
		approxBytes += entryBytes
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

// backfillLegacyKnowledgeDatesV2 performs a one-shot, single-identity migration
// of knowledge-source-dates-<sha256(identity)>.json through the control plane
// to the single-writer official_service_dev provenance store (evidence level
// legacy_json_locked). When supported and settled, the client stops writing
// local knowledge-source-dates-*.json files.
func (c Client) backfillLegacyKnowledgeDatesV2(ctx context.Context) (bool, error) {
	control, ok := c.controlClient()
	if !ok {
		return false, nil
	}
	hasBackfill, probeErr := control.probeFeature(ctx, v2FeatureProvenanceBackfill)
	if probeErr != nil {
		return false, probeErr
	}
	if !hasBackfill {
		return false, nil
	}
	identity, err := readingIdentity(Client{Config: c.Config})
	if err != nil || identity == "" {
		return false, err
	}
	localPath, hasLocalPath := knowledgeSourceDatesPath(c.Config, identity)
	trackKey := control.Config.URL + "|" + identity + "|" + localPath

	for {
		provenanceBackfillTracker.mu.Lock()
		if provenanceBackfillTracker.completed[trackKey] {
			provenanceBackfillTracker.mu.Unlock()
			return true, nil
		}
		if waitCh, busy := provenanceBackfillTracker.inflight[trackKey]; busy {
			provenanceBackfillTracker.mu.Unlock()
			select {
			case <-waitCh:
				continue
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}
		doneCh := make(chan struct{})
		provenanceBackfillTracker.inflight[trackKey] = doneCh
		provenanceBackfillTracker.mu.Unlock()

		var doneOK bool
		defer func() {
			provenanceBackfillTracker.mu.Lock()
			delete(provenanceBackfillTracker.inflight, trackKey)
			if doneOK {
				if len(provenanceBackfillTracker.completed) >= 128 {
					clear(provenanceBackfillTracker.completed)
				}
				provenanceBackfillTracker.completed[trackKey] = true
			}
			close(doneCh)
			provenanceBackfillTracker.mu.Unlock()
		}()

		// Same-machine default: send only reading_identity so the server reads
		// strictly state_dir/knowledge-source-dates-<sha256(identity)>.json.
		resp, reqErr := control.provenanceBackfillLegacyDatesV2(ctx, v2ProvenanceBackfillRequest{
			ReadingIdentity: identity,
		})
		if reqErr != nil {
			return false, reqErr
		}
		switch resp.Status {
		case "migrated":
			if !resp.HasUnconfirmedProjects && resp.Unmapped == 0 {
				doneOK = true
				return true, nil
			}
			return false, nil
		case "absent", "empty":
			// If the client's own state_dir differs from the server's state_dir
			// and this exact readingIdentity's legacy file exists locally, pass
			// its validated days in <=500 entry (<64 KiB) chunks and only rename
			// to .migrated.bak once unmapped == 0 and all projects are confirmed.
			if hasLocalPath {
				knowledgeDatesMu.Lock()
				days, exists, readErr := readLegacyKnowledgeDatesFile(localPath)
				knowledgeDatesMu.Unlock()
				if readErr == nil && exists && len(days) > 0 {
					chunks := chunkLegacyKnowledgeDays(days, provenanceBackfillChunkMaxEntries, provenanceBackfillChunkMaxBytes)
					totalUnmapped := 0
					anyUnconfirmed := false
					for _, chunk := range chunks {
						resp2, err2 := control.provenanceBackfillLegacyDatesV2(ctx, v2ProvenanceBackfillRequest{
							ReadingIdentity: identity,
							Days:            chunk,
						})
						if err2 != nil {
							return false, err2
						}
						if resp2.Status != "migrated" && resp2.Status != "empty" && resp2.Status != "absent" {
							return false, fmt.Errorf("来源日期回填状态未确认：%s", resp2.Status)
						}
						totalUnmapped += resp2.Unmapped
						if resp2.HasUnconfirmedProjects {
							anyUnconfirmed = true
						}
					}
					if !anyUnconfirmed && totalUnmapped == 0 {
						knowledgeDatesMu.Lock()
						if _, statErr := os.Lstat(localPath); statErr == nil {
							_ = os.Rename(localPath, localPath+".migrated.bak")
						}
						knowledgeDatesMu.Unlock()
						doneOK = true
						return true, nil
					}
					// Retain local legacy JSON so future startups retry once
					// unconfirmed projects finish registering.
					return false, nil
				}
			}
			doneOK = true
			return true, nil
		default:
			return false, fmt.Errorf("来源日期回填状态未确认：%s", resp.Status)
		}
	}
}

func (a *App) triggerProvenanceBackfillOnce() {
	if a.Config.ProjectControl == nil {
		return
	}
	client := a.Client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = client.backfillLegacyKnowledgeDatesV2(ctx)
	}()
}

// A calendar date has no time zone or invented hour. Under the zero-patch Cognee
// model (Option B), knowledge resolves dates by: earliest remembered key ->
// explicit first_source_date -> native session_date -> local createdAt fallback.
func (d Document) knowledgeDay(location *time.Location) (string, bool) {
	for _, value := range []string{d.FirstSourceDate, metadataString(d, "first_source_date"), metadataString(d, "session_date")} {
		if day, ok := sourceCalendarDate(value); ok {
			return day, true
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
// When the control plane advertises provenance_backfill_v2, the server-side
// single writer (official_service_dev) becomes authoritative and the client
// stops writing local knowledge-source-dates-*.json files.
func (c Client) rememberKnowledgeDays(rows []Document) error {
	if _, ok := c.controlClient(); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		serverAuthoritative, _ := c.backfillLegacyKnowledgeDatesV2(ctx)
		cancel()
		if serverAuthoritative {
			return nil
		}
	}
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
