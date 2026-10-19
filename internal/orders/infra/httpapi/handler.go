package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/codEvil1/stockly/internal/orders/app"
	"github.com/codEvil1/stockly/internal/orders/domain"
)

const (
	maxBodyBytes         = 1 << 20
	maxCustomerIDLen     = 36
	idempotencyKeyHeader = "Idempotency-Key"
	replayHeader         = "Idempotent-Replay"
)

type Handler struct {
	svc    *app.Service
	logger *slog.Logger
}

func NewHandler(svc *app.Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{svc: svc, logger: logger}
}

func (h *Handler) Routes(r chi.Router) {
	r.Route("/orders", func(r chi.Router) {
		r.Post("/", h.create)
		r.Get("/", h.list)
		r.Get("/{id}", h.get)
		r.Post("/{id}/complete", h.complete)
		r.Post("/{id}/cancel", h.cancel)
	})
}

type itemDTO struct {
	SKU        string `json:"sku"`
	Quantity   int    `json:"quantity"`
	PriceCents int64  `json:"price_cents"`
}

type createReq struct {
	CustomerID string    `json:"customer_id"`
	Items      []itemDTO `json:"items"`
}

type orderResp struct {
	ID         string    `json:"id"`
	CustomerID string    `json:"customer_id"`
	Status     string    `json:"status"`
	Items      []itemDTO `json:"items"`
	TotalCents int64     `json:"total_cents"`
	CreatedAt  time.Time `json:"created_at"`
}

type listResp struct {
	Items []orderResp `json:"items"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req createReq
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid payload"))
		return
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid payload"))
		return
	}

	items := make([]domain.Item, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, domain.Item{SKU: it.SKU, Quantity: it.Quantity, PriceCents: it.PriceCents})
	}

	o, created, err := h.svc.Create(r.Context(), app.CreateInput{
		CustomerID:     req.CustomerID,
		Items:          items,
		IdempotencyKey: r.Header.Get(idempotencyKeyHeader),
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if !created {
		w.Header().Set(replayHeader, "true")
		writeJSON(w, http.StatusOK, toResp(o))
		return
	}
	w.Header().Set("Location", "/orders/"+o.ID)
	writeJSON(w, http.StatusCreated, toResp(o))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	o, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toResp(o))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, ok := parseNonNegInt(w, q.Get("limit"), "limit")
	if !ok {
		return
	}
	if limit > app.MaxListLimit {
		writeJSON(w, http.StatusBadRequest, errorBody("limit must be at most 100"))
		return
	}
	offset, ok := parseNonNegInt(w, q.Get("offset"), "offset")
	if !ok {
		return
	}
	customerID := strings.TrimSpace(q.Get("customer_id"))
	if len(customerID) > maxCustomerIDLen || utf8.RuneCountInString(customerID) > maxCustomerIDLen {
		writeJSON(w, http.StatusBadRequest, errorBody("customer_id must have at most 36 chars"))
		return
	}

	orders, err := h.svc.List(r.Context(), app.ListInput{
		CustomerID: customerID,
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	resp := listResp{Items: make([]orderResp, 0, len(orders))}
	for _, o := range orders {
		resp.Items = append(resp.Items, toResp(o))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) complete(w http.ResponseWriter, r *http.Request) {
	o, err := h.svc.Complete(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toResp(o))
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	o, err := h.svc.Cancel(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toResp(o))
}

func toResp(o *domain.Order) orderResp {
	items := make([]itemDTO, 0, len(o.Items))
	for _, it := range o.Items {
		items = append(items, itemDTO{it.SKU, it.Quantity, it.PriceCents})
	}
	return orderResp{o.ID, o.CustomerID, string(o.Status), items, o.Total(), o.CreatedAt}
}

func parseNonNegInt(w http.ResponseWriter, raw, name string) (int, bool) {
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		writeJSON(w, http.StatusBadRequest, errorBody(name+" must be a non-negative integer"))
		return 0, false
	}
	return n, true
}

func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ve domain.ValidationError
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusBadRequest, errorBody(err.Error()))
	case errors.Is(err, domain.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errorBody(err.Error()))
	case errors.Is(err, domain.ErrInvalidTransition), errors.Is(err, domain.ErrIdempotencyMismatch):
		writeJSON(w, http.StatusConflict, errorBody(err.Error()))
	default:
		h.logger.ErrorContext(r.Context(), "internal error",
			"err", err,
			"request_id", middleware.GetReqID(r.Context()),
		)
		writeJSON(w, http.StatusInternalServerError, errorBody("internal error"))
	}
}

func errorBody(msg string) map[string]string { return map[string]string{"error": msg} }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
