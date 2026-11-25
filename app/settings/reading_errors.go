package settings

import "strings"

func readingUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if status, ok := httpStatusCode(err); ok && (status == 401 || status == 403 || status == 404 || status == 413) {
		return true
	}
	return readingAuthDenied(err) || IsControlErrorCode(err, 503, "reading_contract_mismatch")
}

func readingAuthDenied(err error) bool {
	if err == nil {
		return false
	}
	if IsControlAuthDenied(err) {
		return true
	}
	if status, ok := httpStatusCode(err); ok && (status == 401 || status == 403) {
		return true
	}
	s := err.Error()
	for _, reason := range []string{"identity changed", "dataset changed", "credential unavailable"} {
		if strings.Contains(s, reason) {
			return true
		}
	}
	return false
}
