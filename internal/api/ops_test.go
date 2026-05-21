package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouterMetrics(t *testing.T) {
	handler := newTestRouter()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/plain") {
		t.Fatalf("GET /metrics Content-Type = %q, want text/plain", contentType)
	}
	body := response.Body.String()
	for _, want := range []string{
		"# HELP postamat_build_info",
		"# TYPE postamat_build_info gauge",
		`postamat_build_info{service="postamat"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /metrics body missing %q in:\n%s", want, body)
		}
	}
}

func TestRouterMetricsRejectsUnsupportedMethod(t *testing.T) {
	handler := newTestRouter()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/metrics", nil))

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /metrics status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}
