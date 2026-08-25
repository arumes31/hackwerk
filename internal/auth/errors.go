package auth

import "errors"

var (
	ErrForbidden          = errors.New("auth: forbidden")
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrInvalidSession     = errors.New("auth: invalid session")
	ErrRateLimited        = errors.New("auth: rate limited")
	ErrWeakPassword       = errors.New("auth: password does not satisfy policy")
	ErrConflict           = errors.New("auth: concurrent change")
	ErrLastAdmin          = errors.New("auth: last active administrator is protected")
	ErrNotFound           = errors.New("auth: not found")
)
