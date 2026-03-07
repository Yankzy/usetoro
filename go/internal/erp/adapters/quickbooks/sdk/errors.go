package quickbooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// Failure is the outermost struct that holds an error response.
type Failure struct {
	Fault struct {
		Error []struct {
			Message string
			Detail  string
			Code    string `json:"code"`
			Element string `json:"element"`
		}
		Type string `json:"type"`
	}
	Time Date `json:"time"`
}

// Error implements the error interface.
func (f Failure) Error() string {
	text, err := json.Marshal(f)
	if err != nil {
		return fmt.Sprintf("unexpected error while marshalling error: %v", err)
	}

	return string(text)
}

// ObjectNotFoundError is returned when QBO returns an Object Not Found error (Code 610).
type ObjectNotFoundError struct {
	Failure Failure
}

func (e ObjectNotFoundError) Error() string {
	return "Object Not Found: " + e.Failure.Error()
}

// IsQBOApplicationError returns true if the error is a logical application error (like 400 Bad Request)
// rather than a system or network error. This is useful for circuit breakers.
func IsQBOApplicationError(err error) bool {
	var f Failure
	if errors.As(err, &f) {
		return true
	}
	var o ObjectNotFoundError
	if errors.As(err, &o) {
		return true
	}
	return false
}

// check if failure is an object not found error
func isObjectNotFound(f Failure) bool {
	for _, e := range f.Fault.Error {
		if e.Code == "610" || e.Message == "Object Not Found" {
			return true
		}
	}
	return false
}

// parseFailure takes a response reader and tries to parse a Failure.
func parseFailure(resp *http.Response) error {
	msg, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.New("When reading response body:" + err.Error())
	}

	var errStruct Failure

	if err = json.Unmarshal(msg, &errStruct); err != nil {
		return errors.New(strconv.Itoa(resp.StatusCode) + " " + string(msg))
	}

	if isObjectNotFound(errStruct) {
		return ObjectNotFoundError{Failure: errStruct}
	}

	return errStruct
}
