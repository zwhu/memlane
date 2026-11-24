package simpledesktop

import (
	"fmt"
	"strconv"
	"strings"
)

type appRoute struct {
	Page    string
	Project int
	Kind    string
}

func (r appRoute) Key() string {
	return fmt.Sprintf("%s/%d/%s", r.Page, r.Project, r.Kind)
}

func (r appRoute) IsProjectScoped() bool {
	return r.Page == "知识" || r.Page == "通用知识" || r.Page == "图谱"
}

func parsePaneRoute(key string) (appRoute, bool) {
	parts := strings.SplitN(key, "/", 3)
	if len(parts) != 3 {
		return appRoute{}, false
	}
	index, err := strconv.Atoi(parts[1])
	if err != nil {
		return appRoute{}, false
	}
	return appRoute{Page: parts[0], Project: index, Kind: parts[2]}, true
}

func (a *App) currentRoute() appRoute {
	return appRoute{Page: a.page, Project: a.project, Kind: a.kind}
}
