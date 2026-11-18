package mapget_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AndyCross/mapget"
)

type Colour string

type Search struct {
	mapget.Route `route:"GET /shops/{shop}/tiles/{id}" summary:"Find a tile" tags:"tiles"`
	Shop         string        `path:"shop"`
	ID           int64         `path:"id"`
	Q            string        `query:"q"`
	Page         int           `query:"page" default:"1"`
	Colours      []Colour      `query:"colour"`
	Since        *time.Time    `query:"since"`
	Wait         time.Duration `query:"wait"`
	Glazed       bool          `query:"glazed"`
	Trace        string        `header:"X-Trace"`
	Session      string        `cookie:"session"`
	Req          *http.Request
}

type Echo struct {
	Shop    string
	ID      int64
	Q       string
	Page    int
	Colours []Colour
	Since   *time.Time
	Wait    string
	Glazed  bool
	Trace   string
	Session string
	Method  string
}

func search(_ context.Context, in Search) (Echo, error) {
	return Echo{in.Shop, in.ID, in.Q, in.Page, in.Colours, in.Since, in.Wait.String(), in.Glazed, in.Trace, in.Session, in.Req.Method}, nil
}

func do(t *testing.T, h http.Handler, method, target, body string, hdr ...string) (*http.Response, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decoding %q: %v", s, err)
	}
	return v
}

func TestBindEverySource(t *testing.T) {
	mux := http.NewServeMux()
	mapget.Handle(mapget.New(mux), search)

	res, body := do(t, mux, "GET",
		"/shops/triana/tiles/42?q=azul+cobalto&page=3&colour=blue&colour=white&since=2026-11-01T09:00:00Z&wait=90s&glazed=true",
		"", "X-Trace", "abc", "Cookie", "session=s1")
	if res.StatusCode != 200 {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type %q", ct)
	}
	since := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	want := Echo{"triana", 42, "azul cobalto", 3, []Colour{"blue", "white"}, &since, "1m30s", true, "abc", "s1", "GET"}
	if got := decode[Echo](t, body); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestDefaultsAndAbsentValues(t *testing.T) {
	mux := http.NewServeMux()
	mapget.Handle(mapget.New(mux), search)
	_, body := do(t, mux, "GET", "/shops/triana/tiles/42?page=", "")
	got := decode[Echo](t, body)
	if got.Page != 1 || got.Since != nil || got.Colours != nil || got.Q != "" {
		t.Errorf("got %+v", got)
	}
}

func TestPathTypeIsTheConstraint(t *testing.T) {
	mux := http.NewServeMux()
	mapget.Handle(mapget.New(mux), search)
	for _, target := range []string{"/shops/triana/tiles/abc", "/shops/triana/tiles/1.5", "/shops/triana/tiles/99999999999999999999"} {
		res, body := do(t, mux, "GET", target, "")
		if res.StatusCode != 404 {
			t.Errorf("%s: status %d, want 404", target, res.StatusCode)
		}
		if ct := res.Header.Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("%s: content type %q", target, ct)
		}
		if p := decode[map[string]any](t, body); p["title"] != "Not Found" || p["status"] != 404.0 {
			t.Errorf("%s: body %s", target, body)
		}
	}
}

func TestQueryErrorsAreAllReported(t *testing.T) {
	mux := http.NewServeMux()
	mapget.Handle(mapget.New(mux), search)
	res, body := do(t, mux, "GET", "/shops/triana/tiles/42?page=two&since=yesterday&glazed=maybe&wait=soon", "")
	if res.StatusCode != 400 {
		t.Fatalf("status %d", res.StatusCode)
	}
	p := decode[struct {
		Title  string
		Errors map[string][]string
	}](t, body)
	want := map[string][]string{
		"page":   {"must be an integer"},
		"since":  {"must be an RFC 3339 time"},
		"glazed": {"must be true or false"},
		"wait":   {"must be a duration such as 90s or 1h30m"},
	}
	if !reflect.DeepEqual(p.Errors, want) {
		t.Errorf("errors %v", p.Errors)
	}
	if p.Title != "One or more validation errors occurred." {
		t.Errorf("title %q", p.Title)
	}
}

type Required struct {
	mapget.Route `route:"GET /report"`
	Year         int    `query:"year,required"`
	Key          string `header:"X-Key,required"`
}

func TestRequired(t *testing.T) {
	mux := http.NewServeMux()
	mapget.Handle(mapget.New(mux), func(_ context.Context, in Required) (int, error) { return in.Year, nil })
	res, body := do(t, mux, "GET", "/report", "")
	p := decode[struct{ Errors map[string][]string }](t, body)
	if res.StatusCode != 400 || len(p.Errors) != 2 || p.Errors["year"][0] != "is required" {
		t.Errorf("status %d body %s", res.StatusCode, body)
	}
	if res, body := do(t, mux, "GET", "/report?year=2026", "", "X-Key", "k"); res.StatusCode != 200 || body != "2026\n" {
		t.Errorf("status %d body %q", res.StatusCode, body)
	}
}

type Tile struct {
	Name string `json:"name"`
	Size int    `json:"size"`
}

type PutTile struct {
	mapget.Route `route:"PUT /tiles/{id}"`
	ID           int  `path:"id"`
	Body         Tile `body:"json"`
}

func (p PutTile) Validate() error {
	if p.Body.Size <= 0 {
		return mapget.FieldErrors{"size": {"must be more than zero"}}
	}
	if p.Body.Name == "forbidden" {
		return mapget.Forbidden("not that one")
	}
	if p.Body.Name == "plain" {
		return errors.New("a plain error")
	}
	return nil
}

func TestBody(t *testing.T) {
	mux := http.NewServeMux()
	mapget.Handle(mapget.New(mux, mapget.MaxBody(64)), func(_ context.Context, in PutTile) (Tile, error) { return in.Body, nil })

	cases := []struct {
		name, body, ct string
		status         int
		contains       string
	}{
		{"good", `{"name":"estrella","size":20}`, "application/json; charset=utf-8", 200, `"estrella"`},
		{"vendor json", `{"name":"estrella","size":20}`, "application/vnd.tiles+json", 200, `"estrella"`},
		{"no content type", `{"name":"estrella","size":20}`, "-", 200, `"estrella"`},
		{"form", `name=x`, "application/x-www-form-urlencoded", 415, "application/json"},
		{"empty", ``, "application/json", 400, "body is required"},
		{"malformed", `{"name":`, "application/json", 400, "not valid JSON"},
		{"wrong type", `{"name":"x","size":"big"}`, "application/json", 400, `"size":["must be an integer"]`},
		{"too big", `{"name":"` + strings.Repeat("x", 100) + `","size":1}`, "application/json", 413, "larger than 64 bytes"},
		{"validate fields", `{"name":"x","size":0}`, "application/json", 400, `"size":["must be more than zero"]`},
		{"validate problem", `{"name":"forbidden","size":1}`, "application/json", 403, "not that one"},
		{"validate plain", `{"name":"plain","size":1}`, "application/json", 400, "a plain error"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("PUT", "/tiles/1", strings.NewReader(c.body))
		if c.ct != "-" {
			req.Header.Set("Content-Type", c.ct)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.contains) {
			t.Errorf("%s: status %d body %s", c.name, rec.Code, rec.Body)
		}
	}
}

type Optional struct {
	mapget.Route `route:"PATCH /tiles/{id}"`
	ID           int   `path:"id"`
	Body         *Tile `body:"json"`
}

func TestOptionalBody(t *testing.T) {
	mux := http.NewServeMux()
	mapget.Handle(mapget.New(mux), func(_ context.Context, in Optional) (bool, error) { return in.Body != nil, nil })
	if _, body := do(t, mux, "PATCH", "/tiles/1", ""); body != "false\n" {
		t.Errorf("no body: %q", body)
	}
	if _, body := do(t, mux, "PATCH", "/tiles/1", `{"name":"x"}`); body != "true\n" {
		t.Errorf("body: %q", body)
	}
}

type (
	MakeTile struct {
		mapget.Route `route:"POST /tiles"`
		Body         Tile `body:"json"`
	}
	DropTile struct {
		mapget.Route `route:"DELETE /tiles/{id}"`
		ID           int `path:"id"`
	}
	QueueTile struct {
		mapget.Route `route:"POST /tiles/{id}/fire"`
		ID           int `path:"id"`
	}
	OldTile struct {
		mapget.Route `route:"GET /azulejos/{id}"`
		ID           int `path:"id"`
	}
	OneTile struct {
		mapget.Route `route:"GET /tiles/{id}"`
		ID           int `path:"id"`
	}
)

func TestResults(t *testing.T) {
	mux := http.NewServeMux()
	r := mapget.New(mux)
	mapget.Handle(r, func(_ context.Context, in MakeTile) (mapget.Created[Tile], error) {
		return mapget.Created[Tile]{Location: r.MustPath(OneTile{ID: 7}), Body: in.Body}, nil
	})
	mapget.Handle(r, func(_ context.Context, in DropTile) (mapget.NoContent, error) { return mapget.NoContent{}, nil })
	mapget.Handle(r, func(_ context.Context, in QueueTile) (mapget.Accepted[string], error) {
		return mapget.Accepted[string]{Body: "queued"}, nil
	})
	mapget.Handle(r, func(_ context.Context, in OldTile) (http.Handler, error) {
		return http.RedirectHandler(r.MustPath(OneTile{ID: in.ID}), http.StatusMovedPermanently), nil
	})
	mapget.Handle(r, func(_ context.Context, in OneTile) (struct{}, error) { return struct{}{}, nil })

	res, body := do(t, mux, "POST", "/tiles", `{"name":"a","size":1}`)
	if res.StatusCode != 201 || res.Header.Get("Location") != "/tiles/7" || !strings.Contains(body, `"a"`) {
		t.Errorf("created: %d %v %s", res.StatusCode, res.Header, body)
	}
	if res, body := do(t, mux, "DELETE", "/tiles/7", ""); res.StatusCode != 204 || body != "" {
		t.Errorf("no content: %d %q", res.StatusCode, body)
	}
	if res, body := do(t, mux, "POST", "/tiles/7/fire", ""); res.StatusCode != 202 || body != "\"queued\"\n" {
		t.Errorf("accepted: %d %q", res.StatusCode, body)
	}
	if res, _ := do(t, mux, "GET", "/azulejos/7", ""); res.StatusCode != 301 || res.Header.Get("Location") != "/tiles/7" {
		t.Errorf("handler: %d %v", res.StatusCode, res.Header)
	}
	if res, _ := do(t, mux, "GET", "/tiles/7", ""); res.StatusCode != 204 {
		t.Errorf("empty struct: %d", res.StatusCode)
	}
	// The mux answers HEAD from a GET pattern and 405 for anything else.
	if res, _ := do(t, mux, "HEAD", "/tiles/7", ""); res.StatusCode != 204 {
		t.Errorf("head: %d", res.StatusCode)
	}
	if res, _ := do(t, mux, "PUT", "/azulejos/7", ""); res.StatusCode != 405 || res.Header.Get("Allow") != "GET, HEAD" {
		t.Errorf("405: %d %q", res.StatusCode, res.Header.Get("Allow"))
	}
}

func TestErrors(t *testing.T) {
	mux := http.NewServeMux()
	var logged error
	r := mapget.New(mux, mapget.OnError(func(_ *http.Request, err error) { logged = err }))
	mapget.HandleAt(r, "GET /fail/{how}", func(_ context.Context, in struct {
		How string `path:"how"`
	}) (string, error) {
		switch in.How {
		case "problem":
			return "", &mapget.Problem{Status: 402, Type: "https://example.com/out-of-credit", Title: "Out of credit",
				Detail: "top up", Instance: "/accounts/7", Extensions: map[string]any{"balance": 30}}
		case "fields":
			return "", mapget.FieldErrors{"name": {"is taken"}}
		case "wrapped":
			return "", errors.Join(errors.New("context"), mapget.Conflict("already there"))
		}
		return "", errors.New("the database password is hunter2")
	})

	res, body := do(t, mux, "GET", "/fail/problem", "")
	p := decode[map[string]any](t, body)
	if res.StatusCode != 402 || p["type"] != "https://example.com/out-of-credit" || p["balance"] != 30.0 || p["instance"] != "/accounts/7" {
		t.Errorf("problem: %d %s", res.StatusCode, body)
	}
	if res, body := do(t, mux, "GET", "/fail/fields", ""); res.StatusCode != 400 || !strings.Contains(body, `"name":["is taken"]`) {
		t.Errorf("fields: %d %s", res.StatusCode, body)
	}
	if res, body := do(t, mux, "GET", "/fail/wrapped", ""); res.StatusCode != 409 || !strings.Contains(body, "already there") {
		t.Errorf("wrapped: %d %s", res.StatusCode, body)
	}
	res, body = do(t, mux, "GET", "/fail/other", "")
	if res.StatusCode != 500 || strings.Contains(body, "hunter2") || logged == nil {
		t.Errorf("other: %d %s logged=%v", res.StatusCode, body, logged)
	}
}

func TestGroupsAndMiddleware(t *testing.T) {
	mux := http.NewServeMux()
	var trail []string
	mark := func(name string) mapget.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				trail = append(trail, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	app := mapget.New(mux)
	app.Use(mark("app"))
	api := app.Group("/api", mark("api"))
	tenant := api.Group("/tenants/{tenant}", mark("tenant"))

	type inTenant struct {
		Tenant string `path:"tenant"`
		ID     int    `path:"id"`
	}
	mapget.HandleAt(tenant, "GET /tiles/{id}", func(_ context.Context, in inTenant) (string, error) {
		return in.Tenant, nil
	})
	api.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })

	if _, body := do(t, mux, "GET", "/api/tenants/sevilla/tiles/3", ""); body != "\"sevilla\"\n" {
		t.Errorf("body %q", body)
	}
	if got := strings.Join(trail, ","); got != "app,api,tenant" {
		t.Errorf("middleware order %s", got)
	}
	trail = nil
	if _, body := do(t, mux, "GET", "/api/health", ""); body != "ok" || strings.Join(trail, ",") != "app,api" {
		t.Errorf("plain handler: %q %v", body, trail)
	}
	if got, err := tenant.Path(inTenant{Tenant: "cádiz", ID: 3}); err != nil || got != "/api/tenants/c%C3%A1diz/tiles/3" {
		t.Errorf("path %q %v", got, err)
	}

	var sb strings.Builder
	app.WriteRoutes(&sb)
	if got := strings.Fields(sb.String()); len(got) != 8 || got[1] != "/api/health" || got[2] != "-" || got[5] != "/api/tenants/{tenant}/tiles/{id}" || got[7] != "string" {
		t.Errorf("table:\n%s", sb.String())
	}
	routes := app.Routes()
	if len(routes) != 2 || routes[0].Pattern != "GET /api/health" || routes[1].Pattern != "GET /api/tenants/{tenant}/tiles/{id}" ||
		routes[1].In != reflect.TypeFor[inTenant]() || routes[0].In != nil {
		t.Errorf("routes %+v\n%s", routes, sb.String())
	}
}

func mustPanic(t *testing.T, name, contains string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		msg, _ := recover().(string)
		if !strings.Contains(msg, contains) {
			t.Errorf("%s: panic %q, want it to contain %q", name, msg, contains)
		}
	}()
	f()
}

func TestMistakesStopTheProgramAtRegistration(t *testing.T) {
	type typo struct {
		mapget.Route `route:"GET /a/{id}"`
		ID           int `path:"idd"`
	}
	type missing struct {
		mapget.Route `route:"GET /b/{id}/{rev}"`
		ID           int `path:"id"`
	}
	type noRoute struct {
		ID int `query:"id"`
	}
	type badDefault struct {
		mapget.Route `route:"GET /c"`
		Page         int `query:"page" default:"one"`
	}
	type unreadable struct {
		mapget.Route `route:"GET /d"`
		M            map[string]string `query:"m"`
	}
	type twoSources struct {
		mapget.Route `route:"GET /e/{id}"`
		ID           int `path:"id" query:"id"`
	}
	type pointerPath struct {
		mapget.Route `route:"GET /f/{id}"`
		ID           *int `path:"id"`
	}
	type badOption struct {
		mapget.Route `route:"GET /g"`
		Q            string `query:"q,omitempty"`
	}
	type routed struct {
		mapget.Route `route:"GET /h"`
	}
	r := func() *mapget.Router { return mapget.New(http.NewServeMux()) }
	mustPanic(t, "typo", `reads path value "idd", which is not in the pattern "GET /a/{id}"`, func() {
		mapget.Handle(r(), func(context.Context, typo) (int, error) { return 0, nil })
	})
	mustPanic(t, "missing", `has the wildcard {rev}`, func() {
		mapget.Handle(r(), func(context.Context, missing) (int, error) { return 0, nil })
	})
	mustPanic(t, "no route", "has no route", func() {
		mapget.Handle(r(), func(context.Context, noRoute) (int, error) { return 0, nil })
	})
	mustPanic(t, "bad default", `default "one" is not an integer`, func() {
		mapget.Handle(r(), func(context.Context, badDefault) (int, error) { return 0, nil })
	})
	mustPanic(t, "unreadable", "cannot read a map[string]string from text", func() {
		mapget.Handle(r(), func(context.Context, unreadable) (int, error) { return 0, nil })
	})
	mustPanic(t, "two sources", "more than one of", func() {
		mapget.Handle(r(), func(context.Context, twoSources) (int, error) { return 0, nil })
	})
	mustPanic(t, "pointer path", "cannot be a pointer", func() {
		mapget.Handle(r(), func(context.Context, pointerPath) (int, error) { return 0, nil })
	})
	mustPanic(t, "bad option", `unknown option "omitempty"`, func() {
		mapget.Handle(r(), func(context.Context, badOption) (int, error) { return 0, nil })
	})
	mustPanic(t, "HandleAt on routed", "already carries the route", func() {
		mapget.HandleAt(r(), "GET /x", func(context.Context, routed) (int, error) { return 0, nil })
	})
	mustPanic(t, "not a struct", "must be a struct", func() {
		mapget.HandleAt(r(), "GET /x", func(context.Context, int) (int, error) { return 0, nil })
	})
	mustPanic(t, "prefix clash", "is in both the group prefix and the pattern", func() {
		mapget.Handle(r().Group("/x/{id}"), func(context.Context, typo) (int, error) { return 0, nil })
	})
	mustPanic(t, "bad prefix", "must not end with a slash", func() { r().Group("/x/") })
}

type File struct {
	mapget.Route `route:"GET /files/{bucket}/{key...}"`
	Bucket       string   `path:"bucket"`
	Key          string   `path:"key"`
	Version      *int     `query:"version"`
	Tags         []string `query:"tag"`
	Raw          bool     `query:"raw"`
	Page         int      `query:"page" default:"1"`
}

type Home struct {
	mapget.Route `route:"GET /{$}"`
}

type Docs struct {
	mapget.Route `route:"GET /docs/"`
}

func TestPath(t *testing.T) {
	mux := http.NewServeMux()
	r := mapget.New(mux)
	mapget.Handle(r, func(_ context.Context, in File) (File, error) { return in, nil })
	mapget.Handle(r, func(context.Context, Home) (string, error) { return "home", nil })
	mapget.Handle(r, func(context.Context, Docs) (string, error) { return "docs", nil })

	three := 3
	cases := []struct {
		in   any
		want string
	}{
		{File{Bucket: "b", Key: "a/b/c.txt", Page: 1}, "/files/b/a/b/c.txt"},
		{&File{Bucket: "my bucket", Key: "año/100%.txt", Page: 1}, "/files/my%20bucket/a%C3%B1o/100%25.txt"},
		{File{Bucket: "a/b", Key: "k", Page: 1}, "/files/a%2Fb/k"},
		{File{Bucket: "..", Key: "./x", Page: 1}, "/files/%2E%2E/%2E/x"},
		{File{Bucket: "b", Key: "", Page: 1}, "/files/b/"},
		{File{Bucket: "b", Key: "k", Version: &three, Tags: []string{"x y", "z"}, Raw: true, Page: 2}, "/files/b/k?page=2&raw=true&tag=x+y&tag=z&version=3"},
		{File{Bucket: "b", Key: "k", Page: 0}, "/files/b/k?page=0"},
		{Home{}, "/"},
		{Docs{}, "/docs/"},
	}
	for _, c := range cases {
		got, err := r.Path(c.in)
		if err != nil || got != c.want {
			t.Errorf("Path(%+v) = %q, %v; want %q", c.in, got, err, c.want)
			continue
		}
		if res, body := do(t, mux, "GET", got, ""); res.StatusCode != 200 {
			t.Errorf("GET %s: status %d %s", got, res.StatusCode, body)
		}
	}
	for _, bad := range []any{File{Key: "k"}, File{Bucket: "b", Key: "a//b"}, File{Bucket: "b", Key: "/a"}, Tile{}, 3, nil} {
		if got, err := r.Path(bad); err == nil {
			t.Errorf("Path(%+v) = %q, want an error", bad, got)
		}
	}
}

func TestPathIsAmbiguousForATypeWithTwoRoutes(t *testing.T) {
	r := mapget.New(http.NewServeMux())
	type none struct{}
	h := func(context.Context, none) (string, error) { return "", nil }
	mapget.HandleAt(r, "GET /a", h)
	mapget.HandleAt(r, "GET /b", h)
	if _, err := r.Path(none{}); err == nil || !strings.Contains(err.Error(), "more than one route") {
		t.Errorf("err %v", err)
	}
}
