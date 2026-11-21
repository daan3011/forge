package plane

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrNoV2 means the instance doesn't serve the v2 API forge targets.
var ErrNoV2 = errors.New("plane: this instance does not serve /api/v2 (forge needs Plane's v2 API; check plane.base_url)")

// FieldError is one entry of a 400 problem's errors list.
type FieldError struct {
	Field  string `json:"field"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// APIError is a non-2xx response. v2 sends RFC 9457 problem+json; other
// bodies end up in Detail.
type APIError struct {
	Status int          `json:"-"`
	Method string       `json:"-"`
	Path   string       `json:"-"`
	Type   string       `json:"type"`
	Code   string       `json:"code"`
	Title  string       `json:"title"`
	Detail string       `json:"detail"`
	Errors []FieldError `json:"errors"`
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "plane: %s /%s: %d", e.Method, e.Path, e.Status)
	switch {
	case e.Status == http.StatusUnauthorized:
		b.WriteString(" unauthorized (check the Plane API key)")
	case e.Detail != "":
		b.WriteString(" " + e.Detail)
	case e.Title != "":
		b.WriteString(" " + e.Title)
	case e.Code != "":
		b.WriteString(" " + e.Code)
	}
	for _, f := range e.Errors {
		fmt.Fprintf(&b, "; %s: %s", f.Field, f.Detail)
	}
	return b.String()
}

func parseAPIError(resp *http.Response, method, path string) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	e := &APIError{Status: resp.StatusCode, Method: method, Path: path}
	if json.Unmarshal(raw, e) != nil || (e.Code == "" && e.Detail == "" && e.Title == "") {
		// Not problem+json: v1-style {"error": "..."} or plain text.
		var legacy struct {
			Error  string `json:"error"`
			Detail string `json:"detail"`
		}
		if json.Unmarshal(raw, &legacy) == nil && (legacy.Error != "" || legacy.Detail != "") {
			e.Detail = legacy.Error + legacy.Detail
		} else if s := strings.TrimSpace(string(raw)); s != "" && len(s) < 300 && !strings.HasPrefix(s, "<") {
			e.Detail = s
		}
	}
	if resp.StatusCode == http.StatusTooManyRequests && e.Code == "" {
		e.Code = "rate_limited"
	}
	return e
}

func statusIs(err error, status int) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == status
}

// IsNotFound reports a 404.
func IsNotFound(err error) bool { return statusIs(err, http.StatusNotFound) }

// IsAuthError reports a 401 (missing or bad API key) or 403.
func IsAuthError(err error) bool {
	return statusIs(err, http.StatusUnauthorized) || statusIs(err, http.StatusForbidden)
}

// IsRateLimited reports a 429 that outlasted the retries.
func IsRateLimited(err error) bool { return statusIs(err, http.StatusTooManyRequests) }
