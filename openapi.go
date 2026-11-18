package mapget

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"unicode"
)

// Info is the title block of an OpenAPI document.
type Info struct {
	Title       string
	Version     string
	Description string
}

// Document is an OpenAPI 3.1 document. It marshals to JSON with its keys in
// a fixed order, and it is an http.Handler that serves itself.
type Document map[string]any

// ServeHTTP writes the document as JSON.
func (d Document) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	body, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// OpenAPI describes every route registered so far that has a method. The
// parameters come from the request types' tags and the schemas from the Go
// types, read the way encoding/json reads them. A field's doc tag becomes
// its description. Call it after the routes are registered.
func (r *Router) OpenAPI(info Info) Document {
	rt := r.root
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	g := &schemaGen{defs: map[string]any{}, names: map[reflect.Type]string{}, taken: map[string]bool{"Problem": true}}
	g.defs["Problem"] = problemSchema()
	paths := map[string]any{}
	opIDs := map[string]bool{}

	for _, e := range rt.entries {
		if e.pat.method == "" {
			continue
		}
		op := map[string]any{}
		responses := map[string]any{}
		op["responses"] = responses
		if e.plan == nil {
			responses["default"] = map[string]any{"description": "Not described."}
		} else {
			p := e.plan
			if p.summary != "" {
				op["summary"] = p.summary
			}
			if len(p.tags) > 0 {
				op["tags"] = p.tags
			}
			if name := p.typ.Name(); name != "" && !strings.Contains(name, "[") {
				id := string(unicode.ToLower(rune(name[0]))) + name[1:]
				if !opIDs[id] {
					opIDs[id] = true
					op["operationId"] = id
				}
			}
			var params []any
			for i := range p.fields {
				f := &p.fields[i]
				switch f.src {
				case fromBody:
					op["requestBody"] = map[string]any{
						"required": !f.ptr,
						"content":  jsonContent(g.of(f.typ)),
					}
				case fromPath, fromQuery, fromHeader, fromCookie:
					schema := g.of(f.typ)
					if f.hasDef {
						schema["default"] = defaultValue(f)
					}
					if f.slice {
						schema = map[string]any{"type": "array", "items": schema}
					}
					param := map[string]any{"name": f.name, "in": f.src.String(), "schema": schema}
					if f.src == fromPath || f.required {
						param["required"] = true
					}
					if f.doc != "" {
						param["description"] = f.doc
					}
					params = append(params, param)
				}
			}
			if params != nil {
				op["parameters"] = params
			}
			status, body, known := responseOf(e.out)
			switch {
			case !known:
				responses["200"] = map[string]any{"description": "Written by the handler."}
			case body == nil:
				responses[strconv.Itoa(status)] = map[string]any{"description": http.StatusText(status)}
			default:
				responses[strconv.Itoa(status)] = map[string]any{
					"description": http.StatusText(status),
					"content":     jsonContent(g.of(body)),
				}
			}
			responses["default"] = map[string]any{
				"description": "An error, as RFC 9457 problem details.",
				"content": map[string]any{"application/problem+json": map[string]any{
					"schema": map[string]any{"$ref": "#/components/schemas/Problem"},
				}},
			}
		}
		key := e.pat.openAPIPath()
		item, _ := paths[key].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[key] = item
		}
		item[strings.ToLower(e.pat.method)] = op
	}

	infoObj := map[string]any{"title": info.Title, "version": info.Version}
	if info.Description != "" {
		infoObj["description"] = info.Description
	}
	return Document{
		"openapi":    "3.1.0",
		"info":       infoObj,
		"paths":      paths,
		"components": map[string]any{"schemas": g.defs},
	}
}

func jsonContent(schema map[string]any) map[string]any {
	return map[string]any{"application/json": map[string]any{"schema": schema}}
}

// defaultValue is a field's default as the JSON value its schema describes.
func defaultValue(f *field) any {
	v := reflect.New(f.typ).Elem()
	f.codec.parse(f.def, v)
	switch f.typ.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		if f.typ != durationType {
			return v.Interface()
		}
	}
	return f.def
}

func problemSchema() map[string]any {
	str := map[string]any{"type": "string"}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type":     str,
			"title":    str,
			"status":   map[string]any{"type": "integer", "format": "int32"},
			"detail":   str,
			"instance": str,
			"errors": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "array", "items": str},
			},
		},
		"required": []string{"title", "status"},
	}
}

// schemaGen turns Go types into JSON Schema. Named structs become components
// and are referred to; everything else is written in place.
type schemaGen struct {
	defs  map[string]any
	names map[reflect.Type]string
	taken map[string]bool
}

var jsonMarshalerType = reflect.TypeFor[json.Marshaler]()

func (g *schemaGen) of(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	case t == durationType:
		return map[string]any{"type": "string"}
	case t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType):
		return map[string]any{"type": "string"}
	case t.Implements(jsonMarshalerType) || reflect.PointerTo(t).Implements(jsonMarshalerType):
		return map[string]any{}
	}
	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int8, reflect.Int16, reflect.Int32:
		return map[string]any{"type": "integer", "format": "int32"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer", "format": "int64"}
	case reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return map[string]any{"type": "integer", "format": "int32", "minimum": 0}
	case reflect.Uint, reflect.Uint64:
		return map[string]any{"type": "integer", "format": "int64", "minimum": 0}
	case reflect.Float32:
		return map[string]any{"type": "number", "format": "float"}
	case reflect.Float64:
		return map[string]any{"type": "number", "format": "double"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "contentEncoding": "base64"}
		}
		return map[string]any{"type": "array", "items": g.of(t.Elem())}
	case reflect.Array:
		return map[string]any{"type": "array", "items": g.of(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": g.of(t.Elem())}
	case reflect.Struct:
		if t.Name() == "" {
			return g.object(t)
		}
		name, done := g.names[t]
		if !done {
			name = g.name(t)
			g.names[t] = name
			g.defs[name] = map[string]any{} // holds the place while a recursive type refers to itself
			g.defs[name] = g.object(t)
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	return map[string]any{}
}

// name picks a component name: the type's own, with the package paths taken
// out of a generic type's arguments, and a number added if it is taken.
func (g *schemaGen) name(t reflect.Type) string {
	var b strings.Builder
	for _, run := range strings.FieldsFunc(t.Name(), func(r rune) bool { return r == '[' || r == ']' || r == ',' }) {
		if i := strings.LastIndexAny(run, "./"); i >= 0 {
			run = run[i+1:]
		}
		if run != "" {
			b.WriteString(strings.ToUpper(run[:1]) + run[1:])
		}
	}
	base := b.String()
	name := base
	for n := 2; g.taken[name]; n++ {
		name = base + strconv.Itoa(n)
	}
	g.taken[name] = true
	return name
}

func (g *schemaGen) object(t reflect.Type) map[string]any {
	props := map[string]any{}
	var required []string
	g.fields(t, props, &required)
	obj := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		obj["required"] = required
	}
	return obj
}

func (g *schemaGen) fields(t reflect.Type, props map[string]any, required *[]string) {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag := sf.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		if tag == "-" {
			continue
		}
		ft := sf.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if sf.Anonymous && name == "" && ft.Kind() == reflect.Struct {
			g.fields(ft, props, required)
			continue
		}
		if !sf.IsExported() {
			continue
		}
		if name == "" {
			name = sf.Name
		}
		schema := g.of(sf.Type)
		if doc := sf.Tag.Get("doc"); doc != "" {
			schema["description"] = doc
		}
		props[name] = schema
		optional := sf.Type.Kind() == reflect.Pointer ||
			strings.Contains(","+opts+",", ",omitempty,") || strings.Contains(","+opts+",", ",omitzero,")
		if !optional {
			*required = append(*required, name)
		}
	}
}
