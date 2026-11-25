package settings

import (
	"path/filepath"
	"strconv"
	"strings"
)

func SearchProjects(projects []Project, id string) []Project {
	if id == "" {
		return projects
	}
	for _, p := range projects {
		if p.ID == id {
			return []Project{p}
		}
	}
	return nil
}

// Names remain editable labels. Paths disambiguate their presentation; stable
// project IDs remain the selection/query identity even when names collide.
func ProjectLabels(projects []Project) []string {
	labels := make([]string, len(projects))
	used := map[string]bool{"全部项目": true}
	for i, p := range projects {
		label := p.Name
		duplicates := 0
		for _, other := range projects {
			if other.Name == p.Name {
				duplicates++
			}
		}
		if duplicates > 1 || used[label] {
			parts := strings.Split(filepath.Dir(filepath.Clean(p.Path)), string(filepath.Separator))
			context := "未关联路径"
			if p.Path != "" {
				for n := 1; n <= len(parts); n++ {
					context = filepath.Join(parts[len(parts)-n:]...)
					unique := true
					for j, other := range projects {
						if j == i || other.Name != p.Name {
							continue
						}
						otherParts := strings.Split(filepath.Dir(filepath.Clean(other.Path)), string(filepath.Separator))
						if context == filepath.Join(otherParts[max(0, len(otherParts)-n):]...) {
							unique = false
							break
						}
					}
					if unique {
						break
					}
				}
			}
			label += " · " + context
		}
		for used[label] {
			label += " · " + strconv.Itoa(i+1)
		}
		labels[i], used[label] = label, true
	}
	return labels
}
