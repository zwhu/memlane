package simpledesktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type manualSearchEntry struct {
	Query string    `json:"query"`
	At    time.Time `json:"searched_at"`
}

func (c Client) manualSearchHistoryPath() (string, error) {
	base := c.Config.ClientStateFile
	if base == "" {
		base = c.Config.ConfigFile
	}
	if base == "" {
		return "", fmt.Errorf("搜索历史存储位置不可用")
	}
	identity, err := readingIdentity(Client{Config: c.Config})
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(base), fmt.Sprintf("manual-search-history-%x.json", sha256.Sum256([]byte(identity)))), nil
}

func (c Client) manualSearchHistory() ([]manualSearchEntry, error) {
	path, err := c.manualSearchHistoryPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rows []manualSearchEntry
	err = json.Unmarshal(b, &rows)
	return rows, err
}

// Only the search dialog's edited input calls this. Agent/API searches never do.
func (a *App) rememberManualSearch() {
	s := &a.searchUI
	if !s.HistoryDirty {
		return
	}
	query := strings.TrimSpace(s.Query)
	if query == "" {
		s.HistoryDirty = false
		return
	}
	rows, err := a.Client.manualSearchHistory()
	if err != nil {
		a.overviewState.HistoryError = "搜索历史读取失败：" + err.Error()
		return
	}
	updated := []manualSearchEntry{{Query: query, At: time.Now()}}
	for _, row := range rows {
		if row.Query != query && len(updated) < 100 {
			updated = append(updated, row)
		}
	}
	path, err := a.Client.manualSearchHistoryPath()
	if err == nil {
		var b []byte
		b, err = json.Marshal(updated)
		if err == nil {
			err = os.WriteFile(path+".tmp", b, 0600)
		}
		if err == nil {
			err = os.Rename(path+".tmp", path)
		}
	}
	if err != nil {
		a.overviewState.HistoryError = "搜索历史保存失败：" + err.Error()
		return
	}
	s.HistoryDirty = false
	if a.overviewState.SearchHistory {
		a.overviewPageController().CancelActivity()
	}
	a.overviewState.History, _ = a.Client.overviewSearchHistory(context.Background())
	a.overviewState.HistoryLoaded, a.overviewState.HistoryError = true, ""
}

func (c Client) overviewSearchHistory(ctx context.Context) ([]overviewEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := c.manualSearchHistory()
	if err != nil {
		return nil, err
	}
	events := []overviewEvent{}
	for n, row := range rows {
		events = append(events, overviewEvent{Key: fmt.Sprint("manual-search/", n), Title: row.Query, State: "搜索记录", Project: "手动搜索", At: row.At})
	}
	return events, nil
}
