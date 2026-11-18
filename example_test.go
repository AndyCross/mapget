package mapget_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/AndyCross/mapget"
)

type Order struct {
	ID    int      `json:"id"`
	Lines []string `json:"lines,omitempty"`
}

// The request type carries its route. The field types are the constraints.
type GetOrder struct {
	mapget.Route `route:"GET /orders/{id}"`
	ID           int      `path:"id"`
	Expand       []string `query:"expand"`
}

func getOrder(_ context.Context, in GetOrder) (Order, error) {
	if in.ID > 100 {
		return Order{}, mapget.NotFound("there is no order with that id")
	}
	o := Order{ID: in.ID}
	if len(in.Expand) > 0 {
		o.Lines = []string{"12 x TILE-4"}
	}
	return o, nil
}

func Example() {
	mux := http.NewServeMux()
	app := mapget.New(mux)
	mapget.Handle(app, getOrder)

	get := func(target string) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
		fmt.Printf("%d %s", rec.Code, rec.Body)
		if rec.Code >= 400 {
			fmt.Println()
		}
	}
	get("/orders/42?expand=lines")
	get("/orders/abc") // not an int, so not a route
	get("/orders/101")

	// Output:
	// 200 {"id":42,"lines":["12 x TILE-4"]}
	// 404 {"status":404,"title":"Not Found"}
	// 404 {"detail":"there is no order with that id","status":404,"title":"Not Found"}
}

func ExampleRouter_Path() {
	app := mapget.New(http.NewServeMux())
	api := app.Group("/api/v1")
	mapget.Handle(api, getOrder)

	fmt.Println(app.Path(GetOrder{ID: 42}))
	fmt.Println(app.Path(GetOrder{ID: 42, Expand: []string{"lines", "customer"}}))

	// Output:
	// /api/v1/orders/42 <nil>
	// /api/v1/orders/42?expand=lines&expand=customer <nil>
}

func ExampleRouter_WriteRoutes() {
	app := mapget.New(http.NewServeMux())
	api := app.Group("/api/v1")
	mapget.Handle(api, getOrder)
	app.HandleFunc("GET /healthz", func(http.ResponseWriter, *http.Request) {})

	app.WriteRoutes(os.Stdout)

	// Output:
	// GET  /api/v1/orders/{id}  mapget_test.GetOrder  mapget_test.Order
	// GET  /healthz             -                     -
}

func ExampleHandleAt() {
	mux := http.NewServeMux()
	app := mapget.New(mux)

	// The pattern at the call, as with ASP.NET Core's MapGet.
	type in struct {
		Name string `path:"name"`
		Loud bool   `query:"loud"`
	}
	mapget.HandleAt(app, "GET /hello/{name}", func(_ context.Context, in in) (string, error) {
		if in.Loud {
			return "HELLO, " + in.Name, nil
		}
		return "hello, " + in.Name, nil
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/hello/ada?loud=true", nil))
	fmt.Print(rec.Body)

	// Output:
	// "HELLO, ada"
}

func ExampleFieldErrors() {
	mux := http.NewServeMux()
	app := mapget.New(mux)

	type in struct {
		Page int `query:"page" default:"1"`
		Size int `query:"size,required"`
	}
	mapget.HandleAt(app, "GET /tiles", func(_ context.Context, in in) (int, error) { return in.Page * in.Size, nil })

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/tiles?page=two", nil))
	fmt.Println(rec.Code, rec.Header().Get("Content-Type"))
	fmt.Println(rec.Body)

	// Output:
	// 400 application/problem+json
	// {"errors":{"page":["must be an integer"],"size":["is required"]},"status":400,"title":"One or more validation errors occurred."}
}
