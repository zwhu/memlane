package app

func (a *App) stepCommonKnowledgeV2(s *pane, delta int) {
	if s == nil || !s.CommonV2Mode || s.Loading {
		return
	}
	s.HomeAnchor = nil
	if delta < 0 {
		if s.CommonPageIndex > 0 {
			s.CommonPageIndex--
			s.Offset = s.CommonPageIndex * 30
			a.load(s)
			return
		}
		if !s.CommonHasPrev {
			return
		}
		prevOpts := v2KnowledgePageOpts{Cursor: s.CommonPrevCursor, Direction: "prev"}
		if prevOpts.Cursor == "" && len(s.Items) > 0 {
			prevOpts.AnchorDocID = s.Items[0].ID
		}
		if len(s.CommonNav) == 0 {
			s.CommonNav = []v2KnowledgePageOpts{{}}
		}
		s.CommonNav = append([]v2KnowledgePageOpts{prevOpts}, s.CommonNav...)
		s.CommonCursors = append([]string{prevOpts.Cursor}, s.CommonCursors...)
		s.CommonPageIndex = 0
		s.Offset = 0
		a.load(s)
		return
	}
	if delta > 0 {
		if !s.More || s.CommonNextCursor == "" {
			return
		}
		if len(s.CommonNav) == 0 {
			s.CommonNav = []v2KnowledgePageOpts{{}}
		}
		if len(s.CommonCursors) == 0 {
			s.CommonCursors = []string{""}
		}
		nextOpts := v2KnowledgePageOpts{Cursor: s.CommonNextCursor, Direction: "next"}
		nextIdx := s.CommonPageIndex + 1
		if nextIdx < len(s.CommonNav) {
			s.CommonNav[nextIdx] = nextOpts
		} else {
			s.CommonNav = append(s.CommonNav, nextOpts)
		}
		if nextIdx < len(s.CommonCursors) {
			s.CommonCursors[nextIdx] = s.CommonNextCursor
		} else {
			s.CommonCursors = append(s.CommonCursors, s.CommonNextCursor)
		}
		s.CommonPageIndex = nextIdx
		s.Offset = s.CommonPageIndex * 30
		a.load(s)
	}
}
