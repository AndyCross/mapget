// Package mapget puts typed, self-describing endpoints on the standard
// library's http.ServeMux.
//
// Go 1.22 gave ServeMux methods and wildcards, which covers matching a
// request to a handler. mapget adds what ASP.NET Core does around its router
// and the mux leaves to you: binding the request to a typed value, answering
// a malformed one, generating links, grouping routes, listing them and
// describing them as OpenAPI. The mux still does all the routing, and the
// package has no dependencies outside the standard library.
//
// A request type carries its route, in the way a C# action carries
// [HttpGet("{id}")], and its fields say where each value comes from:
//
//	type GetOrder struct {
//		mapget.Route `route:"GET /orders/{id}"`
//		ID     int      `path:"id"`
//		Expand []string `query:"expand"`
//	}
//
//	func (s *Service) GetOrder(ctx context.Context, in GetOrder) (Order, error)
//
//	mux := http.NewServeMux()
//	app := mapget.New(mux)
//	mapget.Handle(app, svc.GetOrder)
//
// Handle registers "GET /orders/{id}" on the mux. A request for /orders/42
// calls GetOrder with ID 42 and writes the Order as JSON. A request for
// /orders/abc is answered with 404, because the field's type is the route
// constraint. A query value that does not parse is answered with a 400
// problem details body that names every failing field.
//
// The same type builds links, so an address is written in one place:
//
//	app.Path(GetOrder{ID: 42, Expand: []string{"lines"}}) // "/orders/42?expand=lines"
//
// # Tags
//
// Each exported field of a request type takes one of these tags. A field with
// none is left alone, except a field of type *http.Request, which is given
// the request.
//
//	path:"name"       a wildcard from the pattern; never a pointer or slice
//	query:"name"      a query parameter; a slice takes every value
//	header:"Name"     a request header
//	cookie:"name"     a cookie
//	body:"json"       the request body, decoded as JSON
//
// query, header and cookie take a ",required" option, and a separate
// default:"value" tag for when the value is absent. A pointer field is nil
// when the value is absent. Field types can be strings, booleans, integers,
// floats, time.Duration, or anything that implements
// encoding.TextUnmarshaler, such as time.Time or a UUID type.
//
// A mistake in the tags is a panic when the route is registered, in the way
// the mux itself panics on a conflicting pattern: a path tag that names no
// wildcard, a wildcard with no field, a default that does not parse.
//
// # Validation
//
// Rules go in tags named after the JSON Schema keywords, on parameter fields
// and on any field inside a JSON body:
//
//	minimum, maximum      numbers
//	minLength, maxLength  strings, counted in characters
//	pattern               strings, a regular expression
//	enum                  strings and integers, comma separated
//	minItems, maxItems    slices; the tags above apply to each element
//
// Every failure is reported in one 400, under the field's name or its path
// in the body, such as "lines[0].qty". On a path field a rule is a route
// constraint and a value outside it is a 404. An absent optional parameter,
// a nil pointer and an omitempty body field at its zero value are not
// checked. The same tags are written into the OpenAPI document.
//
// For a rule a tag cannot state, give the request type a Validate() error
// method. It runs after binding, once the tags have passed. Return
// FieldErrors for a 400 with messages by field, a *Problem for any other
// answer, or a plain error for a 400 with its text.
//
// # Results and errors
//
// A handler's result is written as JSON with status 200. Created, Accepted
// and NoContent choose other statuses, an empty struct answers 204, and a
// result that is an http.Handler writes the response itself. An error that
// is a *Problem or FieldErrors is written as RFC 9457 problem details; any
// other error is logged and answered with a 500 that says nothing about it.
package mapget
