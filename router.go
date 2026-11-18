package mapget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
)

// Mux is the part of *http.ServeMux that mapget uses.
type Mux interface {
	Handle(pattern string, handler http.Handler)
}

// Middleware wraps a handler, in the usual net/http shape.
type Middleware = func(http.Handler) http.Handler

// A Router registers handlers on a Mux and remembers what it registered. The
// Mux still does all the routing. A Router made by Group shares its parent's
// route table, so links and the OpenAPI document cover the whole tree.
type Router struct {
	mux    Mux
	prefix []segment
	mw     []Middleware
	root   *root
}

type root struct {
	mu      sync.RWMutex
	entries []*entry
	byType  map[reflect.Type]*entry // nil entry: the type has more than one route
	maxBody int64
	onError func(*http.Request, error)
}

type entry struct {
	pat  pattern
	plan *plan        // nil for a plain http.Handler
	out  reflect.Type // nil for a plain http.Handler
}

// An Option configures a Router made by New.
type Option func(*root)

// MaxBody sets the largest JSON request body a typed handler will read. The
// default is 1 MiB. A larger body is answered with 413.
func MaxBody(n int64) Option { return func(rt *root) { rt.maxBody = n } }

// OnError sets what happens to an error that is not a Problem or FieldErrors
// before the client is sent a 500 with no detail. The default logs it with
// slog.
func OnError(f func(*http.Request, error)) Option { return func(rt *root) { rt.onError = f } }

// New returns a Router that registers on mux, which is usually an
// *http.ServeMux.
func New(mux Mux, opts ...Option) *Router {
	rt := &root{
		byType:  map[reflect.Type]*entry{},
		maxBody: 1 << 20,
		onError: func(r *http.Request, err error) {
			slog.ErrorContext(r.Context(), "mapget: handler failed",
				"method", r.Method, "path", r.URL.Path, "err", err)
		},
	}
	for _, o := range opts {
		o(rt)
	}
	return &Router{mux: mux, root: rt}
}

// Group returns a Router whose routes all begin with prefix and run inside
// the given middleware, after any the receiver already has. The prefix may
// hold wildcards, such as "/tenants/{tenant}". It is ASP.NET Core's MapGroup.
func (r *Router) Group(prefix string, mw ...Middleware) *Router {
	g := &Router{mux: r.mux, root: r.root}
	g.prefix = append(g.prefix, r.prefix...)
	if prefix != "" {
		segs, err := parsePath(prefix)
		if err != nil {
			panic("mapget: group prefix: " + err.Error())
		}
		if last := segs[len(segs)-1]; last.multi || last.end {
			panic(fmt.Sprintf("mapget: group prefix %q must not end with a slash, {$} or a ... wildcard", prefix))
		}
		g.prefix = append(g.prefix, segs...)
	}
	g.mw = append(g.mw, r.mw...)
	g.mw = append(g.mw, mw...)
	return g
}

// Use adds middleware to every route registered through r from now on.
func (r *Router) Use(mw ...Middleware) { r.mw = append(r.mw, mw...) }

// Handle registers a plain handler, with the router's prefix and middleware
// applied, and lists it in the route table.
func (r *Router) Handle(pattern string, h http.Handler) {
	r.register(r.resolve(pattern), nil, nil, h)
}

// HandleFunc is Handle for a function.
func (r *Router) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	r.Handle(pattern, http.HandlerFunc(h))
}

func (r *Router) resolve(s string) pattern {
	pat, err := parsePattern(s)
	if err != nil {
		panic("mapget: " + err.Error())
	}
	seen := map[string]bool{}
	for _, seg := range r.prefix {
		seen[seg.name] = seg.wild
	}
	for _, seg := range pat.segs {
		if seg.wild && seen[seg.name] {
			panic(fmt.Sprintf("mapget: wildcard {%s} is in both the group prefix and the pattern %q", seg.name, s))
		}
	}
	pat.segs = append(append([]segment{}, r.prefix...), pat.segs...)
	return pat
}

func (r *Router) register(pat pattern, p *plan, out reflect.Type, h http.Handler) {
	for i := len(r.mw) - 1; i >= 0; i-- {
		h = r.mw[i](h)
	}
	// The mux goes first: it panics on a bad or conflicting pattern, and a
	// route it refused should not be in the table.
	r.mux.Handle(pat.String(), h)
	e := &entry{pat: pat, plan: p, out: out}
	rt := r.root
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.entries = append(rt.entries, e)
	if p != nil {
		if _, dup := rt.byType[p.typ]; dup {
			rt.byType[p.typ] = nil
		} else {
			rt.byType[p.typ] = e
		}
	}
}

// Handle registers h at the route written on its request type, the type's
// embedded Route field. It panics if In has no route, if the pattern's
// wildcards and the type's path fields differ, or if the mux refuses the
// pattern.
//
// Each request is bound to a new In: path values, query parameters, headers,
// cookies and a JSON body, by the field tags path, query, header, cookie and
// body. A path value that does not parse as its field's type is answered
// with 404, as a failed route constraint is in ASP.NET Core. Anything else
// that does not parse is answered with a 400 Problem that lists every
// failing field.
//
// The result is written as JSON with status 200, unless Out is Created,
// Accepted or NoContent, an empty struct (204), or an http.Handler, which is
// left to write the response itself. An error is written as a Problem.
func Handle[In, Out any](r *Router, h func(context.Context, In) (Out, error)) {
	p := planFor(reflect.TypeFor[In]())
	if p.route == "" {
		panic(fmt.Sprintf("mapget: %s has no route; embed mapget.Route with a route tag, or use HandleAt", p.typ))
	}
	handle(r, p, p.route, h)
}

// HandleAt is Handle with the pattern given at the call, in the style of
// ASP.NET Core's MapGet, for request types that carry no route of their own.
func HandleAt[In, Out any](r *Router, pattern string, h func(context.Context, In) (Out, error)) {
	p := planFor(reflect.TypeFor[In]())
	if p.route != "" {
		panic(fmt.Sprintf("mapget: %s already carries the route %q; use Handle", p.typ, p.route))
	}
	handle(r, p, pattern, h)
}

func handle[In, Out any](r *Router, p *plan, route string, h func(context.Context, In) (Out, error)) {
	pat := r.resolve(route)
	p.checkAgainst(pat)
	rt := r.root
	outType := reflect.TypeFor[Out]()
	empty := outType.Kind() == reflect.Struct && outType.NumField() == 0 && !outType.Implements(handlerType)
	r.register(pat, p, outType, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var in In
		if err := p.bind(w, req, reflect.ValueOf(&in).Elem(), rt.maxBody); err != nil {
			rt.fail(w, req, err)
			return
		}
		out, err := h(req.Context(), in)
		if err != nil {
			rt.fail(w, req, err)
			return
		}
		if empty {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		rt.write(w, req, out)
	}))
}

func (rt *root) fail(w http.ResponseWriter, r *http.Request, err error) {
	var p *Problem
	var fe FieldErrors
	switch {
	case errors.As(err, &p):
	case errors.As(err, &fe):
		p = fe.problem()
	default:
		rt.onError(r, err)
		p = Status(http.StatusInternalServerError, "")
	}
	p.ServeHTTP(w, r)
}

var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

func (rt *root) write(w http.ResponseWriter, r *http.Request, out any) {
	status := http.StatusOK
	body := out
	switch v := out.(type) {
	case nil:
		w.WriteHeader(http.StatusNoContent)
		return
	case typedResult:
		status = v.resultStatus()
		var loc string
		loc, body = v.resultValue()
		if loc != "" {
			w.Header().Set("Location", loc)
		}
		if v.resultBodyType() == nil {
			w.WriteHeader(status)
			return
		}
	case http.Handler:
		v.ServeHTTP(w, r)
		return
	}
	// Encode before writing anything, so a value that cannot be encoded is a
	// 500 and not half a response.
	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufPool.Put(buf)
	if err := json.NewEncoder(buf).Encode(body); err != nil {
		rt.fail(w, r, fmt.Errorf("encoding %T: %w", body, err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

// RouteInfo describes one registered route.
type RouteInfo struct {
	Method  string       // empty when the pattern matches every method
	Host    string       // empty when the pattern matches every host
	Path    string       // the path as net/http writes it, wildcards included
	Pattern string       // the whole pattern, as given to the mux
	In, Out reflect.Type // nil for a plain http.Handler
	Summary string
	Tags    []string
}

// Routes returns every route registered through r, its parents and its
// groups, ordered by path and then method.
func (r *Router) Routes() []RouteInfo {
	rt := r.root
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	infos := make([]RouteInfo, 0, len(rt.entries))
	for _, e := range rt.entries {
		info := RouteInfo{Method: e.pat.method, Host: e.pat.host, Path: e.pat.path(), Pattern: e.pat.String(), Out: e.out}
		if e.plan != nil {
			info.In, info.Summary, info.Tags = e.plan.typ, e.plan.summary, e.plan.tags
		}
		infos = append(infos, info)
	}
	sort.SliceStable(infos, func(i, j int) bool {
		if infos[i].Path != infos[j].Path {
			return infos[i].Path < infos[j].Path
		}
		return infos[i].Method < infos[j].Method
	})
	return infos
}

// WriteRoutes writes the route table as aligned text, one route a line.
func (r *Router) WriteRoutes(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	name := func(t reflect.Type) string {
		if t == nil {
			return "-"
		}
		return strings.ReplaceAll(t.String(), "github.com/", "")
	}
	for _, info := range r.Routes() {
		method := info.Method
		if method == "" {
			method = "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", method, info.Host+info.Path, name(info.In), name(info.Out))
	}
	return tw.Flush()
}
