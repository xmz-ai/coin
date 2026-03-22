package coin

import "fmt"

// APIError represents a non-success response from server.
type APIError struct {
	HTTPStatus int
	Code       string
	Message    string
	RequestID  string
	RawBody    []byte
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.RequestID == "" {
		return fmt.Sprintf("api error: http=%d code=%s message=%s", e.HTTPStatus, e.Code, e.Message)
	}
	return fmt.Sprintf("api error: http=%d code=%s message=%s request_id=%s", e.HTTPStatus, e.Code, e.Message, e.RequestID)
}
