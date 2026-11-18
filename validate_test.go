package mapget_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/AndyCross/mapget"
)

type Rated struct {
	Stars int    `json:"stars" minimum:"1" maximum:"5"`
	Note  string `json:"note,omitempty" minLength:"3"`
}

type NewTile struct {
	Name    string           `json:"name" minLength:"2" maxLength:"10"`
	Code    string           `json:"code" pattern:"^[A-Z]{2}-[0-9]+$"`
	Finish  string           `json:"finish" enum:"matt, gloss"`
	Size    int              `json:"size" enum:"10,15,20"`
	Weight  float64          `json:"weight" minimum:"0.5"`
	Colours []string         `json:"colours" minItems:"1" maxItems:"3" minLength:"3"`
	Ratings []Rated          `json:"ratings" maxItems:"2"`
	Best    *Rated           `json:"best"`
	Count   *int             `json:"count" minimum:"1"`
	ByShop  map[string]Rated `json:"byShop"`
	Rated                    // embedded: its fields are promoted
	Hidden  string           `json:"-" minLength:"99"`
}

type MakeNewTile struct {
	mapget.Route `route:"POST /shops/{shop}/tiles/{size}"`
	Shop         string   `path:"shop" pattern:"^[a-z]+$"`
	Size         int      `path:"size" minimum:"10" maximum:"20"`
	Page         int      `query:"page" default:"1" minimum:"1"`
	Sort         string   `query:"sort" enum:"name,size"`
	Tags         []string `query:"tag" maxItems:"2" maxLength:"4"`
	Limit        *int     `query:"limit" maximum:"100"`
	Key          string   `header:"X-Key" minLength:"8"`
	Body         NewTile  `body:"json"`
}

const goodTile = `{"name":"estrella","code":"ES-12","finish":"matt","size":15,"weight":1.2,
	"colours":["blue"],"ratings":[{"stars":4}],"stars":5}`

func TestValidationTags(t *testing.T) {
	mux := http.NewServeMux()
	mapget.Handle(mapget.New(mux), func(_ context.Context, in MakeNewTile) (int, error) { return in.Page, nil })

	errorsOf := func(target, body string, hdr ...string) (int, map[string][]string) {
		t.Helper()
		res, b := do(t, mux, "POST", target, body, hdr...)
		var p struct{ Errors map[string][]string }
		json.Unmarshal([]byte(b), &p)
		return res.StatusCode, p.Errors
	}

	if code, errs := errorsOf("/shops/triana/tiles/15", goodTile); code != 200 || errs != nil {
		t.Fatalf("good request: %d %v", code, errs)
	}
	// Absent optional parameters are not checked: no sort, no key, no limit above.

	// A path value outside its rules is not an address.
	for _, target := range []string{"/shops/Triana/tiles/15", "/shops/triana/tiles/9", "/shops/triana/tiles/21"} {
		if code, _ := errorsOf(target, goodTile); code != 404 {
			t.Errorf("%s: %d, want 404", target, code)
		}
	}

	code, errs := errorsOf("/shops/triana/tiles/15?page=0&sort=price&tag=a&tag=toolong&tag=c&limit=101", goodTile, "X-Key", "short")
	want := map[string][]string{
		"page":   {"must be at least 1"},
		"sort":   {"must be one of name, size"},
		"tag":    {"must have at most 2 items"},
		"tag[1]": {"must be at most 4 characters"},
		"limit":  {"must be at most 100"},
		"X-Key":  {"must be at least 8 characters"},
	}
	if code != 400 || !reflect.DeepEqual(errs, want) {
		t.Errorf("parameters: %d\n got %v\nwant %v", code, errs, want)
	}

	code, errs = errorsOf("/shops/triana/tiles/15", `{"name":"x","code":"es-12","finish":"satin","size":12,"weight":0.1,
		"colours":[],"ratings":[{"stars":0},{"stars":6,"note":"no"},{"stars":3}],
		"best":{"stars":9},"count":0,"byShop":{"triana":{"stars":7}},"stars":0}`)
	want = map[string][]string{
		"name":                {"must be at least 2 characters"},
		"code":                {"must match the pattern ^[A-Z]{2}-[0-9]+$"},
		"finish":              {"must be one of matt, gloss"},
		"size":                {"must be one of 10, 15, 20"},
		"weight":              {"must be at least 0.5"},
		"colours":             {"must have at least 1 item"},
		"ratings":             {"must have at most 2 items"},
		"ratings[0].stars":    {"must be at least 1"},
		"ratings[1].stars":    {"must be at most 5"},
		"ratings[1].note":     {"must be at least 3 characters"},
		"best.stars":          {"must be at most 5"},
		"count":               {"must be at least 1"},
		"byShop.triana.stars": {"must be at most 5"},
		"stars":               {"must be at least 1"},
	}
	if code != 400 || !reflect.DeepEqual(errs, want) {
		t.Errorf("body: %d\n got %v\nwant %v", code, errs, want)
	}

	code, errs = errorsOf("/shops/triana/tiles/15", `{"name":"estrellas del sur","code":"ES-12","finish":"matt","size":15,"weight":1,"colours":["azul","no"],"stars":1}`)
	want = map[string][]string{"name": {"must be at most 10 characters"}, "colours[1]": {"must be at least 3 characters"}}
	if code != 400 || !reflect.DeepEqual(errs, want) {
		t.Errorf("body 2: %d %v", code, errs)
	}
}

func TestValidationTagMistakesPanicAtRegistration(t *testing.T) {
	r := func() *mapget.Router { return mapget.New(http.NewServeMux()) }
	type minOnString struct {
		Q string `query:"q" minimum:"1"`
	}
	type lenOnInt struct {
		N int `query:"n" maxLength:"3"`
	}
	type badPattern struct {
		Q string `query:"q" pattern:"("`
	}
	type itemsOnScalar struct {
		Q string `query:"q" minItems:"1"`
	}
	type badEnum struct {
		N int `query:"n" enum:"1,two"`
	}
	type badDefault struct {
		N int `query:"n" default:"0" minimum:"1"`
	}
	type inBody struct {
		Body struct {
			Lines []struct {
				Qty string `json:"qty" minimum:"1"`
			} `json:"lines"`
		} `body:"json"`
	}
	mustPanic(t, "minimum on string", "minimum applies to numbers, not string", func() {
		mapget.HandleAt(r(), "GET /x", func(context.Context, minOnString) (int, error) { return 0, nil })
	})
	mustPanic(t, "maxLength on int", "maxLength applies to strings, not int", func() {
		mapget.HandleAt(r(), "GET /x", func(context.Context, lenOnInt) (int, error) { return 0, nil })
	})
	mustPanic(t, "bad pattern", "is not a regular expression", func() {
		mapget.HandleAt(r(), "GET /x", func(context.Context, badPattern) (int, error) { return 0, nil })
	})
	mustPanic(t, "minItems on scalar", "minItems applies to slices", func() {
		mapget.HandleAt(r(), "GET /x", func(context.Context, itemsOnScalar) (int, error) { return 0, nil })
	})
	mustPanic(t, "bad enum", `enum value "two" is not an integer`, func() {
		mapget.HandleAt(r(), "GET /x", func(context.Context, badEnum) (int, error) { return 0, nil })
	})
	mustPanic(t, "default breaks rule", `default "0" must be at least 1`, func() {
		mapget.HandleAt(r(), "GET /x", func(context.Context, badDefault) (int, error) { return 0, nil })
	})
	mustPanic(t, "in body", "Qty: minimum applies to numbers, not string", func() {
		mapget.HandleAt(r(), "POST /x", func(context.Context, inBody) (int, error) { return 0, nil })
	})
}

func TestValidationTagsInOpenAPI(t *testing.T) {
	r := mapget.New(http.NewServeMux())
	mapget.Handle(r, func(_ context.Context, in MakeNewTile) (int, error) { return 0, nil })
	b, _ := json.Marshal(r.OpenAPI(mapget.Info{Title: "t", Version: "1"}))
	d := decode[map[string]any](t, string(b))
	eq := func(got any, want string) {
		t.Helper()
		g, _ := json.Marshal(got)
		if string(g) != want {
			t.Errorf("got  %s\nwant %s", g, want)
		}
	}
	params := at(t, d, "paths", "/shops/{shop}/tiles/{size}", "post", "parameters").([]any)
	schema := func(i int) any { return params[i].(map[string]any)["schema"] }
	eq(schema(0), `{"pattern":"^[a-z]+$","type":"string"}`)
	eq(schema(1), `{"format":"int64","maximum":20,"minimum":10,"type":"integer"}`)
	eq(schema(2), `{"default":1,"format":"int64","minimum":1,"type":"integer"}`)
	eq(schema(3), `{"enum":["name","size"],"type":"string"}`)
	eq(schema(4), `{"items":{"maxLength":4,"type":"string"},"maxItems":2,"type":"array"}`)
	props := at(t, d, "components", "schemas", "NewTile", "properties")
	eq(at(t, props, "name"), `{"maxLength":10,"minLength":2,"type":"string"}`)
	eq(at(t, props, "size"), `{"enum":[10,15,20],"format":"int64","type":"integer"}`)
	eq(at(t, props, "colours"), `{"items":{"minLength":3,"type":"string"},"maxItems":3,"minItems":1,"type":"array"}`)
	eq(at(t, props, "ratings"), `{"items":{"$ref":"#/components/schemas/Rated"},"maxItems":2,"type":"array"}`)
	eq(at(t, d, "components", "schemas", "Rated", "properties", "stars"), `{"format":"int64","maximum":5,"minimum":1,"type":"integer"}`)
}
