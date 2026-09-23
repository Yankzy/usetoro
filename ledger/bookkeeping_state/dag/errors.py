from __future__ import annotations


class AseTransportError(Exception):
    """Base error for all ASE transport and communication failures."""

    error_code: str = "ASE_TRANSPORT_ERROR"

    def __init__(self, message: str, error_code: str | None = None) -> None:
        super().__init__(message)
        if error_code is not None:
            self.error_code = error_code


class UnsupportedSchemaVersionError(AseTransportError):
    error_code = "UNSUPPORTED_SCHEMA_VERSION"


class InvalidRequestError(AseTransportError):
    error_code = "INVALID_REQUEST"


class UnknownDagError(AseTransportError):
    error_code = "UNKNOWN_DAG"


class DagConfigurationError(AseTransportError):
    error_code = "DAG_CONFIGURATION_ERROR"


class AseExecutionTimeoutError(AseTransportError):
    error_code = "ASE_EXECUTION_TIMEOUT"


class AseExecutionFailedError(AseTransportError):
    error_code = "ASE_EXECUTION_FAILED"


class MissingItemOutcomeError(AseTransportError):
    error_code = "MISSING_ITEM_OUTCOME"


class DuplicateItemOutcomeError(AseTransportError):
    error_code = "DUPLICATE_ITEM_OUTCOME"


class UnknownBookItemError(AseTransportError):
    error_code = "UNKNOWN_BOOK_ITEM"


class SubjectTypeMismatchError(AseTransportError):
    error_code = "SUBJECT_TYPE_MISMATCH"


class StaleStateRevisionError(AseTransportError):
    error_code = "STALE_STATE_REVISION"


class InvalidAccountCodeError(AseTransportError):
    error_code = "INVALID_ACCOUNT_CODE"


class UnauthorizedEvidenceReferenceError(AseTransportError):
    error_code = "UNAUTHORIZED_EVIDENCE_REFERENCE"


class ResponseCorrelationMismatchError(AseTransportError):
    error_code = "RESPONSE_CORRELATION_MISMATCH"


class MissingDagProviderError(AseTransportError):
    error_code = "MISSING_DAG_PROVIDER"


class IdempotencyPayloadMismatchError(AseTransportError):
    error_code = "IDEMPOTENCY_KEY_PAYLOAD_MISMATCH"


class IdempotencyBackendUnavailableError(AseTransportError):
    error_code = "IDEMPOTENCY_BACKEND_UNAVAILABLE"
