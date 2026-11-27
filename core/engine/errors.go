package engine

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrModelDigestMismatch   = errors.New("model digest mismatch")
	ErrUnauthorized          = errors.New("unauthorized")
	ErrForbidden             = errors.New("forbidden")
	ErrNotFound              = errors.New("not found")
	ErrConflict              = errors.New("conflict")
	ErrUnavailable           = errors.New("engine unavailable")
	ErrInvalidArgument       = errors.New("invalid argument")
	ErrPathDenied            = errors.New("path denied")
	ErrContentTooLarge       = errors.New("content too large")
	ErrNotUTF8Text           = errors.New("content is not UTF-8 text")
	ErrUnknownEngine         = errors.New("unknown engine")
	ErrUnsupportedCapability = errors.New("unsupported engine capability")
	ErrDatasetNotProvisioned = errors.New("dataset not provisioned")
)

type EngineError struct {
	StatusCode int
	Code       string
	Message    string
	Err        error
}

func (e *EngineError) Error() string {
	var parts []string
	if e.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", e.StatusCode))
	}
	if e.Code != "" {
		parts = append(parts, e.Code)
	}
	if e.Message != "" {
		parts = append(parts, e.Message)
	} else if e.Err != nil {
		parts = append(parts, e.Err.Error())
	}
	if len(parts) == 0 {
		return "engine error"
	}
	return strings.Join(parts, ": ")
}

func (e *EngineError) Unwrap() error { return e.Err }
