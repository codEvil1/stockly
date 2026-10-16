package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/codEvil1/stockly/internal/orders/app"
	"github.com/codEvil1/stockly/internal/orders/domain"
	"github.com/codEvil1/stockly/internal/orders/infra/httpapi"
)

type memRepo struct {
	mu     sync.Mutex
	orders map[string]*domain.Order
	keys   map[string]string
}

func newMemRepo() *memRepo {
	return &memRepo{orders: map[string]*domain.Order{}, keys: map[string]string{}}
}

func clone(o *domain.Order) *domain.Order {
	return domain.Rehydrate(o.ID, o.CustomerID, o.Status, append([]domain.Item(nil), o.Items...), o.CreatedAt)
}

func (m *memRepo) Insert(_ context.Context, o *domain.Order, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key != "" {
		if _, ok := m.keys[key]; ok {
			return domain.ErrDuplicateKey
		}
		m.keys[key] = o.ID
	}
	m.orders[o.ID] = clone(o)
	return nil
}

func (m *memRepo) Update(_ context.Context, o *domain.Order, from domain.Status) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.orders[o.ID]
	if !ok {
		return domain.ErrNotFound
	}
	if cur.Status != from {
		return domain.ErrInvalidTransition
	}
	cur.Status = o.Status
	return nil
}

func (m *memRepo) FindByID(_ context.Context, id string) (*domain.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o, ok := m.orders[id]; ok {
		return clone(o), nil
	}
	return nil, domain.ErrNotFound
}

func (m *memRepo) FindByIdempotencyKey(ctx context.Context, key string) (*domain.Order, error) {
	m.mu.Lock()
	id, ok := m.keys[key]
	m.mu.Unlock()
	if !ok {
		return nil, domain.ErrNotFound
	}
	return m.FindByID(ctx, id)
}

func (m *memRepo) List(_ context.Context, f domain.ListFilter) ([]*domain.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []*domain.Order
	for _, o := range m.orders {
		if f.CustomerID == "" || o.CustomerID == f.CustomerID {
			all = append(all, clone(o))
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	if f.Offset >= len(all) {
		return nil, nil
	}
	all = all[f.Offset:]
	if len(all) > f.Limit {
		all = all[:f.Limit]
	}
	return all, nil
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := app.NewService(newMemRepo(), app.WithClock(func() time.Time { return time.Unix(0, 0).UTC() }))
	r := chi.NewRouter()
	httpapi.DocsRoutes(r)
	httpapi.NewHandler(svc, logger).Routes(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, method, url, body string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

type order struct {
	ID         string `json:"id"`
	CustomerID string `json:"customer_id"`
	Status     string `json:"status"`
	TotalCents int64  `json:"total_cents"`
	Items      []struct {
		SKU string `json:"sku"`
	} `json:"items"`
}

const validBody = `{"customer_id":"cust-1","items":[{"sku":"A","quantity":2,"price_cents":1000},{"sku":"B","quantity":1,"price_cents":550}]}`

func createOrder(t *testing.T, srv *httptest.Server, body string, headers map[string]string) (*http.Response, order) {
	t.Helper()
	resp, b := do(t, http.MethodPost, srv.URL+"/orders", body, headers)
	var o order
	_ = json.Unmarshal(b, &o)
	return resp, o
}

func TestCreateOrder(t *testing.T) {
	srv := newServer(t)
	resp, o := createOrder(t, srv, validBody, nil)

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	if o.TotalCents != 2550 || o.Status != "CREATED" || len(o.Items) != 2 {
		t.Errorf("unexpected body: %+v", o)
	}
	if loc := resp.Header.Get("Location"); loc != "/orders/"+o.ID {
		t.Errorf("unexpected Location header %q", loc)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("unexpected Content-Type %q", ct)
	}
}

func TestCreateOrderRejectsBadInput(t *testing.T) {
	srv := newServer(t)
	cases := map[string]string{
		"malformed json":       `{`,
		"unknown field":        `{"customer_id":"c","items":[{"sku":"A","quantity":1,"price_cents":1}],"x":1}`,
		"trailing data":        validBody + `{}`,
		"no items":             `{"customer_id":"c","items":[]}`,
		"invalid item":         `{"customer_id":"c","items":[{"sku":"A","quantity":0,"price_cents":1}]}`,
		"missing customer":     `{"items":[{"sku":"A","quantity":1,"price_cents":1}]}`,
		"customer too long":    `{"customer_id":"` + strings.Repeat("c", 37) + `","items":[{"sku":"A","quantity":1,"price_cents":1}]}`,
		"duplicate sku":        `{"customer_id":"c","items":[{"sku":"A","quantity":1,"price_cents":1},{"sku":"A","quantity":1,"price_cents":1}]}`, // antes virava 500
		"body with wrong type": `{"customer_id":1,"items":[]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			resp, b := do(t, http.MethodPost, srv.URL+"/orders", body, nil)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (%s)", resp.StatusCode, b)
			}
			var e map[string]string
			if err := json.Unmarshal(b, &e); err != nil || e["error"] == "" {
				t.Errorf("expected an error body, got %s", b)
			}
		})
	}
}

func TestCreateOrderIsIdempotent(t *testing.T) {
	srv := newServer(t)
	h := map[string]string{"Idempotency-Key": "abc-123"}

	r1, o1 := createOrder(t, srv, validBody, h)
	r2, o2 := createOrder(t, srv, validBody, h)

	if r1.StatusCode != http.StatusCreated || r2.StatusCode != http.StatusOK {
		t.Fatalf("expected 201 then 200, got %d then %d", r1.StatusCode, r2.StatusCode)
	}
	if o1.ID != o2.ID {
		t.Errorf("replay must return the same order: %s vs %s", o1.ID, o2.ID)
	}
	if r2.Header.Get("Idempotent-Replay") != "true" {
		t.Errorf("replay must be flagged with Idempotent-Replay: true")
	}

	_, o3 := createOrder(t, srv, validBody, map[string]string{"Idempotency-Key": "other"})
	if o3.ID == o1.ID {
		t.Errorf("a different key must create a different order")
	}

	long := map[string]string{"Idempotency-Key": strings.Repeat("k", 65)}
	if resp, _ := createOrder(t, srv, validBody, long); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for oversized key, got %d", resp.StatusCode)
	}
}

func TestGetOrder(t *testing.T) {
	srv := newServer(t)
	_, created := createOrder(t, srv, validBody, nil)

	resp, b := do(t, http.MethodGet, srv.URL+"/orders/"+created.ID, "", nil)
	var got order
	_ = json.Unmarshal(b, &got)
	if resp.StatusCode != http.StatusOK || got.ID != created.ID {
		t.Fatalf("expected the created order, got %d %s", resp.StatusCode, b)
	}

	if resp, _ := do(t, http.MethodGet, srv.URL+"/orders/11111111-1111-4111-8111-111111111111", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	if resp, _ := do(t, http.MethodGet, srv.URL+"/orders/not-a-uuid", "", nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for malformed id, got %d", resp.StatusCode)
	}
}

func TestCompleteAndCancel(t *testing.T) {
	srv := newServer(t)
	_, o := createOrder(t, srv, validBody, nil)

	resp, b := do(t, http.MethodPost, srv.URL+"/orders/"+o.ID+"/complete", "", nil)
	var done order
	_ = json.Unmarshal(b, &done)
	if resp.StatusCode != http.StatusOK || done.Status != "COMPLETED" {
		t.Fatalf("complete: %d %s", resp.StatusCode, b)
	}

	for _, action := range []string{"complete", "cancel"} {
		if resp, _ := do(t, http.MethodPost, srv.URL+"/orders/"+o.ID+"/"+action, "", nil); resp.StatusCode != http.StatusConflict {
			t.Errorf("%s on a completed order: expected 409, got %d", action, resp.StatusCode)
		}
	}

	_, o2 := createOrder(t, srv, validBody, nil)
	resp, b = do(t, http.MethodPost, srv.URL+"/orders/"+o2.ID+"/cancel", "", nil)
	var canceled order
	_ = json.Unmarshal(b, &canceled)
	if resp.StatusCode != http.StatusOK || canceled.Status != "CANCELED" {
		t.Fatalf("cancel: %d %s", resp.StatusCode, b)
	}

	if resp, _ := do(t, http.MethodPost, srv.URL+"/orders/11111111-1111-4111-8111-111111111111/cancel", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestListOrders(t *testing.T) {
	srv := newServer(t)
	for i := 0; i < 3; i++ {
		createOrder(t, srv, fmt.Sprintf(`{"customer_id":"cust-%d","items":[{"sku":"A","quantity":1,"price_cents":1}]}`, i%2), nil)
	}

	type listResp struct {
		Items []order `json:"items"`
	}
	get := func(query string) (int, listResp) {
		resp, b := do(t, http.MethodGet, srv.URL+"/orders"+query, "", nil)
		var l listResp
		_ = json.Unmarshal(b, &l)
		return resp.StatusCode, l
	}

	if code, l := get(""); code != 200 || len(l.Items) != 3 {
		t.Errorf("expected 3 orders, got %d (status %d)", len(l.Items), code)
	}
	if code, l := get("?customer_id=cust-0"); code != 200 || len(l.Items) != 2 {
		t.Errorf("expected 2 orders for cust-0, got %d (status %d)", len(l.Items), code)
	}
	if code, l := get("?limit=1&offset=1"); code != 200 || len(l.Items) != 1 {
		t.Errorf("expected 1 order on the page, got %d (status %d)", len(l.Items), code)
	}
	if code, l := get("?customer_id=nobody"); code != 200 || l.Items == nil || len(l.Items) != 0 {
		t.Errorf("an empty list must be [] and not null, got %+v", l)
	}
	for _, q := range []string{"?limit=abc", "?limit=-1", "?offset=x"} {
		if code, _ := get(q); code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", q, code)
		}
	}
}

func TestDocsAreServed(t *testing.T) {
	srv := newServer(t)
	resp, b := do(t, http.MethodGet, srv.URL+"/openapi.yaml", "", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(b), "openapi: 3.0.3") {
		t.Errorf("openapi spec not served correctly: %d", resp.StatusCode)
	}
	if resp, _ := do(t, http.MethodGet, srv.URL+"/docs", "", nil); resp.StatusCode != 200 {
		t.Errorf("docs page not served: %d", resp.StatusCode)
	}
}
