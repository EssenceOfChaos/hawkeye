package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"
)

// ErrorBody is the common API error envelope.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// APIError is returned by handlers to produce a specific error code.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// errorHandler serialises every error into the common envelope. Internal errors are
// logged but never exposed to the client.
func errorHandler(log *slog.Logger) echo.HTTPErrorHandler {
	return func(err error, c echo.Context) {
		if c.Response().Committed {
			return
		}
		status, body := http.StatusInternalServerError, ErrorDetail{Code: "INTERNAL_ERROR", Message: "An internal error occurred."}

		var apiErr *APIError
		var httpErr *echo.HTTPError
		switch {
		case errors.As(err, &apiErr):
			status, body = apiErr.Status, ErrorDetail{Code: apiErr.Code, Message: apiErr.Message, Details: apiErr.Details}
		case errors.As(err, &httpErr):
			status = httpErr.Code
			body = ErrorDetail{Code: codeForStatus(status), Message: http.StatusText(status)}
		default:
			log.ErrorContext(c.Request().Context(), "unhandled error", "error", err,
				"request_id", c.Response().Header().Get(echo.HeaderXRequestID))
		}

		if c.Request().Method == http.MethodHead {
			_ = c.NoContent(status)
			return
		}
		if werr := c.JSON(status, ErrorBody{Error: body}); werr != nil {
			log.Error("write error response", "error", werr)
		}
	}
}

func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "BAD_REQUEST"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "FORBIDDEN"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusMethodNotAllowed:
		return "METHOD_NOT_ALLOWED"
	case http.StatusRequestEntityTooLarge:
		return "PAYLOAD_TOO_LARGE"
	case http.StatusTooManyRequests:
		return "RATE_LIMITED"
	case http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return "UNAVAILABLE"
	default:
		if status >= 500 {
			return "INTERNAL_ERROR"
		}
		return "REQUEST_ERROR"
	}
}
