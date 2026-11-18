package mapget_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"

	"github.com/AndyCross/mapget"
)

// SKU is a product code such as TILE-0042. Its UnmarshalText is the
// constraint: anything else is not a SKU.
type SKU string

var skuPattern = regexp.MustCompile(`^[A-Z]{2,6}-[0-9]{4}$`)

func (s *SKU) UnmarshalText(text []byte) error {
	if !skuPattern.Match(text) {
		return errors.New("not a SKU")
	}
	*s = SKU(text)
	return nil
}

type GetProduct struct {
	mapget.Route `route:"GET /products/{sku}"`
	SKU          SKU  `path:"sku"`
	Like         *SKU `query:"like"`
}

// A regular expression constraint is a type with an UnmarshalText method.
// In the path a value that fails is a 404, as {sku:regex(...)} is in ASP.NET
// Core. In the query string it is a 400 that names the field.
func Example_regexConstraint() {
	mux := http.NewServeMux()
	app := mapget.New(mux)
	mapget.Handle(app, func(_ context.Context, in GetProduct) (string, error) {
		return "found " + string(in.SKU), nil
	})

	for _, target := range []string{"/products/TILE-0042", "/products/tile-42", "/products/TILE-0042?like=nope"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
		fmt.Printf("%s\n  %d %s\n", target, rec.Code, rec.Body)
	}
	fmt.Println(app.Path(GetProduct{SKU: "TILE-0042"}))

	// Output:
	// /products/TILE-0042
	//   200 "found TILE-0042"
	//
	// /products/tile-42
	//   404 {"status":404,"title":"Not Found"}
	// /products/TILE-0042?like=nope
	//   400 {"errors":{"like":["must be a valid SKU"]},"status":400,"title":"One or more validation errors occurred."}
	// /products/TILE-0042 <nil>
}
