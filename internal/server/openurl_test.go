package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleVersionReportsAppMode(t *testing.T) {
	server := New(nil, nil)
	server.Version = "v1.2.3"
	server.AppMode = true

	request := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	response := httptest.NewRecorder()
	server.Mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("version response is not JSON: %v", err)
	}
	if payload["appMode"] != true {
		t.Errorf("appMode = %v, want true (payload %v)", payload["appMode"], payload)
	}
	if payload["version"] != "v1.2.3" {
		t.Errorf("version = %v, want v1.2.3", payload["version"])
	}
}

func TestHandleOpenURL(t *testing.T) {
	t.Run("opens valid https link", func(t *testing.T) {
		var opened []string
		server := New(nil, nil)
		server.SetOpenURL(func(url string) { opened = append(opened, url) })

		body := strings.NewReader(`{"url":"https://example.com/docs"}`)
		request := httptest.NewRequest(http.MethodPost, "/api/open-url", body)
		response := httptest.NewRecorder()
		server.Mux.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body = %s", response.Code, response.Body.String())
		}
		if len(opened) != 1 || opened[0] != "https://example.com/docs" {
			t.Errorf("opened = %v, want the posted URL", opened)
		}
	})

	t.Run("rejects non-http schemes", func(t *testing.T) {
		server := New(nil, nil)
		server.SetOpenURL(func(string) { t.Error("opener must not be called") })
		for _, url := range []string{
			"file:///etc/passwd",
			"javascript:alert(1)",
			"//evil.example.com",
			"relative/path",
			"ftp://example.com/x",
		} {
			body := strings.NewReader(`{"url":"` + url + `"}`)
			request := httptest.NewRequest(http.MethodPost, "/api/open-url", body)
			response := httptest.NewRecorder()
			server.Mux.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Errorf("url %q: status = %d, want 400", url, response.Code)
			}
		}
	})

	t.Run("rejects malformed body", func(t *testing.T) {
		server := New(nil, nil)
		server.SetOpenURL(func(string) { t.Error("opener must not be called") })
		request := httptest.NewRequest(http.MethodPost, "/api/open-url", strings.NewReader(`{`))
		response := httptest.NewRecorder()
		server.Mux.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", response.Code)
		}
	})

	t.Run("unavailable without opener", func(t *testing.T) {
		server := New(nil, nil)
		body := strings.NewReader(`{"url":"https://example.com"}`)
		request := httptest.NewRequest(http.MethodPost, "/api/open-url", body)
		response := httptest.NewRecorder()
		server.Mux.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", response.Code)
		}
	})
}
