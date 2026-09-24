package service

import "errors"

var (
	ErrInvalidTransition = errors.New("requested status transition is not allowed")
	ErrInvalidInput      = errors.New("business input validation failed")
	ErrUnauthorized      = errors.New("invalid username or password")
	ErrInactiveUser      = errors.New("user account is inactive")
	ErrForbidden         = errors.New("role is not permitted for this operation")
	ErrLocked            = errors.New("resolved record is immutable")
	// ErrReleaseConflict means the batch ledger moved while the reviewer was
	// submitting; the request is rejected without changing the cumulative
	// quantity.
	ErrReleaseConflict = errors.New("同一批次已有放行先一步提交，请刷新后重试")
)
