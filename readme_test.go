package mapget_test

import (
	"context"
	"net/http"
	"net/netip"
	"testing"

	"github.com/AndyCross/mapget"
)

type OrderID int

func TestNamedAndTextTypesBind(t *testing.T) {
	mux := http.NewServeMux()
	type in struct {
		ID   OrderID    `path:"id"`
		From netip.Addr `query:"from"`
	}
	mapget.HandleAt(mapget.New(mux), "GET /o/{id}", func(_ context.Context, in in) (string, error) {
		return in.From.String(), nil
	})
	if res, body := do(t, mux, "GET", "/o/7?from=10.0.0.1", ""); res.StatusCode != 200 || body != "\"10.0.0.1\"\n" {
		t.Errorf("%d %q", res.StatusCode, body)
	}
	if res, body := do(t, mux, "GET", "/o/7?from=nowhere", ""); res.StatusCode != 400 {
		t.Errorf("%d %q", res.StatusCode, body)
	}
}
