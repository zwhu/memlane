package simpledesktop

import appoverview "github.com/zwhu/memlane/app/overview"

var middleProjectName = appoverview.MiddleProjectName
var knowledgePreview = appoverview.KnowledgePreview
var groupOverviewFailures = appoverview.GroupFailures

func (a *App) overviewSourceName(item Item) string {
	for _, p := range overviewProjects(a.Config) {
		if p.Dataset == item.Dataset {
			return p.Name
		}
	}
	return "来源未关联"
}
