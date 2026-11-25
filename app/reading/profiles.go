package reading

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/zwhu/memlane/app/api"
)

type ProfilesTransport interface {
	ListUserProfiles(context.Context) (api.ListUserProfilesResponse, error)
	GetUserProfile(context.Context, string) (api.UserProfileView, error)
	StartUserProfileImport(context.Context, string, string) (api.ProfileImportResponse, error)
	SaveManualUserProfile(context.Context, string, string, string) (api.UserProfileView, error)
	CancelUserProfileImport(context.Context, string, string) (api.ProfileCancelResponse, error)
	DeleteUserProfile(context.Context, string) (api.ProfileDeleteResponse, error)
}

type ProfilesController struct {
	Client    *api.Client
	Transport ProfilesTransport
}

func (c ProfilesController) transport() ProfilesTransport {
	if c.Transport != nil {
		return c.Transport
	}
	return c.Client
}

func ProfileFromAPI(row api.UserProfileView) Profile {
	var p Profile
	_ = json.Unmarshal(row.Extra, &p)
	_ = json.Unmarshal(row.Progress, &p.Progress)
	p.ID, p.Kind, p.Name = row.InstanceID, row.Kind, row.Name
	if p.ID == "" {
		p.ID = row.Source
	}
	p.Status, p.Error, p.Operation = row.Status, row.ErrorCode, row.OperationID
	p.ContentVersion = row.ContentHash
	p.Method, p.CanGenerate, p.Updated = row.Method, row.CanGenerate, row.UpdatedAt
	if row.Document != nil {
		p.Document = &ProfileDocument{Dataset: row.Document.DatasetID, ID: row.Document.DataID}
	}
	return p
}

func (c ProfilesController) List(ctx context.Context) (ProfileDirectory, error) {
	out, err := c.transport().ListUserProfiles(ctx)
	list := ProfileDirectory{Prompt: out.Prompt, Profiles: make([]Profile, 0, len(out.Profiles))}
	for _, row := range out.Profiles {
		list.Profiles = append(list.Profiles, ProfileFromAPI(row))
	}
	return list, err
}

func (c ProfilesController) Get(ctx context.Context, source string) (Profile, string, error) {
	out, err := c.transport().GetUserProfile(ctx, source)
	return ProfileFromAPI(out), out.Body, err
}

var ErrProfileSaving = errors.New("画像正在保存，无法取消")

func (c ProfilesController) Act(ctx context.Context, profile Profile, action, operation, body string) error {
	transport := c.transport()
	switch action {
	case "generate":
		_, err := transport.StartUserProfileImport(ctx, profile.ID, operation)
		return err
	case "import":
		_, err := transport.SaveManualUserProfile(context.WithoutCancel(ctx), profile.ID, operation, body)
		return err
	case "cancel":
		if profile.Status == "saving" {
			return ErrProfileSaving
		}
		_, err := transport.CancelUserProfileImport(ctx, profile.ID, profile.Operation)
		return err
	case "delete":
		if profile.Status == "saving" {
			return ErrProfileSaving
		}
		_, err := transport.DeleteUserProfile(ctx, profile.ID)
		return err
	default:
		return errors.New("未知画像操作")
	}
}

func ProfileCopy(body string) (string, bool) { return CopyBody(body, body != "", false) }
