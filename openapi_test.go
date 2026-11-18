package mapget_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/AndyCross/mapget"
)

type Page[T any] struct {
	Items []T    `json:"items"`
	Next  string `json:"next,omitempty"`
}

type Node struct {
	Name     string            `json:"name" doc:"What it is called."`
	Children []*Node           `json:"children,omitempty"`
	Parent   *Node             `json:"parent"`
	Labels   map[string]string `json:"labels"`
	Blob     []byte            `json:"blob"`
	Made     time.Time         `json:"made"`
	Skipped  string            `json:"-"`
	Any      any               `json:"any"`
	Embedded
}

type Embedded struct {
	Score float32 `json:"score"`
}

type ListNodes struct {
	mapget.Route `route:"GET /nodes" summary:"List nodes" tags:"nodes, tree"`
	Depth        int       `query:"depth" default:"2" doc:"How far down to go."`
	Kinds        []string  `query:"kind"`
	After        time.Time `query:"after,required"`
	Tenant       string    `header:"X-Tenant"`
}

type PutNode struct {
	mapget.Route `route:"PUT /nodes/{path...}"`
	Path         string `path:"path"`
	Body         Node   `body:"json"`
}

// at walks a decoded JSON document by keys.
func at(t *testing.T, v any, keys ...string) any {
	t.Helper()
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("at %q: not an object: %v", k, v)
		}
		v, ok = m[k]
		if !ok {
			t.Fatalf("no key %q in %v", k, m)
		}
	}
	return v
}

func TestOpenAPI(t *testing.T) {
	mux := http.NewServeMux()
	r := mapget.New(mux).Group("/v1")
	mapget.Handle(r, func(context.Context, ListNodes) (Page[Node], error) { return Page[Node]{}, nil })
	mapget.Handle(r, func(context.Context, PutNode) (mapget.Created[Node], error) { return mapget.Created[Node]{}, nil })
	mapget.HandleAt(r, "DELETE /nodes/{id}", func(context.Context, struct {
		ID int32 `path:"id"`
	}) (mapget.NoContent, error) {
		return mapget.NoContent{}, nil
	})
	r.HandleFunc("GET /health", func(http.ResponseWriter, *http.Request) {})
	r.HandleFunc("/any-method", func(http.ResponseWriter, *http.Request) {})

	doc := r.OpenAPI(mapget.Info{Title: "Nodes", Version: "1.2.3"})
	mux.Handle("GET /openapi.json", doc)
	res, body := do(t, mux, "GET", "/openapi.json", "")
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	d := decode[map[string]any](t, body)

	eq := func(got any, want string) {
		t.Helper()
		b, _ := json.Marshal(got)
		if string(b) != want {
			t.Errorf("got  %s\nwant %s", b, want)
		}
	}
	eq(at(t, d, "openapi"), `"3.1.0"`)
	eq(at(t, d, "info"), `{"title":"Nodes","version":"1.2.3"}`)

	list := at(t, d, "paths", "/v1/nodes", "get")
	eq(at(t, list, "operationId"), `"listNodes"`)
	eq(at(t, list, "summary"), `"List nodes"`)
	eq(at(t, list, "tags"), `["nodes","tree"]`)
	eq(at(t, list, "parameters"), `[`+
		`{"description":"How far down to go.","in":"query","name":"depth","schema":{"default":2,"format":"int64","type":"integer"}},`+
		`{"in":"query","name":"kind","schema":{"items":{"type":"string"},"type":"array"}},`+
		`{"in":"query","name":"after","required":true,"schema":{"format":"date-time","type":"string"}},`+
		`{"in":"header","name":"X-Tenant","schema":{"type":"string"}}]`)
	eq(at(t, list, "responses", "200", "content", "application/json", "schema"), `{"$ref":"#/components/schemas/PageNode"}`)
	eq(at(t, list, "responses", "default", "content", "application/problem+json", "schema"), `{"$ref":"#/components/schemas/Problem"}`)

	put := at(t, d, "paths", "/v1/nodes/{path}", "put")
	eq(at(t, put, "parameters"), `[{"in":"path","name":"path","required":true,"schema":{"type":"string"}}]`)
	eq(at(t, put, "requestBody"), `{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Node"}}},"required":true}`)
	eq(at(t, put, "responses", "201", "content", "application/json", "schema"), `{"$ref":"#/components/schemas/Node"}`)

	del := at(t, d, "paths", "/v1/nodes/{id}", "delete")
	eq(at(t, del, "parameters"), `[{"in":"path","name":"id","required":true,"schema":{"format":"int32","type":"integer"}}]`)
	eq(at(t, del, "responses", "204"), `{"description":"No Content"}`)

	eq(at(t, d, "paths", "/v1/health", "get"), `{"responses":{"default":{"description":"Not described."}}}`)
	if _, ok := at(t, d, "paths").(map[string]any)["/v1/any-method"]; ok {
		t.Error("a pattern with no method should not be described")
	}

	eq(at(t, d, "components", "schemas", "PageNode"),
		`{"properties":{"items":{"items":{"$ref":"#/components/schemas/Node"},"type":"array"},"next":{"type":"string"}},"required":["items"],"type":"object"}`)
	eq(at(t, d, "components", "schemas", "Node"), `{"properties":{`+
		`"any":{},`+
		`"blob":{"contentEncoding":"base64","type":"string"},`+
		`"children":{"items":{"$ref":"#/components/schemas/Node"},"type":"array"},`+
		`"labels":{"additionalProperties":{"type":"string"},"type":"object"},`+
		`"made":{"format":"date-time","type":"string"},`+
		`"name":{"description":"What it is called.","type":"string"},`+
		`"parent":{"$ref":"#/components/schemas/Node"},`+
		`"score":{"format":"float","type":"number"}},`+
		`"required":["name","labels","blob","made","any","score"],"type":"object"}`)
}
