// Command example is a small orders API on the standard library's ServeMux,
// with mapget doing what the mux leaves to you.
//
//	go run ./example
//	curl -s localhost:8080/api/orders/1
//	curl -s 'localhost:8080/api/orders?status=open&limit=abc'
//	curl -s localhost:8080/api/orders/abc
//	curl -s -X POST localhost:8080/api/orders -d '{"customer":"Ada","lines":[{"sku":"TILE-4","qty":12}]}'
//	curl -s localhost:8080/openapi.json
//
// Then open http://localhost:8080/docs for Swagger UI over the generated
// document.
package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/AndyCross/mapget"
)

type Line struct {
	SKU string `json:"sku" pattern:"^[A-Z]{2,6}-[0-9]+$"`
	Qty int    `json:"qty" minimum:"1" doc:"How many."`
}

type Order struct {
	ID       int       `json:"id"`
	Customer string    `json:"customer"`
	Status   string    `json:"status" doc:"open or shipped."`
	Lines    []Line    `json:"lines"`
	Placed   time.Time `json:"placed"`
	Self     string    `json:"self" doc:"This order's address."`
}

// The request types. Each carries its route, and its fields say where every
// value comes from.

type ListOrders struct {
	mapget.Route `route:"GET /orders" summary:"List orders" tags:"orders"`
	Status       string `query:"status" enum:"open,shipped" doc:"Only orders in this state."`
	Limit        int    `query:"limit" default:"20" minimum:"1" maximum:"100"`
}

type GetOrder struct {
	mapget.Route `route:"GET /orders/{id}" summary:"Fetch one order" tags:"orders"`
	ID           int `path:"id"`
}

type CreateOrder struct {
	mapget.Route `route:"POST /orders" summary:"Place an order" tags:"orders"`
	Body         NewOrder `body:"json"`
}

type NewOrder struct {
	Customer string `json:"customer" minLength:"1" maxLength:"80"`
	Lines    []Line `json:"lines" minItems:"1" maxItems:"50"`
}

// Validate is for rules a tag cannot state, such as one that compares two
// values. The tags have already been checked when it runs.
func (c CreateOrder) Validate() error {
	seen := map[string]bool{}
	for _, l := range c.Body.Lines {
		if seen[l.SKU] {
			return mapget.FieldErrors{"lines": {"has " + l.SKU + " twice; put the quantities on one line"}}
		}
		seen[l.SKU] = true
	}
	return nil
}

type ShipOrder struct {
	mapget.Route `route:"POST /orders/{id}/ship" summary:"Mark an order shipped" tags:"orders"`
	ID           int `path:"id"`
}

type service struct {
	links *mapget.Router
	mu    sync.Mutex
	next  int
	byID  map[int]*Order
}

func (s *service) list(_ context.Context, in ListOrders) ([]Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Order{}
	for _, o := range s.byID {
		if in.Status == "" || o.Status == in.Status {
			out = append(out, *o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > in.Limit {
		out = out[:in.Limit]
	}
	return out, nil
}

func (s *service) get(_ context.Context, in GetOrder) (Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.byID[in.ID]
	if !ok {
		return Order{}, mapget.NotFound("there is no order with that id")
	}
	return *o, nil
}

func (s *service) create(_ context.Context, in CreateOrder) (mapget.Created[Order], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	o := &Order{ID: s.next, Customer: in.Body.Customer, Status: "open", Lines: in.Body.Lines, Placed: time.Now().UTC()}
	// The address comes from the same type the route is declared on.
	o.Self = s.links.MustPath(GetOrder{ID: o.ID})
	s.byID[o.ID] = o
	return mapget.Created[Order]{Location: o.Self, Body: *o}, nil
}

func (s *service) ship(_ context.Context, in ShipOrder) (mapget.NoContent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.byID[in.ID]
	switch {
	case !ok:
		return mapget.NoContent{}, mapget.NotFound("there is no order with that id")
	case o.Status == "shipped":
		return mapget.NoContent{}, mapget.Conflict("the order has already shipped")
	}
	o.Status = "shipped"
	return mapget.NoContent{}, nil
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

// swaggerUI serves Swagger UI from a CDN, pointed at the generated document.
func swaggerUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Orders API</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css">
</head>
<body>
<div id="ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"></script>
<script>SwaggerUIBundle({url: "/openapi.json", dom_id: "#ui", docExpansion: "list"})</script>
</body>
</html>`)
}

func main() {
	mux := http.NewServeMux()
	app := mapget.New(mux)
	api := app.Group("/api", logging)

	s := &service{links: api, byID: map[int]*Order{}}
	mapget.Handle(api, s.list)
	mapget.Handle(api, s.get)
	mapget.Handle(api, s.create)
	mapget.Handle(api, s.ship)

	s.create(context.Background(), CreateOrder{Body: NewOrder{Customer: "Grace", Lines: []Line{{SKU: "TILE-9", Qty: 40}}}})

	app.Handle("GET /openapi.json", app.OpenAPI(mapget.Info{Title: "Orders", Version: "1.0.0",
		Description: "The mapget example. Every operation here was generated from Go types and struct tags."}))
	app.HandleFunc("GET /docs", swaggerUI)
	app.WriteRoutes(os.Stdout)

	log.Println("listening on :8080, docs at http://localhost:8080/docs")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
