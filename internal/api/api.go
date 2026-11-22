// Package api exposes the pack calculator over HTTP.
//
//	GET  /healthz                  liveness check
//	GET  /api/v1/packs             current pack sizes
//	PUT  /api/v1/packs             replace pack sizes   {"packSizes":[250,500]}
//	POST /api/v1/calculate         calculate packs      {"items":12001}
//	GET  /api/v1/calculate?items=N calculate packs (handy for curl / browsers)
//	GET  /                         web UI
package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pack-calculator/internal/packing"
	"pack-calculator/internal/store"
)

const maxBodyBytes = 64 << 10

// Server holds the dependencies of the HTTP handlers.
type Server struct {
	store  *store.PackStore
	logger *slog.Logger
}

// NewHandler builds the full HTTP handler, including middleware. ui may be nil
// to serve the API only.
func NewHandler(st *store.PackStore, logger *slog.Logger, ui fs.FS) http.Handler {
	s := &Server{store: st, logger: logger}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/v1/packs", s.getPacks)
	mux.HandleFunc("PUT /api/v1/packs", s.putPacks)
	mux.HandleFunc("POST /api/v1/calculate", s.postCalculate)
	mux.HandleFunc("GET /api/v1/calculate", s.getCalculate)
	// "GET /" is method-specific so the mux still answers 405 (with an Allow
	// header) for wrong methods on known routes, e.g. DELETE /api/v1/packs.
	var files http.Handler
	if ui != nil {
		files = http.FileServerFS(ui)
	}
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if files == nil || strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		files.ServeHTTP(w, r)
	})

	return s.recoverer(s.logRequests(securityHeaders(mux)))
}

// PacksBody is the request and response body for /api/v1/packs.
type PacksBody struct {
	PackSizes []int `json:"packSizes"`
}

// CalculateRequest is the request body for POST /api/v1/calculate.
// PackSizes optionally overrides the configured sizes for this request only.
type CalculateRequest struct {
	Items     *int64 `json:"items"`
	PackSizes []int  `json:"packSizes,omitempty"`
}

type errorBody struct {
	Error string `json:"error"`
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) getPacks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, PacksBody{PackSizes: s.store.Get()})
}

func (s *Server) putPacks(w http.ResponseWriter, r *http.Request) {
	var body PacksBody
	if !decodeJSON(w, r, &body) {
		return
	}
	sizes, err := s.store.Set(body.PackSizes)
	if err != nil {
		s.writeCalcError(w, err)
		return
	}
	s.logger.Info("pack sizes updated", "packSizes", sizes)
	writeJSON(w, http.StatusOK, PacksBody{PackSizes: sizes})
}

func (s *Server) postCalculate(w http.ResponseWriter, r *http.Request) {
	var req CalculateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Items == nil {
		writeError(w, http.StatusBadRequest, `"items" is required`)
		return
	}
	sizes := req.PackSizes
	if len(sizes) == 0 {
		sizes = s.store.Get()
	}
	s.calculate(w, *req.Items, sizes)
}

func (s *Server) getCalculate(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("items")
	if raw == "" {
		writeError(w, http.StatusBadRequest, `query parameter "items" is required`)
		return
	}
	items, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, `"items" must be a whole number`)
		return
	}
	s.calculate(w, items, s.store.Get())
}

func (s *Server) calculate(w http.ResponseWriter, items int64, sizes []int) {
	res, err := packing.Calculate(items, sizes)
	if err != nil {
		s.writeCalcError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) writeCalcError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, packing.ErrNoPackSizes),
		errors.Is(err, packing.ErrInvalidPackSize),
		errors.Is(err, packing.ErrTooManyPackSizes),
		errors.Is(err, packing.ErrInvalidOrder):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, packing.ErrTooComplex):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		s.logger.Error("internal error", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

// decodeJSON reads a single JSON object, rejecting unknown fields and
// oversized bodies. It writes the error response itself and reports success.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		var typeErr *json.UnmarshalTypeError
		switch {
		case errors.As(err, &maxErr):
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		case errors.Is(err, io.EOF):
			writeError(w, http.StatusBadRequest, "request body is empty")
		case errors.As(err, &typeErr):
			writeError(w, http.StatusBadRequest, `field "`+typeErr.Field+`" must be a whole number`)
		default:
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		}
		return false
	}
	if dec.More() {
		writeError(w, http.StatusBadRequest, "request body must contain a single JSON object")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// --- middleware ---

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start).String(),
		)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.logger.Error("panic", "value", v, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' data:")
		next.ServeHTTP(w, r)
	})
}
