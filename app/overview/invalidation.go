package overview

func failureDetailMatchesDataset(detail *FailureDetail, dataset string) (matched, known bool) {
	if detail == nil {
		return false, false
	}
	for _, ds := range detail.Datasets {
		if ds == "" {
			continue
		}
		known = true
		if ds == dataset {
			return true, true
		}
	}
	for _, rec := range detail.TechRecords {
		if rec.Dataset == "" {
			continue
		}
		known = true
		if rec.Dataset == dataset {
			return true, true
		}
	}
	return false, known
}

func overviewEventMatchesDataset(ev Event, dataset string) bool {
	if dataset == "" {
		return true
	}
	if ev.Record.Dataset == dataset {
		return true
	}
	for _, f := range ev.Failures {
		if f.Dataset == dataset {
			return true
		}
	}
	if ev.Recall != nil {
		for _, hit := range ev.Recall.Hits {
			if hit.Dataset == dataset {
				return true
			}
		}
	}
	return false
}
func (a *ControllerOf[T]) Invalidate(dataset string) {
	v := a.State
	v.SnapshotVersion++
	// Fence overview, activity, & day in-flight requests so late callbacks cannot repopulate revoked data.
	a.CancelAll()
	v.GroupedEvents = nil
	if dataset == "" {
		v.ScopeKey = ""
		v.Loaded = false
		v.RawLoaded = false
		v.ExpandedEvent = ""
		v.ExpandedReport = ""
		v.ExpandedDetail = nil
		v.CopiedEvent = ""
		v.Error = ""
		v.ActivityError = ""
		v.HistoryError = ""
		v.RecallError = ""
		v.DayItems = nil
		v.DayNextCursor = ""
		v.DayHasMore = false
		v.DayTotal = 0
		v.V2DayBuckets = nil
		v.V2TotalKnowledge = 0
		v.Events = nil
		v.History = nil
		v.HistoryLoaded = false
		v.ActivityLoaded = false
		v.More = false
	} else {
		if len(v.DayItems) > 0 {
			keptDay := make([]T, 0, len(v.DayItems))
			for _, i := range v.DayItems {
				if a.Describe(i).Dataset != dataset {
					keptDay = append(keptDay, i)
				}
			}
			v.DayItems = keptDay
			v.DayNextCursor = ""
			v.DayHasMore = false
		}
		v.DayTotal = len(v.DayItems)
		v.V2DayBuckets = nil
		v.V2TotalKnowledge = 0
		v.Snapshot.Raw = 0
		v.RawLoaded = false
		v.Loaded = false
		v.BucketCache = nil
		v.HeatmapCache = nil
		expandedFoundInEvents := false
		expandedMatchesEvent := false
		if len(v.Events) > 0 {
			keptEvents := make([]Event, 0, len(v.Events))
			for _, ev := range v.Events {
				if ev.Key != "" && ev.Key == v.ExpandedEvent {
					expandedFoundInEvents = true
					if overviewEventMatchesDataset(ev, dataset) {
						expandedMatchesEvent = true
					}
				}
				if ev.Record.Dataset == dataset {
					continue
				}
				if len(ev.Failures) > 0 {
					keptFailures := make([]ActivityRow, 0, len(ev.Failures))
					for _, f := range ev.Failures {
						if f.Dataset != dataset {
							keptFailures = append(keptFailures, f)
						}
					}
					if len(keptFailures) == 0 {
						continue
					}
					ev.Failures = keptFailures
				}
				if ev.Recall != nil {
					keptHits := make([]RecallHit, 0, len(ev.Recall.Hits))
					for _, h := range ev.Recall.Hits {
						if h.Dataset != dataset {
							keptHits = append(keptHits, h)
						}
					}
					if len(keptHits) == 0 {
						continue
					}
					rcCopy := *ev.Recall
					rcCopy.Hits = keptHits
					ev.Recall = &rcCopy
				}
				keptEvents = append(keptEvents, ev)
			}
			v.Events = keptEvents
		}
		detailMatched, detailKnown := failureDetailMatchesDataset(v.ExpandedDetail, dataset)
		if expandedMatchesEvent || detailMatched || (!detailKnown && !expandedFoundInEvents && (v.ExpandedEvent != "" || v.ExpandedReport != "" || v.ExpandedDetail != nil)) {
			v.ExpandedEvent = ""
			v.ExpandedReport = ""
			v.ExpandedDetail = nil
			v.CopiedEvent = ""
		}
		if len(v.History) > 0 {
			keptHistory := make([]Event, 0, len(v.History))
			for _, ev := range v.History {
				if ev.Record.Dataset != dataset {
					keptHistory = append(keptHistory, ev)
				}
			}
			v.History = keptHistory
		}
	}
	if a.WithdrawItem == nil {
		var kept []T
		for _, item := range v.Snapshot.Knowledge {
			if dataset != "" && a.Describe(item).Dataset != dataset {
				kept = append(kept, item)
			}
		}
		v.Snapshot.Knowledge = kept
		return
	}
	for n, i := range v.Snapshot.Knowledge {
		if dataset == "" || a.Describe(i).Dataset == dataset {
			a.WithdrawItem(&v.Snapshot.Knowledge[n])
		}
	}
}

func (a *ControllerOf[T]) CancelAll() {
	v := a.State
	if v.BootstrapCancel != nil {
		v.BootstrapCancel()
		v.BootstrapCancel = nil
	}
	if v.RecallCancel != nil {
		v.RecallCancel()
		v.RecallCancel = nil
	}
	v.Generation++
	v.Loading = false
	a.CancelActivity()
	a.cancelDay()
}
