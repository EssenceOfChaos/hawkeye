package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"

	"hawkeye/internal/errtrack"
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

// StatusCode lets Echo work out the status of a request that ended in this error, for the request log and the metrics.
func (e *APIError) StatusCode() int { return e.Status }

// errorHandler serializes every error into the common envelope. Internal errors are
// logged but never exposed to the client.
func errorHandler(log *slog.Logger, rep errtrack.Reporter) echo.HTTPErrorHandler {
	return func(c *echo.Context, err error) {
		if resp, _ := echo.UnwrapResponse(c.Response()); resp != nil && resp.Committed {
			return
		}
		status, body := http.StatusInternalServerError, ErrorDetail{Code: "INTERNAL_ERROR", Message: "An internal error occurred."}

		var apiErr *APIError
		var httpErr echo.HTTPStatusCoder // the router's own 404 and 405 are not *echo.HTTPError
		switch {
		case errors.As(err, &apiErr):
			status, body = apiErr.Status, ErrorDetail{Code: apiErr.Code, Message: apiErr.Message, Details: apiErr.Details}
		case errors.As(err, &httpErr):
			status = httpErr.StatusCode()
			body = ErrorDetail{Code: codeForStatus(status), Message: http.StatusText(status)}
		default:
			rid := c.Response().Header().Get(echo.HeaderXRequestID)
			var pe panicError
			if !errors.As(err, &pe) { // recovered panics were already logged and reported by recoverPanics
				log.ErrorContext(c.Request().Context(), "unhandled error", "error", err, "request_id", rid)
				rep.Capture(c.Request().Context(), err, map[string]string{"request_id": rid, "route": c.Path(), "method": c.Request().Method})
			}
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
