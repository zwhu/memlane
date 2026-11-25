package app

func (a *App) returnFromSource(s *pane) {
	s.SubagentParent, s.SubagentParentPage = nil, nil
	s.SessionSubagents = false
	if s.SourceReturn != nil {
		location := s.SourceReturn
		s.SourceReturn, s.History = nil, nil
		a.restoreReadingLocation(location)
		return
	}
	if len(s.History) == 0 {
		return
	}
	last := len(s.History) - 1
	original := s.History[last]
	history := append([]Item(nil), s.History[:last]...)
	floor := a.overviewSourceFloor[s]
	a.selectItem(s, original)
	s.History = history
	a.clampOverviewFloor(s, floor, len(history))
}
