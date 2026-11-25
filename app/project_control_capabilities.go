package app

import (
	"context"

	"github.com/zwhu/memlane/app/settings"
)

type ControlCapabilities = settings.ControlCapabilities

const readingContract = settings.ReadingContract

func (c ProjectControlClient) requireReadingContract(ctx context.Context) error {
	return settings.ProjectControlClient(c).RequireReadingContract(ctx)
}

func (c ProjectControlClient) probeFeature(ctx context.Context, feature string) (bool, error) {
	return settings.ProjectControlClient(c).ProbeFeature(ctx, feature)
}
