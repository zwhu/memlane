package simpledesktop

// Appending only reveals metadata from the last successful catalog read. A
// changing upstream offset cannot shift records between successive batches.
func (a *App) appendRawRecords(s *pane) {
	if s.Loading || !s.Loaded || !s.More || len(s.RawCatalog) <= len(s.Items) {
		return
	}
	end := min(len(s.Items)+30, len(s.RawCatalog))
	s.Items = append(s.Items, s.RawCatalog[len(s.Items):end]...)
	s.More = end < len(s.RawCatalog)
	a.startTitles(s)
}

func (a *App) rawRecordsAutoLoad(s *pane) {
	if s.SourceReturn == nil && s.Query == "" && s.ListScroll.MaxY > 0 && s.ListScroll.Y >= s.ListScroll.MaxY-160 {
		a.appendRawRecords(s)
	}
}
