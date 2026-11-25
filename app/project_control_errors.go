package app

import (
	"net/http"

	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/settings"
)

type ControlResponseError = settings.ControlResponseError

func isControlIndexRebuilding(err error) bool {
	return settings.IsControlErrorCode(err, http.StatusServiceUnavailable, "catalog_index_rebuilding")
}

func isControlInvalidCursor(err error) bool {
	return reading.InvalidCursor(err) || settings.IsControlErrorCode(err, http.StatusBadRequest, "invalid_cursor")
}

func isControlFailClosedV2(err error) bool {
	if settings.IsControlAuthDenied(err) {
		return true
	}
	return isControlIndexRebuilding(err)
}
