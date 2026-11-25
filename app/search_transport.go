package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	appsearch "github.com/zwhu/memlane/app/search"
	"github.com/zwhu/memlane/app/settings"
)

func (a *App) searchFetch(scope string) appsearch.Fetcher[Item, searchLaneResult] {
	projects, client := a.searchCatalog(scope), a.Client
	control, hasControl := a.accessClient()
	recallStartedAt := a.overviewState.V2RecallStartedAt
	page := a.searchPage
	if hasControl {
		page.ClientFactory = func() (appsearch.Searcher, error) {
			identity, err := readingIdentity(Client{Config: client.Config})
			if err != nil {
				return nil, err
			}
			return newAppAPIClient(control, identity)
		}
	}
	modern := page.APIFetcher(scope)
	return func(ctx context.Context, query string, previous searchLaneResult, publish func(appsearch.Publication[Item, searchLaneResult])) {
		readingID, _ := readingIdentity(Client{Config: client.Config})
		publishRows := func(seq uint64, lanes searchLaneResult, rows []Item, err error, done bool) {
			message := ""
			if err != nil {
				if query == "" {
					message = "最近内容读取失败：" + settings.ControlUserError(err)
				} else if done || lanes.KeywordTimedOut {
					message = lanes.message()
				}
			}
			publish(appsearch.Publication[Item, searchLaneResult]{Sequence: seq, Lanes: lanes, Rows: rows, Err: err, Message: message,
				Loaded: err == nil || query != "" && lanes.hasAnySuccess(), Done: done})
		}
		if len(projects) == 0 {
			publishRows(1, searchLaneResult{MetadataOK: true, SemanticOK: true, KeywordOK: true}, []Item{}, nil, true)
			return
		}
		if query == "" {
			if !hasControl {
				publishRows(1, searchLaneResult{}, nil, fmt.Errorf("控制面服务不可用，无法加载最近记忆"), true)
				return
			}
			rows, err := searchRecentItemsV2(ctx, control, projects, scope, readingID, recallStartedAt)
			if errors.Is(err, errGatewaySearchResponse) || isControlNotFound(err) {
				modern(ctx, query, previous, publish)
				return
			}
			publishRows(1, searchLaneResult{}, rows, err, true)
			return
		}
		if hasControl {
			if err := control.requireReadingContract(ctx); err != nil {
				if isControlNotFound(err) {
					modern(ctx, query, previous, publish)
					return
				}
				publishRows(1, searchLaneResult{MetadataError: err}, nil, err, true)
				return
			}
			useGateway, err := control.probeFeature(ctx, "async_capture")
			if err != nil {
				publishRows(1, searchLaneResult{MetadataError: err}, nil, err, true)
				return
			}
			if useGateway {
				modern(ctx, query, previous, publish)
				return
			}
			projectID := ""
			datasetID := ""
			if len(projects) == 1 {
				if scope == "@common" {
					datasetID = projects[0].Dataset
				} else if scope != "" {
					projectID = projects[0].ID
				}
			}
			allowedDatasets := make(map[string]bool, len(projects))
			for _, p := range projects {
				allowedDatasets[p.Dataset] = true
			}
			var mu sync.Mutex
			var seq uint64
			lanes := previous
			publishProgressive := func(done bool) {
				mu.Lock()
				seq++
				mySeq := seq
				snap := lanes
				mu.Unlock()
				publishRows(mySeq, snap, snap.rows(query), snap.err(), done)
			}
			var wg sync.WaitGroup
			if !lanes.MetadataOK {
				wg.Add(1)
				go func() {
					defer wg.Done()
					resp, err := control.searchMetadataV2(ctx, readingID, query, "all", 30, projectID, datasetID)
					var items []Item
					if err == nil {
						items = make([]Item, 0, len(resp.Items))
						for _, row := range resp.Items {
							if row.DocID == "" {
								continue
							}
							if !allowedDatasets[row.DatasetID] {
								continue
							}
							item := row.toItem(recallStartedAt)
							if readingID == "" {
								item.ServerUnreadKnown = false
								item.ServerUnread = false
							}
							items = append(items, item)
						}
					}
					mu.Lock()
					lanes.Metadata = items
					lanes.MetadataError = err
					lanes.MetadataOK = err == nil
					mu.Unlock()
					publishProgressive(false)
				}()
			}
			if !lanes.SemanticOK {
				wg.Add(1)
				go func() {
					defer wg.Done()
					items, err := client.search(ctx, projects, query)
					mu.Lock()
					lanes.Semantic = items
					lanes.SemanticError = err
					lanes.SemanticOK = err == nil
					mu.Unlock()
					publishProgressive(false)
				}()
			}
			if !lanes.KeywordOK {
				wg.Add(1)
				go func() {
					defer wg.Done()
					lexCtx, lexCancel := context.WithTimeout(ctx, lexicalSearchSoftTimeout)
					items, err := client.searchType(lexCtx, projects, query, "CHUNKS_LEXICAL")
					timedOut := err != nil && ctx.Err() == nil && (lexCtx.Err() == context.DeadlineExceeded || strings.Contains(err.Error(), context.DeadlineExceeded.Error()))
					lexCancel()
					mu.Lock()
					lanes.Keyword = items
					lanes.KeywordError = err
					lanes.KeywordOK = err == nil
					lanes.KeywordTimedOut = timedOut
					mu.Unlock()
					publishProgressive(false)
				}()
			}
			wg.Wait()
			publishProgressive(true)
			return
		}
		lanes := client.searchLanes(ctx, projects, query, previous)
		publishRows(1, lanes, lanes.rows(query), lanes.err(), true)
	}
}

var errGatewaySearchResponse = errors.New("search recent uses the gateway response contract")

func searchRecentItemsV2(ctx context.Context, control ProjectControlClient, projects []Project, scope, readingID, recallStartedAt string) ([]Item, error) {
	if len(projects) == 0 {
		return []Item{}, nil
	}
	projectID := ""
	datasetID := ""
	if len(projects) == 1 {
		if scope == "@common" {
			datasetID = projects[0].Dataset
		} else if scope != "" {
			projectID = projects[0].ID
		}
	}
	allowed := make(map[string]bool, len(projects))
	for _, p := range projects {
		allowed[p.Dataset] = true
	}
	resp, err := control.searchRecentV2(ctx, readingID, 20, projectID, datasetID)
	if err == nil && len(resp.RecentItems) != 0 && string(resp.RecentItems) != "null" {
		return nil, errGatewaySearchResponse
	}
	if err == nil {
		result := make([]Item, 0, 12)
		seen := map[string]bool{}
		kCount := 0
		for _, row := range resp.KnowledgeItems {
			if row.DocID == "" || !allowed[row.DatasetID] {
				continue
			}
			item := row.toItem(recallStartedAt)
			if readingID == "" {
				item.ServerUnreadKnown = false
				item.ServerUnread = false
			}
			if seen[item.Key()] {
				continue
			}
			seen[item.Key()] = true
			result = append(result, item)
			kCount++
			if kCount >= 6 {
				break
			}
		}
		sCount := 0
		for _, row := range resp.SessionItems {
			if row.SessionID == "" || !allowed[row.DatasetID] {
				continue
			}
			item := row.toItem()
			if seen[item.Key()] {
				continue
			}
			seen[item.Key()] = true
			result = append(result, item)
			sCount++
			if sCount >= 6 {
				break
			}
		}
		return result, nil
	}
	return nil, err
}
