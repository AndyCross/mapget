package mapget

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
)

// Path returns the address of the route registered for in's type, with in's
// path fields in the path and its query fields in the query string. It is
// the inverse of binding: a request for the result binds to a value equal to
// in, for the path and query fields.
//
// A query field is left out when it holds its default, or its zero value if
// it has no default. Header, cookie and body fields play no part.
//
// It returns an error if the type has no route on this router or has more
// than one, or if a path field holds a value no request path could carry: an
// empty string or a lone "/", or for a {name...} wildcard a value with an
// empty segment.
func (r *Router) Path(in any) (string, error) {
	v := reflect.ValueOf(in)
	for v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return "", fmt.Errorf("mapget: Path needs a request struct, not %T", in)
	}
	rt := r.root
	rt.mu.RLock()
	e, ok := rt.byType[v.Type()]
	rt.mu.RUnlock()
	switch {
	case !ok:
		return "", fmt.Errorf("mapget: no route is registered for %s", v.Type())
	case e == nil:
		return "", fmt.Errorf("mapget: %s is registered at more than one route", v.Type())
	}
	return e.link(v)
}

// MustPath is Path for values known to be good, such as an id just read from
// a database. It panics where Path returns an error.
func (r *Router) MustPath(in any) string {
	s, err := r.Path(in)
	if err != nil {
		panic(err)
	}
	return s
}

func (e *entry) link(v reflect.Value) (string, error) {
	var query url.Values
	for i := range e.plan.fields {
		f := &e.plan.fields[i]
		switch f.src {
		case fromQuery:
			fv := v.Field(f.index)
			var vals []string
			switch {
			case f.slice:
				for j := 0; j < fv.Len(); j++ {
					vals = append(vals, f.codec.format(fv.Index(j)))
				}
			case f.ptr:
				if !fv.IsNil() {
					vals = []string{f.codec.format(fv.Elem())}
				}
			case f.hasDef:
				if s := f.codec.format(fv); s != f.def {
					vals = []string{s}
				}
			case f.required || !fv.IsZero():
				vals = []string{f.codec.format(fv)}
			}
			if len(vals) > 0 {
				if query == nil {
					query = url.Values{}
				}
				query[f.name] = vals
			}
		}
	}
	var b strings.Builder
	for _, s := range e.pat.segs {
		b.WriteByte('/')
		switch {
		case s.wild:
			f := e.plan.pathField(s.name)
			val := f.codec.format(v.Field(f.index))
			if !s.multi {
				if val == "" {
					return "", fmt.Errorf("mapget: %s.%s is empty, and a path segment cannot be", e.plan.typ, f.goName)
				}
				if val == "/" {
					// ServeMux marks a trailing slash internally with the segment
					// "/", so a segment that unescapes to exactly "/" matches no
					// {wildcard} and "%2F" on its own is answered with 404.
					return "", fmt.Errorf(`mapget: %s.%s is "/", which ServeMux cannot match as a segment`, e.plan.typ, f.goName)
				}
				b.WriteString(escapeSegment(val))
				continue
			}
			parts := strings.Split(val, "/")
			for i, part := range parts {
				if part == "" && i < len(parts)-1 {
					return "", fmt.Errorf("mapget: %s.%s has an empty segment in %q", e.plan.typ, f.goName, val)
				}
				if i > 0 {
					b.WriteByte('/')
				}
				b.WriteString(escapeSegment(part))
			}
		case s.end, s.multi:
			// "/orders/{$}" and "/orders/" are both written "/orders/".
		default:
			b.WriteString(s.lit)
		}
	}
	if len(query) > 0 {
		b.WriteByte('?')
		b.WriteString(query.Encode())
	}
	return b.String(), nil
}

// escapeSegment escapes one path segment. The dot segments are escaped too,
// because ServeMux redirects a path that contains "." or ".." and leaves
// "%2E" alone.
func escapeSegment(s string) string {
	switch s {
	case ".":
		return "%2E"
	case "..":
		return "%2E%2E"
	}
	return url.PathEscape(s)
}

func (p *plan) pathField(name string) *field {
	for i := range p.fields {
		if f := &p.fields[i]; f.src == fromPath && f.name == name {
			return f
		}
	}
	return nil // checkAgainst has made sure this cannot happen
}
