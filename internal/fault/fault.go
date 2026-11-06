package fault

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"net"
	"strings"

	"google.golang.org/api/googleapi"
)

// Error is the public, machine-readable error contract. Exit is process-only.
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Details   any    `json:"details,omitempty"`
	Exit      int    `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func New(code, message string, exit int) *Error {
	return &Error{Code: code, Message: message, Exit: exit}
}
func Input(format string, args ...any) *Error {
	return New("INVALID_ARGUMENT", fmt.Sprintf(format, args...), 2)
}
func Conflict(format string, args ...any) *Error {
	return New("CONFLICT", fmt.Sprintf(format, args...), 4)
}

func From(err error) *Error {
	var own *Error
	if errors.As(err, &own) {
		return own
	}
	var retrieve *oauth2.RetrieveError
	var auth *google.AuthenticationError
	if errors.As(err, &retrieve) || errors.As(err, &auth) {
		return New("AUTH_PERMISSION", "Google credentials could not obtain an access token; check credentials and scopes", 3)
	}
	var api *googleapi.Error
	if errors.As(err, &api) {
		// Never expose response bodies, headers or URLs that may contain secrets.
		message := Sanitize(api.Message)
		if message == "" {
			message = "Google API request failed"
		}
		switch {
		case api.Code == 401 || api.Code == 403:
			return New("AUTH_PERMISSION", "Google rejected the credentials or permissions; run doctor and check API enablement and Play Console access", 3)
		case api.Code == 409 || api.Code == 412 || strings.Contains(strings.ToLower(message), "editnotfound") || strings.Contains(strings.ToLower(message), "already in review") || strings.Contains(strings.ToLower(message), "already been used"):
			return New("CONFLICT", message, 4)
		case api.Code == 404 || api.Code == 400:
			return New("GOOGLE_REJECTED", message, 2)
		case api.Code == 429 || api.Code >= 500:
			return &Error{Code: "SERVICE_UNAVAILABLE", Message: "Google API is temporarily unavailable or rate limited", Retryable: true, Exit: 5}
		}
	}
	var neterr net.Error
	if errors.As(err, &neterr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &Error{Code: "NETWORK_ERROR", Message: "Request interrupted or network unavailable", Retryable: true, Exit: 5}
	}
	return New("INTERNAL_ERROR", "Operation failed; check local files and configuration", 1)
}
