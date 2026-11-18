package mapget

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Route marks a request type with the route it answers. Embed it and give it
// a route tag holding a net/http pattern:
//
//	type GetOrder struct {
//		mapget.Route `route:"GET /orders/{id}" summary:"Fetch one order" tags:"orders"`
//		ID int `path:"id"`
//	}
//
// The summary and tags are optional and go into the OpenAPI document.
type Route struct{}

type source int

const (
	fromPath source = iota
	fromQuery
	fromHeader
	fromCookie
	fromBody
	fromRequest
)

func (s source) String() string {
	return [...]string{"path", "query", "header", "cookie", "body", "request"}[s]
}

// A codec reads and writes one scalar type as text.
type codec struct {
	parse  func(s string, v reflect.Value) error
	format func(v reflect.Value) string
	want   string // completes "must be ..."
}

// A field is one bound field of a request type.
type field struct {
	index    int
	goName   string
	name     string // the name on the wire
	src      source
	typ      reflect.Type // the scalar type, with pointer and slice removed
	ptr      bool
	slice    bool
	required bool
	def      string
	hasDef   bool
	doc      string
	codec    codec
	rules    *rules    // validation tags, or nil
	body     *bodyType // for a body field: the walk that checks it, or nil
}

// A plan is everything known about a request type before the first request.
type plan struct {
	typ      reflect.Type
	route    string
	summary  string
	tags     []string
	fields   []field
	hasQuery bool
	validate bool
}

var (
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
	textMarshalerType   = reflect.TypeFor[encoding.TextMarshaler]()
	timeType            = reflect.TypeFor[time.Time]()
	durationType        = reflect.TypeFor[time.Duration]()
	requestType         = reflect.TypeFor[*http.Request]()
	routeType           = reflect.TypeFor[Route]()
	validatorType       = reflect.TypeFor[validator]()
)

type validator interface{ Validate() error }

func codecFor(t reflect.Type) (codec, bool) {
	switch {
	case t == durationType:
		return codec{
			parse: func(s string, v reflect.Value) error {
				d, err := time.ParseDuration(s)
				v.SetInt(int64(d))
				return err
			},
			format: func(v reflect.Value) string { return time.Duration(v.Int()).String() },
			want:   "a duration such as 90s or 1h30m",
		}, true
	case reflect.PointerTo(t).Implements(textUnmarshalerType):
		c := codec{
			parse: func(s string, v reflect.Value) error {
				return v.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(s))
			},
			format: func(v reflect.Value) string { return fmt.Sprint(v.Interface()) },
			want:   "a valid " + t.Name(),
		}
		if t == timeType {
			c.want = "an RFC 3339 time"
		}
		if t.Implements(textMarshalerType) {
			c.format = func(v reflect.Value) string {
				b, err := v.Interface().(encoding.TextMarshaler).MarshalText()
				if err != nil {
					return ""
				}
				return string(b)
			}
		}
		return c, true
	}
	switch t.Kind() {
	case reflect.String:
		return codec{
			parse:  func(s string, v reflect.Value) error { v.SetString(s); return nil },
			format: func(v reflect.Value) string { return v.String() },
			want:   "a string",
		}, true
	case reflect.Bool:
		return codec{
			parse: func(s string, v reflect.Value) error {
				b, err := strconv.ParseBool(s)
				v.SetBool(b)
				return err
			},
			format: func(v reflect.Value) string { return strconv.FormatBool(v.Bool()) },
			want:   "true or false",
		}, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		bits := t.Bits()
		return codec{
			parse: func(s string, v reflect.Value) error {
				n, err := strconv.ParseInt(s, 10, bits)
				v.SetInt(n)
				return err
			},
			format: func(v reflect.Value) string { return strconv.FormatInt(v.Int(), 10) },
			want:   "an integer",
		}, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		bits := t.Bits()
		return codec{
			parse: func(s string, v reflect.Value) error {
				n, err := strconv.ParseUint(s, 10, bits)
				v.SetUint(n)
				return err
			},
			format: func(v reflect.Value) string { return strconv.FormatUint(v.Uint(), 10) },
			want:   "a whole number, zero or more",
		}, true
	case reflect.Float32, reflect.Float64:
		bits := t.Bits()
		return codec{
			parse: func(s string, v reflect.Value) error {
				f, err := strconv.ParseFloat(s, bits)
				v.SetFloat(f)
				return err
			},
			format: func(v reflect.Value) string { return strconv.FormatFloat(v.Float(), 'g', -1, bits) },
			want:   "a number",
		}, true
	}
	return codec{}, false
}

// planFor reads a request type's tags. It panics on anything a compiler would
// have rejected had the tags been code: this runs at registration, so a
// mistake stops the program at startup rather than on the first request.
func planFor(t reflect.Type) *plan {
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("mapget: request type %s must be a struct", t))
	}
	p := &plan{typ: t}
	p.validate = t.Implements(validatorType) || reflect.PointerTo(t).Implements(validatorType)
	hasBody := false
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.Type == routeType {
			p.route = sf.Tag.Get("route")
			p.summary = sf.Tag.Get("summary")
			for _, tag := range strings.Split(sf.Tag.Get("tags"), ",") {
				if tag = strings.TrimSpace(tag); tag != "" {
					p.tags = append(p.tags, tag)
				}
			}
			if p.route == "" {
				panic(fmt.Sprintf("mapget: %s embeds Route without a route tag", t))
			}
			continue
		}
		if !sf.IsExported() {
			continue
		}
		f := field{index: i, goName: sf.Name, typ: sf.Type, doc: sf.Tag.Get("doc")}
		if sf.Type == requestType {
			f.src = fromRequest
			p.fields = append(p.fields, f)
			continue
		}
		found := 0
		for _, src := range []source{fromPath, fromQuery, fromHeader, fromCookie, fromBody} {
			tag, ok := sf.Tag.Lookup(src.String())
			if !ok {
				continue
			}
			found++
			name, opts, _ := strings.Cut(tag, ",")
			f.src, f.name = src, name
			for _, opt := range strings.Split(opts, ",") {
				switch opt {
				case "":
				case "required":
					f.required = true
				default:
					panic(fmt.Sprintf("mapget: %s.%s: unknown option %q in %s tag", t, sf.Name, opt, src))
				}
			}
		}
		if found == 0 {
			continue
		}
		where := fmt.Sprintf("mapget: %s.%s", t, sf.Name)
		if found > 1 {
			panic(where + ": more than one of path, query, header, cookie and body")
		}
		if f.src == fromBody {
			if f.name != "json" {
				panic(fmt.Sprintf(`%s: body:"%s" is not supported, use body:"json"`, where, f.name))
			}
			if hasBody {
				panic(fmt.Sprintf("mapget: %s has two body fields", t))
			}
			hasBody = true
			if f.typ.Kind() == reflect.Pointer {
				f.ptr, f.typ = true, f.typ.Elem()
			}
			bt, err := bodyTypeFor(f.typ)
			if err != nil {
				panic("mapget: " + err.Error())
			}
			f.body = bt
			p.fields = append(p.fields, f)
			continue
		}
		if f.name == "" {
			panic(where + ": empty name in " + f.src.String() + " tag")
		}
		switch {
		case f.typ.Kind() == reflect.Pointer:
			f.ptr, f.typ = true, f.typ.Elem()
		case f.typ.Kind() == reflect.Slice && !reflect.PointerTo(f.typ).Implements(textUnmarshalerType):
			f.slice, f.typ = true, f.typ.Elem()
		}
		if f.src == fromPath && (f.ptr || f.slice) {
			panic(where + ": a path value is always present and single, so the field cannot be a pointer or a slice")
		}
		if f.src == fromCookie && f.slice {
			panic(where + ": a cookie field cannot be a slice")
		}
		c, ok := codecFor(f.typ)
		if !ok {
			panic(fmt.Sprintf("%s: cannot read a %s from text; implement encoding.TextUnmarshaler", where, f.typ))
		}
		f.codec = c
		r, err := parseRules(sf.Tag, f.typ, f.slice)
		if err != nil {
			panic(where + ": " + err.Error())
		}
		f.rules = r
		if def, ok := sf.Tag.Lookup("default"); ok {
			if f.src == fromPath || f.required {
				panic(where + ": a default is never used on a path or required field")
			}
			dv := reflect.New(f.typ).Elem()
			if err := c.parse(def, dv); err != nil {
				panic(fmt.Sprintf("%s: default %q is not %s", where, def, c.want))
			}
			if r != nil {
				if msg := r.check(dv); msg != "" {
					panic(fmt.Sprintf("%s: default %q %s", where, def, msg))
				}
			}
			f.def, f.hasDef = def, true
		}
		if f.src == fromQuery {
			p.hasQuery = true
		}
		p.fields = append(p.fields, f)
	}
	return p
}

// checkAgainst makes sure the pattern's wildcards and the type's path fields
// are the same set.
func (p *plan) checkAgainst(pat pattern) {
	want := map[string]segment{}
	for _, w := range pat.wildcards() {
		want[w.name] = w
	}
	for _, f := range p.fields {
		if f.src != fromPath {
			continue
		}
		if _, ok := want[f.name]; !ok {
			panic(fmt.Sprintf("mapget: %s.%s reads path value %q, which is not in the pattern %q",
				p.typ, f.goName, f.name, pat))
		}
		delete(want, f.name)
	}
	for name := range want {
		panic(fmt.Sprintf(`mapget: pattern %q has the wildcard {%s} and %s has no field tagged path:"%s"`,
			pat, name, p.typ, name))
	}
}

var errNoMatch = NotFound("")

// bind fills v, a settable value of the plan's type, from the request.
func (p *plan) bind(w http.ResponseWriter, r *http.Request, v reflect.Value, maxBody int64) error {
	var query url.Values
	if p.hasQuery {
		query = r.URL.Query()
	}
	var errs FieldErrors
	fail := func(name, msg string) {
		if errs == nil {
			errs = FieldErrors{}
		}
		errs.Add(name, msg)
	}
	for i := range p.fields {
		f := &p.fields[i]
		fv := v.Field(f.index)
		var vals []string
		switch f.src {
		case fromRequest:
			fv.Set(reflect.ValueOf(r))
			continue
		case fromBody:
			if err := f.bindBody(w, r, fv, maxBody, fail); err != nil {
				return err
			}
			continue
		case fromPath:
			// The field's type is the route constraint. A value that does not
			// parse means the address names nothing, which is a 404.
			if err := f.codec.parse(r.PathValue(f.name), fv); err != nil {
				return errNoMatch
			}
			if f.rules != nil && f.rules.check(fv) != "" {
				return errNoMatch
			}
			continue
		case fromQuery:
			vals = query[f.name]
		case fromHeader:
			vals = r.Header.Values(f.name)
		case fromCookie:
			if c, err := r.Cookie(f.name); err == nil {
				vals = []string{c.Value}
			}
		}
		// An empty value for anything but a string is treated as absent.
		if f.typ.Kind() != reflect.String && !f.slice && len(vals) > 0 && vals[0] == "" {
			vals = nil
		}
		if len(vals) == 0 {
			switch {
			case f.required:
				fail(f.name, "is required")
				continue
			case f.hasDef:
				vals = []string{f.def}
			default:
				continue
			}
		}
		switch {
		case f.slice:
			s := reflect.MakeSlice(fv.Type(), len(vals), len(vals))
			ok := true
			for j, val := range vals {
				if err := f.codec.parse(val, s.Index(j)); err != nil {
					fail(f.name, "must be "+f.codec.want)
					ok = false
					break
				}
			}
			if ok {
				fv.Set(s)
				if f.rules != nil {
					f.rules.apply(s, true, f.name, fail)
				}
			}
		case f.ptr:
			pv := reflect.New(f.typ)
			if err := f.codec.parse(vals[0], pv.Elem()); err != nil {
				fail(f.name, "must be "+f.codec.want)
				continue
			}
			fv.Set(pv)
			if f.rules != nil {
				f.rules.apply(pv.Elem(), false, f.name, fail)
			}
		default:
			if err := f.codec.parse(vals[0], fv); err != nil {
				fv.SetZero()
				fail(f.name, "must be "+f.codec.want)
			} else if f.rules != nil {
				f.rules.apply(fv, false, f.name, fail)
			}
		}
	}
	if errs != nil {
		return errs
	}
	if p.validate {
		var val validator
		if p.typ.Implements(validatorType) {
			val = v.Interface().(validator)
		} else {
			val = v.Addr().Interface().(validator)
		}
		if err := val.Validate(); err != nil {
			var prob *Problem
			var fe FieldErrors
			if errors.As(err, &prob) || errors.As(err, &fe) {
				return err
			}
			return BadRequest(err.Error())
		}
	}
	return nil
}

func (f *field) bindBody(w http.ResponseWriter, r *http.Request, fv reflect.Value, maxBody int64, fail func(name, msg string)) error {
	if ct := r.Header.Get("Content-Type"); ct != "" {
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil || !(mt == "application/json" || strings.HasSuffix(mt, "+json")) {
			return Status(http.StatusUnsupportedMediaType, "the request body must be application/json")
		}
	}
	target := fv
	if f.ptr {
		target = reflect.New(f.typ).Elem()
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	err := dec.Decode(target.Addr().Interface())
	var tooBig *http.MaxBytesError
	var wrongType *json.UnmarshalTypeError
	switch {
	case err == nil:
		if f.ptr {
			fv.Set(target.Addr())
		}
		f.body.walk(target, "", fail)
	case errors.Is(err, io.EOF):
		if !f.ptr {
			return BadRequest("a JSON request body is required")
		}
	case errors.As(err, &tooBig):
		return Status(http.StatusRequestEntityTooLarge,
			fmt.Sprintf("the request body is larger than %d bytes", tooBig.Limit))
	case errors.As(err, &wrongType) && wrongType.Field != "":
		fail(wrongType.Field, "must be "+jsonKind(wrongType.Type))
	default:
		return BadRequest("the request body is not valid JSON")
	}
	return nil
}

func jsonKind(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "an integer"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Slice, reflect.Array:
		return "an array"
	}
	return "an object"
}
