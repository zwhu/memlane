package simpledesktop

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/zwhu/memlane/app/reading"
)

type userProfileDocument = reading.ProfileDocument
type userProfile = reading.Profile
type userProfilesDirectory = reading.ProfileDirectory

type userProfilesState = reading.ProfilesState

var userProfileIcon = homeSVG(`<circle cx="12" cy="8" r="4"/><path d="M4 21v-2a8 8 0 0 1 16 0v2"/>`)

func (a *App) selectedUserProfile() (userProfile, bool) {
	return a.userProfiles.SelectedProfile()
}

func profileController(control ProjectControlClient) (reading.ProfilesController, error) {
	client, err := newAppAPIClient(control, "")
	return reading.ProfilesController{Client: client}, err
}

func (a *App) refreshUserProfiles() {
	s := &a.userProfiles
	if s.Loading || a.Config.ProjectControl == nil || a.Update == nil {
		return
	}
	s.Loading = true
	s.Next = time.Now().Add(4 * time.Second)
	client := ProjectControlClient{Config: *a.Config.ProjectControl, HTTP: a.Client.HTTP}
	shellAdapter := appShellAdapter{a}
	authEpoch := shellAdapter.AuthEpoch()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var result userProfilesDirectory
		controller, err := profileController(client)
		if err == nil {
			result, err = controller.List(ctx)
		}
		gateway := !legacyReadingRouteMissing(err)
		if !gateway {
			err = client.request(ctx, "GET", "/v1/user-profiles", nil, &result)
		}

		a.Update(func() {
			s.Loading = false
			if authEpoch != shellAdapter.AuthEpoch() {
				return
			}
			if err != nil {
				if reading.Status(err, 401, 403) || isControlAuthDenied(err) {
					shellAdapter.InvalidateReading()
				}
				s.Message = "用户画像暂时无法加载，请刷新重试"
				return
			}
			s.Loaded, s.Gateway = true, gateway
			s.Directory = result
			if p, ok := a.selectedUserProfile(); ok && p.Status == "ready" && s.Message == "结果已接收，正在保存" {
				s.Message = ""
			}
			if _, ok := a.selectedUserProfile(); !ok {
				s.Selected = ""
				if len(result.Profiles) > 0 {
					s.Selected = result.Profiles[0].ID
				}
			}
			a.readUserProfile()
		})
	}()
}

func (a *App) selectUserProfile(id string) {
	s := &a.userProfiles
	if s.Select(id) {
		a.readUserProfile()
	}
}

func (a *App) readUserProfile() {
	s := &a.userProfiles
	p, ok := a.selectedUserProfile()
	if !ok || p.Document == nil {
		s.ClearBody()
		return
	}
	key := p.ID + "/" + p.Document.Dataset + "/" + p.Document.ID
	if s.BodyKey != "" && (s.BodyKey != key || p.ContentVersion != "" && s.BodyVersion != p.ContentVersion) {
		s.ClearBody()
	}
	if (key == s.BodyKey && time.Since(s.ReadAt) < 30*time.Second) || s.Reading || a.Config.ProjectControl == nil || a.Update == nil {
		return
	}
	s.ReadGeneration++
	generation, selected := s.ReadGeneration, p.ID
	shellAdapter := appShellAdapter{a}
	authEpoch := shellAdapter.AuthEpoch()
	s.Reading = true
	client := ProjectControlClient{Config: *a.Config.ProjectControl, HTTP: a.Client.HTTP}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var result struct {
			Body           string              `json:"body"`
			Document       userProfileDocument `json:"document"`
			ContentVersion string              `json:"content_hash"`
		}
		controller, err := profileController(client)
		if err == nil {
			var profile userProfile
			profile, result.Body, err = controller.Get(ctx, selected)
			result.ContentVersion = profile.ContentVersion
			if profile.Document != nil {
				result.Document = *profile.Document
			}
		}
		if legacyReadingRouteMissing(err) {
			err = client.request(ctx, "GET", "/v1/user-profiles/"+url.PathEscape(selected)+"/document", nil, &result)
		}

		a.Update(func() {
			if generation != s.ReadGeneration || selected != s.Selected || authEpoch != shellAdapter.AuthEpoch() {
				return
			}
			s.Reading = false
			current, exists := a.selectedUserProfile()
			if !exists || current.Document == nil || *current.Document != *p.Document || current.ContentVersion != p.ContentVersion {
				a.readUserProfile()
				return
			}
			if err != nil {
				if reading.Status(err, 401, 403) || isControlAuthDenied(err) {
					shellAdapter.InvalidateReading()
				} else if readingUnavailable(err) {
					s.ClearBody()
				}
				s.Message = "画像文档暂不可读，请刷新后核对"
				return
			}
			if result.Document != *p.Document || p.ContentVersion != "" && result.ContentVersion != p.ContentVersion {
				s.ClearBody()
				s.Message = "画像文档已变更，请刷新后核对"
				return
			}
			s.Body, s.BodyKey, s.BodyVersion = result.Body, key, result.ContentVersion
			s.ReadAt = time.Now()
		})
	}()
}

func (a *App) userProfileAction(p userProfile, action, body string) {
	s := &a.userProfiles
	if s.Busy || a.Config.ProjectControl == nil || a.Update == nil {
		return
	}
	if p.Status == "saving" && (action == "cancel" || action == "delete") {
		return
	}
	operation := integrationOperation()
	payload := map[string]any{"operation_id": operation}
	if action == "cancel" {
		payload["operation_id"] = p.Operation
	}
	if action == "import" {
		payload["body"] = body
	}
	if action == "reconcile" {
		delete(payload, "operation_id")
	}
	s.Busy, s.Message = true, ""
	control := ProjectControlClient{Config: *a.Config.ProjectControl, HTTP: a.Client.HTTP}
	if action == "generate" || action == "import" {
		// Both endpoints own their operation journal. A save must be allowed to finish.
		hc := newHTTPClient(control.HTTP, 0)
		hc.Timeout = 0
		control.HTTP = hc
	}
	shellAdapter := appShellAdapter{a}
	authEpoch := shellAdapter.AuthEpoch()
	go func() {
		ctx := context.Background()
		controller, err := profileController(control)
		if err == nil && action != "reconcile" {
			err = controller.Act(ctx, p, action, operation, body)
		}
		var result userProfilesDirectory
		legacy := action == "reconcile" || legacyReadingRouteMissing(err) && action != "delete"
		if legacy {
			err = control.request(ctx, http.MethodPost, "/v1/user-profiles/"+url.PathEscape(p.ID)+"/"+action, payload, &result)
		}
		a.Update(func() {
			s.Busy = false
			if authEpoch != shellAdapter.AuthEpoch() {
				return
			}
			if err != nil {
				if reading.Status(err, 401, 403) || isControlAuthDenied(err) {
					appShellAdapter{a}.InvalidateReading()
				}
				s.Message = "操作未确认，正在核对结果"
				a.refreshUserProfiles()
				return
			}
			if action == "import" {
				s.Open = false
			}
			if action == "delete" {
				a.clearUserProfileBody("", "")
			}
			if legacy {
				s.Directory, s.Loaded = result, true
				a.readUserProfile()
				return
			}
			a.refreshUserProfiles()
		})
	}()
}

func (a *App) beginProfileImport(p userProfile) {
	s := &a.userProfiles
	if s.Drafts == nil {
		s.Drafts = map[string]string{}
	}
	s.DraftSource, s.Draft, s.Open = p.ID, s.Drafts[p.ID], true
}

var profileReadingMarkdown = reading.UnwrapExportEnvelope
