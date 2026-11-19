package simpledesktop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type projectSuggestion struct{ ID, Name, Kind string }
type projectCreateState struct {
	Open, Focus, Loading, Saving, Submitted, Loaded                 bool
	Name, Folder, TargetProject, ResultID, Message, SuggestionError string
	Selected                                                        *projectSuggestion
	Suggestions                                                     []projectSuggestion
	Request                                                         uint64
}

func (a *App) openProjectCreation(projectID string) {
	a.projectUI.SidebarMenuOpen = false
	a.closeUnifiedSearch()
	s := &a.projectCreate
	s.Open = true
	s.Focus = true
	if s.Submitted {
		return
	}
	if s.TargetProject != projectID {
		*s = projectCreateState{Open: true, Focus: true, TargetProject: projectID, Request: s.Request + 1}
	}
	if projectID != "" {
		for _, p := range a.access.Directory.Projects {
			if p.ID == projectID {
				s.Selected = &projectSuggestion{p.ID, p.Name, "project"}
				s.Name = p.Name
				break
			}
		}
	}
	if projectID == "" && !s.Loaded && !s.Loading {
		a.loadProjectSuggestions()
	}
}
func (a *App) loadProjectSuggestions() {
	s := &a.projectCreate
	client, ok := a.accessClient()
	if !ok || a.Update == nil {
		return
	}
	s.Loading = true
	s.Request++
	request := s.Request
	registered := append([]AccessProject(nil), a.access.Directory.Projects...)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		rows, err := client.linkableDatasets(ctx)
		suggestions := []projectSuggestion{}
		known := map[string]bool{}
		for _, p := range registered {
			known[p.Dataset] = true
		}
		if err == nil {
			for _, d := range rows {
				if !known[d.ID] {
					suggestions = append(suggestions, projectSuggestion{d.ID, d.Name, "dataset"})
				}
			}
		}
		a.Update(func() {
			if request != s.Request || a.Config.ProjectControl == nil || *a.Config.ProjectControl != client.Config {
				return
			}
			s.Loading = false
			s.Loaded = true
			s.Suggestions = suggestions
			s.SuggestionError = ""
			if err != nil {
				s.SuggestionError = "暂时无法读取已有项目记录"
			}
		})
	}()
}
func (a *App) selectProjectFolder() {
	s := &a.projectCreate
	if s.TargetProject != "" || s.Saving || s.Submitted || a.ChooseProjectFolder == nil {
		return
	}
	path, err := a.ChooseProjectFolder()
	if err != nil {
		s.Message = "暂时无法打开文件夹选择器"
		return
	}
	if path == "" {
		return
	}
	s.Folder = filepath.Clean(path)
	s.Message = ""
	if strings.TrimSpace(s.Name) == "" {
		s.Name = filepath.Base(s.Folder)
	}
}
func (a *App) submitProjectCreation() {
	s := &a.projectCreate
	client, ok := a.accessClient()
	if !ok || s.Saving || s.Submitted || !a.access.Loaded || a.access.Busy || a.Update == nil {
		return
	}
	name, folder := strings.TrimSpace(s.Name), strings.TrimSpace(s.Folder)
	if name == "" || (s.TargetProject == "" && s.Selected == nil && folder == "") || (folder != "" && !filepath.IsAbs(folder)) {
		s.Message = "请填写项目名称并添加源文件夹"
		return
	}
	s.Saving = true
	s.Submitted = true
	s.Message = ""
	a.access.Busy = true
	s.Request++
	request := s.Request
	revision := a.access.Directory.Revision
	selected := s.Selected
	target := s.TargetProject
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 125*time.Second)
		defer cancel()
		var project AccessProject
		var err error
		if target != "" {
			project, err = client.rename(ctx, revision, target, name)
		} else if selected != nil && selected.Kind == "project" && folder == "" {
			project.ID = selected.ID
		} else if selected != nil && selected.Kind == "project" {
			project, err = client.alias(ctx, revision, selected.ID, folder)
		} else {
			dataset := ""
			if selected != nil {
				dataset = selected.ID
			}
			project, err = client.create(ctx, revision, name, folder, dataset)
		}
		var directory AccessDirectory
		if err == nil {
			directory, err = client.directory(ctx)
			if err == nil && target == "" && selected != nil && selected.Kind == "project" && folder == "" {
				found := false
				for _, row := range directory.Projects {
					if row.ID == selected.ID {
						project = row
						found = true
						break
					}
				}
				if !found {
					err = &ControlResponseError{Status: 404, Message: "项目管理请求未确认（HTTP 404），请核对原操作"}
				}
			}
		}
		a.Update(func() {
			if request != s.Request || a.Config.ProjectControl == nil || *a.Config.ProjectControl != client.Config {
				return
			}
			s.Saving = false
			a.access.Busy = false
			s.ResultID = project.ID
			if err != nil {
				s.Message = "保存结果尚未确认，请核对本次操作。"
				if strings.Contains(err.Error(), "设置已变化") || strings.Contains(err.Error(), "HTTP 400") || strings.Contains(err.Error(), "HTTP 403") || strings.Contains(err.Error(), "HTTP 404") {
					s.Submitted = false
					s.Message = "未能保存，请检查项目名称与记录后重试。"
					a.RefreshProjectDirectory()
				}
				return
			}
			a.finishProjectCreation(directory, project.ID)
		})
	}()
}
func (a *App) confirmProjectCreation() {
	s := &a.projectCreate
	client, ok := a.accessClient()
	if !ok || s.Saving || !s.Submitted || a.Update == nil {
		return
	}
	s.Saving = true
	request := s.Request
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		directory, err := client.directory(ctx)
		a.Update(func() {
			if request != s.Request || a.Config.ProjectControl == nil || *a.Config.ProjectControl != client.Config {
				return
			}
			s.Saving = false
			if err != nil {
				s.Message = "暂时无法核对，原保存操作已保留。"
				return
			}
			id := ""
			for _, p := range directory.Projects {
				if s.TargetProject != "" {
					if p.ID == s.TargetProject && p.Name == strings.TrimSpace(s.Name) {
						id = p.ID
					}
					continue
				}
				if s.Selected != nil && ((s.Selected.Kind == "project" && p.ID != s.Selected.ID) || (s.Selected.Kind == "dataset" && p.Dataset != s.Selected.ID)) {
					continue
				}
				if s.Selected != nil && s.Folder == "" && p.Name == strings.TrimSpace(s.Name) {
					id = p.ID
					break
				}
				for _, alias := range p.Aliases {
					if filepath.Clean(alias) == filepath.Clean(s.Folder) {
						id = p.ID
						break
					}
				}
			}
			if id == "" {
				a.applyAccessDirectory(directory)
				s.Message = "尚未确认本次保存结果，未重复提交。"
				return
			}
			a.finishProjectCreation(directory, id)
		})
	}()
}
func (a *App) finishProjectCreation(directory AccessDirectory, id string) {
	s := &a.projectCreate
	a.applyAccessDirectory(directory)
	if id == "" {
		s.Message = "项目记录尚未确认，请核对本次操作。"
		return
	}
	editing := s.TargetProject != ""
	s.Open = false
	s.Submitted = false
	s.Name = ""
	s.Folder = ""
	s.Selected = nil
	s.ResultID = ""
	s.Loaded = false
	s.TargetProject = ""
	if editing {
		return
	}
	for index, p := range a.Config.Projects {
		if p.ID == id {
			a.navigate("知识", index)
			return
		}
	}
	s.Message = fmt.Sprintf("项目 %s 已创建", id)
}

func (a *App) acceptProjectFolderDrop(paths []string) {
	s := &a.projectCreate
	if s.TargetProject != "" || s.Saving || s.Submitted {
		return
	}
	if len(paths) != 1 {
		s.Message = "请一次添加一个项目文件夹"
		return
	}
	info, err := os.Stat(paths[0])
	if err != nil || !info.IsDir() || !filepath.IsAbs(paths[0]) {
		s.Message = "请拖入文件夹"
		return
	}
	s.Folder = filepath.Clean(paths[0])
	s.Message = ""
	if strings.TrimSpace(s.Name) == "" {
		s.Name = filepath.Base(s.Folder)
	}
}
