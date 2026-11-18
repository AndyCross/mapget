package mapget

import (
	"net/http"
	"reflect"
)

// Created is a handler result that answers 201 with a Location header and
// Body as JSON.
type Created[T any] struct {
	Location string
	Body     T
}

// Accepted is a handler result that answers 202 with Body as JSON, and a
// Location header when Location is set.
type Accepted[T any] struct {
	Location string
	Body     T
}

// NoContent is a handler result that answers 204 with no body.
type NoContent struct{}

// typedResult is what Created, Accepted and NoContent have in common. The
// status and body type are known from the type alone, which is what lets the
// OpenAPI document describe them.
type typedResult interface {
	resultStatus() int
	resultBodyType() reflect.Type // nil when there is no body
	resultValue() (location string, body any)
}

func (c Created[T]) resultStatus() int             { return http.StatusCreated }
func (c Created[T]) resultBodyType() reflect.Type  { return reflect.TypeFor[T]() }
func (c Created[T]) resultValue() (string, any)    { return c.Location, c.Body }
func (a Accepted[T]) resultStatus() int            { return http.StatusAccepted }
func (a Accepted[T]) resultBodyType() reflect.Type { return reflect.TypeFor[T]() }
func (a Accepted[T]) resultValue() (string, any)   { return a.Location, a.Body }
func (NoContent) resultStatus() int                { return http.StatusNoContent }
func (NoContent) resultBodyType() reflect.Type     { return nil }
func (NoContent) resultValue() (string, any)       { return "", nil }

var (
	typedResultType = reflect.TypeFor[typedResult]()
	handlerType     = reflect.TypeFor[http.Handler]()
)

// responseOf says what a handler's result type answers with: the status, and
// the type of the JSON body or nil. ok is false when the type writes its own
// response (an http.Handler), so nothing is known ahead of time.
func responseOf(out reflect.Type) (status int, body reflect.Type, ok bool) {
	if out.Implements(typedResultType) && out.Kind() != reflect.Interface {
		tr := reflect.Zero(out).Interface().(typedResult)
		return tr.resultStatus(), tr.resultBodyType(), true
	}
	if out.Implements(handlerType) {
		return 0, nil, false
	}
	if out.Kind() == reflect.Struct && out.NumField() == 0 {
		return http.StatusNoContent, nil, true
	}
	return http.StatusOK, out, true
}
