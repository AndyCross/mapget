package mapget_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/AndyCross/mapget"
)

type Trip struct {
	mapget.Route `route:"GET /trip/{name}/{n}/{rest...}"`
	Name         string   `path:"name" json:"name"`
	N            int      `path:"n" json:"n"`
	Rest         string   `path:"rest" json:"rest"`
	Q            string   `query:"q" json:"q"`
	Page         int      `query:"page" default:"1" json:"page"`
	Ratio        float64  `query:"ratio" json:"ratio"`
	On           bool     `query:"on" json:"on"`
	Tags         []string `query:"tag" json:"tags"`
}

// FuzzPathRoundTrip checks that Path and binding are inverses: whatever
// address Path writes for a value, a request for that address through a real
// ServeMux binds to the same value.
func FuzzPathRoundTrip(f *testing.F) {
	mux := http.NewServeMux()
	r := mapget.New(mux)
	mapget.Handle(r, func(_ context.Context, in Trip) (Trip, error) { return in, nil })

	f.Add("a", 1, "b/c", "q", 2, 0.5, true, "x")
	f.Add("..", -7, "./../x", "a&b=c", 0, -1e300, false, "")
	f.Add("a/b", 0, "", "ñ ?#%", 1, 0.0, false, "é/+")
	f.Add("%2F", 0, "trailing/", "", 1, 0.0, false, " ")

	f.Fuzz(func(t *testing.T, name string, n int, rest, q string, page int, ratio float64, on bool, tag string) {
		if !utf8.ValidString(name) || !utf8.ValidString(rest) || !utf8.ValidString(q) || !utf8.ValidString(tag) {
			t.Skip() // the response is JSON, which cannot carry invalid UTF-8 back for comparison
		}
		if ratio != ratio {
			t.Skip() // NaN is not equal to itself
		}
		in := Trip{Name: name, N: n, Rest: rest, Q: q, Page: page, Ratio: ratio, On: on}
		if tag != "" {
			in.Tags = []string{tag, tag + "2"}
		}
		target, err := r.Path(in)
		if err != nil {
			if name == "" || name == "/" || strings.HasPrefix(rest, "/") || strings.Contains(rest, "//") {
				return // Path refuses what no request path can carry
			}
			t.Fatalf("Path(%+v): %v", in, err)
		}
		req, err := http.NewRequest("GET", "http://example.com"+target, nil)
		if err != nil {
			t.Fatalf("Path(%+v) = %q, which is not a URL: %v", in, target, err)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("Path(%+v) = %q, which answered %d %s", in, target, rec.Code, rec.Body)
		}
		var got Trip
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, in) {
			t.Fatalf("%q\n got %+v\nwant %+v", target, got, in)
		}
	})
}
