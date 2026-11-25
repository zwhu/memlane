package settings

import "github.com/zwhu/memlane/app/api"

type activityRow = api.ActivityRow
type overviewEvent = api.ActivityEvent

func aggregateActivity(rows []activityRow, projects []Project) []overviewEvent {
	names := make(map[string]string, len(projects))
	for _, p := range projects {
		names[p.Dataset] = p.Name
	}
	return api.AggregateActivity(rows, names)
}
