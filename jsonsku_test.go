package mapget_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/AndyCross/mapget"
)

// The README says a constraint type holds in a JSON body too.
func TestConstraintTypeInJSONBody(t *testing.T) {
	mux := http.NewServeMux()
	type in struct {
		Body struct {
			SKU SKU `json:"sku"`
		} `body:"json"`
	}
	mapget.HandleAt(mapget.New(mux), "POST /products", func(_ context.Context, in in) (string, error) {
		return string(in.Body.SKU), nil
	})
	if res, body := do(t, mux, "POST", "/products", `{"sku":"TILE-0042"}`); res.StatusCode != 200 {
		t.Errorf("good: %d %s", res.StatusCode, body)
	}
	if res, body := do(t, mux, "POST", "/products", `{"sku":"nope"}`); res.StatusCode != 400 {
		t.Errorf("bad: %d %s", res.StatusCode, body)
	}
}
