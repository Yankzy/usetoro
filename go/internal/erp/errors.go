package erp

import "errors"

var (
	// ErrNotFound indicates that the requested object was not found in the ERP system.
	// This usually means the Toro shadow database is out of sync and needs a CDC refresh.
	ErrNotFound = errors.New("object not found in ERP")

	// ErrRateLimited indicates that the ERP API rate limits have been exceeded.
	ErrRateLimited = errors.New("ERP API rate limit exceeded")

	// ErrAuthFailed indicates that the OAuth tokens or authentication mechanism failed.
	// The user may need to reconnect their ERP account.
	ErrAuthFailed = errors.New("ERP authentication failed")

	// ErrBadRequest indicates a validation error with the data sent to the ERP.
	ErrBadRequest = errors.New("bad request to ERP API")
)
