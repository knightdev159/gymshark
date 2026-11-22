package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"pack-calculator/internal/packing"
	"pack-calculator/internal/store"
)

// testServer drives the full handler (routing + middleware) in memory with
// httptest.NewRecorder, so the tests are fast and need no network sockets.
type testServer struct {
	handler http.Handler
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	st, err := store.New([]int{250, 500, 1000, 2000, 5000}, "")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{"index.html": {Data: []byte("<h1>ui</h1>")}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &testServer{handler: NewHandler(st, logger, ui)}
}

func (s *testServer) do(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestCalculate_POST(t *testing.T) {
	srv := newTestServer(t)
	status, body := srv.do(t, http.MethodPost, "/api/v1/calculate", `{"items":12001}`)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	var res packing.Result
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatal(err)
	}
	want := []packing.Pack{{Size: 5000, Quantity: 2}, {Size: 2000, Quantity: 1}, {Size: 250, Quantity: 1}}
	if !reflect.DeepEqual(res.Packs, want) || res.TotalItems != 12250 || res.TotalPacks != 4 || res.Surplus != 249 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestCalculate_GET(t *testing.T) {
	srv := newTestServer(t)
	status, body := srv.do(t, http.MethodGet, "/api/v1/calculate?items=251", "")
	if status != http.StatusOK || !strings.Contains(body, `"packs":[{"size":500,"quantity":1}]`) {
		t.Fatalf("status %d: %s", status, body)
	}
}

func TestCalculate_PackSizeOverride(t *testing.T) {
	srv := newTestServer(t)
	status, body := srv.do(t, http.MethodPost, "/api/v1/calculate",
		`{"items":500000,"packSizes":[23,31,53]}`)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	if !strings.Contains(body, `"packs":[{"size":53,"quantity":9429},{"size":31,"quantity":7},{"size":23,"quantity":2}]`) {
		t.Fatalf("unexpected body: %s", body)
	}
	// The override must not change the stored sizes.
	_, body = srv.do(t, http.MethodGet, "/api/v1/packs", "")
	if !strings.Contains(body, `[250,500,1000,2000,5000]`) {
		t.Fatalf("stored sizes changed: %s", body)
	}
}

func TestPacks_UpdateThenCalculate(t *testing.T) {
	srv := newTestServer(t)

	status, body := srv.do(t, http.MethodPut, "/api/v1/packs", `{"packSizes":[53,31,23,31]}`)
	if status != http.StatusOK || strings.TrimSpace(body) != `{"packSizes":[23,31,53]}` {
		t.Fatalf("PUT status %d: %s", status, body)
	}

	status, body = srv.do(t, http.MethodGet, "/api/v1/packs", "")
	if status != http.StatusOK || strings.TrimSpace(body) != `{"packSizes":[23,31,53]}` {
		t.Fatalf("GET status %d: %s", status, body)
	}

	status, body = srv.do(t, http.MethodPost, "/api/v1/calculate", `{"items":500000}`)
	if status != http.StatusOK || !strings.Contains(body, `"totalItems":500000`) {
		t.Fatalf("calculate status %d: %s", status, body)
	}
}

func TestErrors(t *testing.T) {
	srv := newTestServer(t)
	tests := []struct {
		name, method, path, body string
		status                   int
		contains                 string
	}{
		{"negative items", "POST", "/api/v1/calculate", `{"items":-5}`, 400, "invalid order"},
		{"missing items", "POST", "/api/v1/calculate", `{}`, 400, `\"items\" is required`},
		{"fractional items", "POST", "/api/v1/calculate", `{"items":1.5}`, 400, "whole number"},
		{"string items", "POST", "/api/v1/calculate", `{"items":"ten"}`, 400, "whole number"},
		{"unknown field", "POST", "/api/v1/calculate", `{"items":1,"foo":2}`, 400, "unknown field"},
		{"empty body", "POST", "/api/v1/calculate", ``, 400, "empty"},
		{"malformed json", "POST", "/api/v1/calculate", `{"items":`, 400, "invalid JSON"},
		{"two objects", "POST", "/api/v1/calculate", `{"items":1}{"items":2}`, 400, "single JSON object"},
		{"too big body", "POST", "/api/v1/calculate", `{"items":1,"packSizes":[` + strings.Repeat("1,", 40000) + `1]}`, 413, "too large"},
		{"GET without items", "GET", "/api/v1/calculate", ``, 400, "required"},
		{"GET bad items", "GET", "/api/v1/calculate?items=abc", ``, 400, "whole number"},
		{"empty pack sizes", "PUT", "/api/v1/packs", `{"packSizes":[]}`, 400, "at least one"},
		{"zero pack size", "PUT", "/api/v1/packs", `{"packSizes":[0,250]}`, 400, "invalid pack size"},
		{"too complex", "POST", "/api/v1/calculate", `{"items":1000000000,"packSizes":[999983,999979]}`, 422, "too large"},
		{"wrong method", "DELETE", "/api/v1/packs", ``, 405, ""},
		{"unknown api route", "GET", "/api/v1/nope", ``, 404, "not found"},
		{"post to ui", "POST", "/", ``, 405, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, body := srv.do(t, tc.method, tc.path, tc.body)
			if status != tc.status || !strings.Contains(body, tc.contains) {
				t.Fatalf("got %d %s, want %d containing %q", status, body, tc.status, tc.contains)
			}
		})
	}
}

func TestHealthAndUI(t *testing.T) {
	srv := newTestServer(t)
	if status, body := srv.do(t, "GET", "/healthz", ""); status != 200 || !strings.Contains(body, "ok") {
		t.Fatalf("healthz: %d %s", status, body)
	}
	if status, body := srv.do(t, "GET", "/", ""); status != 200 || !strings.Contains(body, "<h1>ui</h1>") {
		t.Fatalf("ui: %d %s", status, body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/v1/packs", nil)
	rec := httptest.NewRecorder()
	srv.handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
}
