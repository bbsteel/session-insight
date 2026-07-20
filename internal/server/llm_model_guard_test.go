package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/llm"
)

func TestIsUniqueConstraintErr(t *testing.T) {
	if !isUniqueConstraintErr(errString("UNIQUE constraint failed: llm_providers.model_id")) {
		t.Fatal("expected unique detect")
	}
	if isUniqueConstraintErr(errString("no such table")) {
		t.Fatal("should not match")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

type unavailableModelClient struct{}

func (unavailableModelClient) ListModels(context.Context) ([]llm.Model, error) { return nil, nil }
func (unavailableModelClient) Generate(context.Context, string, llm.StatusFunc) (string, error) {
	return "", &llm.ModelUnavailableError{
		ModelID: "gpt-5.4-mini", Agent: "codex", Available: []string{"gpt-5.5"},
	}
}

func TestAIGenerateEmitsStructuredUnavailableModelError(t *testing.T) {
	s := newInsightServer(t, findingDetail())
	providerID, err := s.DB.AddLLMProvider(db.LLMProvider{
		Name: "stale-codex", Kind: "acp", Agent: "codex", ModelID: "gpt-5.4-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.newGenerationClient = func(llm.Config) (llm.Client, error) {
		return unavailableModelClient{}, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/ai/summary",
		strings.NewReader(fmt.Sprintf(`{"provider_id":%d}`, providerID)))
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("Content-Type", "application/json")
	req.Host = "localhost"
	w := httptest.NewRecorder()
	s.Mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 SSE: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{
		"event: error", `"code":"model_unavailable"`, fmt.Sprintf(`"provider_id":%d`, providerID),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE body missing %q:\n%s", want, body)
		}
	}
}
