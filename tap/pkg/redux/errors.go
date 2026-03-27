package redux

import "errors"

// Domain Errors explicitly partition structural, security, or validation exceptions statically 
// preventing loosely typed parsing during context reductions.
var (
	ErrMaxOperationsExceeded = errors.New("patch operations limit exceeded")
	ErrPayloadTooLarge       = errors.New("patch array payload exceeds maximum operational byte limits")
	ErrRootReplace           = errors.New("attempted to replace or remove the structural root document perfectly")
	ErrSequenceMismatch      = errors.New("event sequence drift detected breaking idempotent NATS order")
	ErrArrayBan              = errors.New("no-array rule violation")
	ErrSchemaValidation      = errors.New("schema validation failed")
	ErrInvalidPatchStruct    = errors.New("malformed JSON patch operation structure")
	ErrRBACViolation         = errors.New("actor is not authorized to mutate logical json path")
)
