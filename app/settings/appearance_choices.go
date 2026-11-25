package settings

import "strings"

const (
	appearanceSystem = "system"
	appearanceLight  = "light"
	appearanceDark   = "dark"
)

var appearanceModes = []string{appearanceSystem, appearanceLight, appearanceDark}

func normalizeAppearanceMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case appearanceLight:
		return appearanceLight
	case appearanceDark:
		return appearanceDark
	default:
		return appearanceSystem
	}
}

func appearanceModeIndex(mode string) int {
	switch normalizeAppearanceMode(mode) {
	case appearanceLight:
		return 1
	case appearanceDark:
		return 2
	default:
		return 0
	}
}
