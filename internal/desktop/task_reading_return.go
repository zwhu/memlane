package desktop

// A remembered task is not the source of every subsequently selected record.
// Keep its state for navigation, but offer return only for its actual inputs/results.
func (a *App) organizationReadingReturn(memory string) bool {
	s := &a.organization
	if s.Task == nil || s.Selected == "" || s.Task.ID != s.Selected {
		return false
	}
	for _, v := range s.Task.Snapshot.Inputs {
		if v.Memory == memory {
			return true
		}
	}
	for _, e := range s.Task.Entries {
		if e.Receipt != nil && e.Receipt.Memory == memory {
			return true
		}
	}
	return false
}
func (a *App) mergeReadingReturn(memory string) bool {
	s := &a.merges
	if s.Task == nil || s.Selected == "" || s.Task.ID != s.Selected {
		return false
	}
	if s.Task.Receipt != nil && s.Task.Receipt.Memory == memory {
		return true
	}
	for _, v := range s.Task.Snapshot.Inputs {
		if v.Memory == memory {
			return true
		}
	}
	return false
}
