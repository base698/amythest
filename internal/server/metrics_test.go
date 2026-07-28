package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsEndpoint(t *testing.T) {
	s := &Server{mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /ok", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)

	s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ok", nil))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "version=0.0.4") {
		t.Fatalf("content type = %q", got)
	}
	for _, want := range []string{
		`amythest_http_requests_total{method="GET",code="204"} 1`,
		`amythest_http_request_duration_seconds_bucket{method="GET",code="204",le="+Inf"} 1`,
		"amythest_go_heap_alloc_bytes",
		"amythest_vault_notes",
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("metrics output missing %q", want)
		}
	}
}
