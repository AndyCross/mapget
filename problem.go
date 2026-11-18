package mapget

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Problem is an RFC 9457 problem details object, the error body ASP.NET Core
// calls ProblemDetails. It is an error, so a handler returns one to choose the
// status and body of a failure, and it is an http.Handler, so it can be
// registered on a mux directly.
type Problem struct {
	Type     string // a URI naming the kind of problem; "about:blank" when empty
	Title    string // short and the same for every occurrence; the status text when empty
	Status   int
	Detail   string // specific to this occurrence
	Instance string
	// Errors holds messages by field name, in the shape of ASP.NET Core's
	// ValidationProblemDetails.
	Errors map[string][]string
	// Extensions are written as extra top-level members.
	Extensions map[string]any
}

func (p *Problem) Error() string {
	s := fmt.Sprintf("%d %s", p.Status, p.title())
	if p.Detail != "" {
		s += ": " + p.Detail
	}
	return s
}

func (p *Problem) title() string {
	if p.Title != "" {
		return p.Title
	}
	return http.StatusText(p.Status)
}

// MarshalJSON writes the standard members, then Errors, then the extensions.
func (p *Problem) MarshalJSON() ([]byte, error) {
	m := make(map[string]any, len(p.Extensions)+6)
	for k, v := range p.Extensions {
		m[k] = v
	}
	if p.Type != "" {
		m["type"] = p.Type
	}
	m["title"] = p.title()
	m["status"] = p.Status
	if p.Detail != "" {
		m["detail"] = p.Detail
	}
	if p.Instance != "" {
		m["instance"] = p.Instance
	}
	if len(p.Errors) > 0 {
		m["errors"] = p.Errors
	}
	return json.Marshal(m)
}

// ServeHTTP writes the problem as application/problem+json.
func (p *Problem) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	status := p.Status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	body, err := json.Marshal(p)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"title":"Internal Server Error","status":500}`)
	}
	h := w.Header()
	h.Set("Content-Type", "application/problem+json")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	w.Write(body)
}

// Status returns a Problem with the given status and detail.
func Status(status int, detail string) *Problem {
	return &Problem{Status: status, Detail: detail}
}

// BadRequest returns a 400 Problem.
func BadRequest(detail string) *Problem { return Status(http.StatusBadRequest, detail) }

// Unauthorized returns a 401 Problem.
func Unauthorized(detail string) *Problem { return Status(http.StatusUnauthorized, detail) }

// Forbidden returns a 403 Problem.
func Forbidden(detail string) *Problem { return Status(http.StatusForbidden, detail) }

// NotFound returns a 404 Problem.
func NotFound(detail string) *Problem { return Status(http.StatusNotFound, detail) }

// Conflict returns a 409 Problem.
func Conflict(detail string) *Problem { return Status(http.StatusConflict, detail) }

// Unprocessable returns a 422 Problem.
func Unprocessable(detail string) *Problem { return Status(http.StatusUnprocessableEntity, detail) }

// FieldErrors is a set of validation messages by field name. Returned from a
// handler or from a request type's Validate method, it becomes a 400 Problem
// with the messages under "errors".
type FieldErrors map[string][]string

// Add records a message against a field.
func (e FieldErrors) Add(field, message string) { e[field] = append(e[field], message) }

func (e FieldErrors) Error() string {
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+" "+strings.Join(e[k], ", "))
	}
	return strings.Join(parts, "; ")
}

const validationTitle = "One or more validation errors occurred."

func (e FieldErrors) problem() *Problem {
	return &Problem{Status: http.StatusBadRequest, Title: validationTitle, Errors: e}
}
