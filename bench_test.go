package mapget_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/AndyCross/mapget"
)

type benchOut struct {
	ID    int    `json:"id"`
	Page  int    `json:"page"`
	Query string `json:"q"`
}

type benchIn struct {
	mapget.Route `route:"GET /orders/{id}"`
	ID           int    `path:"id"`
	Page         int    `query:"page" default:"1"`
	Q            string `query:"q"`
}

// byHand is the same endpoint written against net/http directly.
func byHand(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	page := 1
	if s := q.Get("page"); s != "" {
		if page, err = strconv.Atoi(s); err != nil {
			http.Error(w, "page must be an integer", http.StatusBadRequest)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(benchOut{ID: id, Page: page, Query: q.Get("q")})
}

type discard struct{ h http.Header }

func (d *discard) Header() http.Header         { return d.h }
func (d *discard) Write(b []byte) (int, error) { return len(b), nil }
func (d *discard) WriteHeader(int)             {}

func run(b *testing.B, mux *http.ServeMux) {
	req := httptest.NewRequest("GET", "/orders/1234?page=3&q=tiles", nil)
	w := &discard{h: http.Header{}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mux.ServeHTTP(w, req)
	}
}

func BenchmarkByHand(b *testing.B) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", byHand)
	run(b, mux)
}

func BenchmarkMapget(b *testing.B) {
	mux := http.NewServeMux()
	mapget.Handle(mapget.New(mux), func(_ context.Context, in benchIn) (benchOut, error) {
		return benchOut{ID: in.ID, Page: in.Page, Query: in.Q}, nil
	})
	run(b, mux)
}

func BenchmarkPath(b *testing.B) {
	r := mapget.New(http.NewServeMux())
	mapget.Handle(r, func(_ context.Context, in benchIn) (benchOut, error) { return benchOut{}, nil })
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r.Path(benchIn{ID: i, Page: 2, Q: "tiles"})
	}
}
