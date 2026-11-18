// Command example is a small orders API on the standard library's ServeMux,
// with mapget doing what the mux leaves to you.
//
//	go run ./example
//	curl -s localhost:8080/api/orders/1
//	curl -s 'localhost:8080/api/orders?status=open&limit=abc'
//	curl -s localhost:8080/api/orders/abc
//	curl -s -X POST localhost:8080/api/orders -d '{"customer":"Ada","lines":[{"sku":"TILE-4","qty":12}]}'
//	curl -s localhost:8080/openapi.json
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/AndyCross/mapget"
)

type Line struct {
	SKU string `json:"sku"`
	Qty int    `json:"qty" doc:"How many, at least one."`
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
	Status       string `query:"status" doc:"Only orders in this state."`
	Limit        int    `query:"limit" default:"20"`
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
	Customer string `json:"customer"`
	Lines    []Line `json:"lines"`
}

func (c CreateOrder) Validate() error {
	errs := mapget.FieldErrors{}
	if c.Body.Customer == "" {
		errs.Add("customer", "is required")
	}
	if len(c.Body.Lines) == 0 {
		errs.Add("lines", "must have at least one line")
	}
	for _, l := range c.Body.Lines {
		if l.Qty < 1 {
			errs.Add("lines", "qty must be at least 1")
			break
		}
	}
	if len(errs) > 0 {
		return errs
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

	app.Handle("GET /openapi.json", app.OpenAPI(mapget.Info{Title: "Orders", Version: "1.0.0"}))
	app.WriteRoutes(os.Stdout)

	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
