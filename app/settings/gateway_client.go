package settings

import "github.com/zwhu/memlane/app/api"

func newAppAPIClient(control ProjectControlClient, identity string) (*api.Client, error) {
	return api.NewClient(api.Config{
		BaseURL: control.Config.URL, ManagementTokenFile: control.Config.ManagementTokenFile,
		ReadingIdentity: identity, HTTPClient: control.HTTP,
	})
}
