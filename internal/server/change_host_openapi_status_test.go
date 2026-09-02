package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
)

// An openapi host without an active profile must report explicit
// unsupported capabilities, never a null operations map.
func TestOpenAPIHostWithoutProfileReportsUnsupportedCapabilities(t *testing.T) {
	database := openCollabAPIDB(t)
	server := New(database, nil)
	if err := database.StoreChangeHostPreview(db.ChangeHostRecord{
		HostID: "openapi-review-internal", Provider: model.ChangeProviderOpenAPI,
		DisplayOrigin:   "https://review.internal",
		EndpointOrigins: []string{"https://review.internal"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.ApproveChangeHost("openapi-review-internal", false, false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	response := serveChangeRequestAPI(server, "GET", "/api/change-hosts/openapi-review-internal/status", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var status struct {
		Capabilities struct {
			Operations map[string]struct {
				State      string `json:"state"`
				ReasonCode string `json:"reason_code"`
			} `json:"operations"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Capabilities.Operations) == 0 {
		t.Fatalf("operations must be an explicit map: %s", response.Body.String())
	}
	for id, declaration := range status.Capabilities.Operations {
		if declaration.State != "unsupported" || declaration.ReasonCode == "" {
			t.Fatalf("operation %s must be explicitly unsupported: %+v", id, declaration)
		}
	}
}
