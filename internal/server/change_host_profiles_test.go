package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/changehost/openapi"
)

// fictionalReviewPlatform is a test-only change host whose fields are close
// to GitHub's but whose URLs and authentication differ (design §16.5 fixture
// kind 1).
func fictionalReviewPlatform(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "si-test-secret-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/api/projects/team/repo/reviews/1234":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{
				"id": 8842,
				"number": 1234,
				"title": "Add retry budget",
				"state": "open",
				"web_url": "` + "http://" + r.Host + `/projects/team/repo/pulls/1234",
				"repository": {"slug": "team/repo"},
				"source": {"latestCommit": "0123456789abcdef0123456789abcdef01234567", "branch": "feature/retry"},
				"destination": {"branch": "main"}
			}`)
		case r.URL.Path == "/api/projects/team/repo/reviews/1234/files":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"values": [
				{"path": "src/main.go", "status": "modified", "diff": "@@ -1 +1 @@\n-old\n+new"},
				{"path": "src/util.go", "status": "added", "diff": "@@ -0,0 +1 @@\n+new"}
			]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func importOpenAPIProfile(t *testing.T, server *Server, fixture *httptest.Server) (string, *httptest.ResponseRecorder) {
	t.Helper()
	origin := fixture.URL
	document := fmt.Sprintf(`{
	  "openapi": "3.0.3",
	  "servers": [{"url": "%s/api"}],
	  "components": {"securitySchemes": {"token": {"type": "apiKey", "in": "header", "name": "PRIVATE-TOKEN"}}},
	  "paths": {
	    "/projects/{repository}/reviews/{number}": {
	      "get": {
	        "operationId": "getReview",
	        "parameters": [
	          {"name": "repository", "in": "path", "required": true, "schema": {"type": "string"}},
	          {"name": "number", "in": "path", "required": true, "schema": {"type": "integer"}}
	        ],
	        "responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {
	          "type": "object", "properties": {
	            "id": {"type": "integer"}, "number": {"type": "integer"},
	            "title": {"type": "string"}, "state": {"type": "string"},
	            "web_url": {"type": "string"}
	          }
	        }}}}}
	      }
	    },
	    "/projects/{repository}/reviews/{number}/files": {
	      "get": {
	        "operationId": "listReviewFiles",
	        "parameters": [
	          {"name": "repository", "in": "path", "required": true, "schema": {"type": "string"}},
	          {"name": "number", "in": "path", "required": true, "schema": {"type": "integer"}}
	        ],
	        "responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {
	          "type": "object", "properties": {"values": {"type": "array", "items": {"type": "object", "properties": {
	            "path": {"type": "string"}, "status": {"type": "string"}, "diff": {"type": "string"}
	          }}}}}
	        }}}}}
	      }
	    }
	  }`, origin)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("document", "openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(document)); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"display_name":        "Fixture Review",
		"api_base_url":        origin + "/api",
		"sample_change_url":   origin + "/projects/team/repo/pulls/1234",
		"credential_mode":     "environment",
		"credential_env_name": "SI_TEST_REVIEW_TOKEN",
	} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/api/change-host-profiles/import", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	server.Mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", response.Code, response.Body.String())
	}
	var imported struct {
		Profile struct {
			ProfileID string `json:"profile_id"`
			Lifecycle string `json:"lifecycle"`
		} `json:"profile"`
		EndpointOrigins []string `json:"endpoint_origins"`
		Host            struct {
			Key string `json:"key"`
		} `json:"host"`
		CandidateCount       int  `json:"candidate_count"`
		RequiresHostApproval bool `json:"requires_host_approval"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Profile.ProfileID == "" || imported.Profile.Lifecycle != "draft" {
		t.Fatalf("unexpected import profile: %s", response.Body.String())
	}
	if imported.CandidateCount == 0 || !imported.RequiresHostApproval {
		t.Fatalf("unexpected import result: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "SI_TEST_REVIEW_TOKEN") ||
		strings.Contains(response.Body.String(), "si-test-secret-token") {
		t.Fatalf("credential material leaked into the import response: %s", response.Body.String())
	}
	return imported.Host.Key, response
}

func TestChangeHostProfileImportProbeConfirmVerifyFlow(t *testing.T) {
	t.Setenv("SI_TEST_REVIEW_TOKEN", "si-test-secret-token")
	fixture := fictionalReviewPlatform(t)
	defer fixture.Close()

	database := openCollabAPIDB(t)
	server := New(database, nil)

	hostKey, importResponse := importOpenAPIProfile(t, server, fixture)
	var imported struct {
		Profile struct {
			ProfileID string `json:"profile_id"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(importResponse.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	profileID := imported.Profile.ProfileID

	// Probe before host approval is refused.
	response := serveChangeRequestAPI(server, "POST", "/api/change-host-profiles/"+profileID+"/probe", "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("probe without approval: status=%d body=%s", response.Code, response.Body.String())
	}

	// Approve the host (http + loopback need explicit flags).
	response = serveChangeRequestAPI(server, "POST", "/api/change-hosts/"+hostKey+"/approve",
		`{"allow_http": true, "allow_private_network": true}`)
	if response.Code != http.StatusOK {
		t.Fatalf("approve: status=%d body=%s", response.Code, response.Body.String())
	}

	// Probe: detail and files resolve; the object id ("id" at 0.7) needs one
	// user confirmation.
	response = serveChangeRequestAPI(server, "POST", "/api/change-host-profiles/"+profileID+"/probe", "")
	if response.Code != http.StatusOK {
		t.Fatalf("probe: status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "si-test-secret-token") {
		t.Fatalf("token leaked into the probe response: %s", response.Body.String())
	}
	var probed struct {
		Verified bool `json:"verified"`
		Profile  struct {
			Lifecycle    string `json:"lifecycle"`
			Capabilities struct {
				Metadata string `json:"metadata"`
				FileSet  string `json:"file_set"`
				Patches  string `json:"patches"`
			} `json:"capabilities"`
		} `json:"profile"`
		RequiredConfirmations []struct {
			Role       string `json:"role"`
			Field      string `json:"field"`
			Candidates []struct {
				Pointer string `json:"pointer"`
			} `json:"candidates"`
		} `json:"required_confirmations"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &probed); err != nil {
		t.Fatal(err)
	}
	if probed.Verified {
		t.Fatal("probe must not verify while a required field awaits confirmation")
	}
	if probed.Profile.Capabilities.Metadata != "supported" || probed.Profile.Capabilities.FileSet != "supported" ||
		probed.Profile.Capabilities.Patches != "supported" {
		t.Fatalf("capabilities: %s", response.Body.String())
	}
	foundConfirmation := false
	for _, confirmation := range probed.RequiredConfirmations {
		if confirmation.Role == "resolve_change" && confirmation.Field == "provider_object_id" {
			foundConfirmation = true
			if len(confirmation.Candidates) == 0 || confirmation.Candidates[0].Pointer != "/id" {
				t.Fatalf("provider_object_id candidates: %+v", confirmation.Candidates)
			}
		}
	}
	if !foundConfirmation {
		t.Fatalf("expected provider_object_id confirmation: %s", response.Body.String())
	}

	// Confirm the object id, then verify — the re-probe keeps the confirmed
	// mapping instead of asking again.
	response = serveChangeRequestAPI(server, "PATCH", "/api/change-host-profiles/"+profileID+"/mapping",
		`{"selections": [{"role": "resolve_change", "field": "provider_object_id", "pointer": "/id"}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("mapping: status=%d body=%s", response.Code, response.Body.String())
	}
	response = serveChangeRequestAPI(server, "POST", "/api/change-host-profiles/"+profileID+"/verify", "")
	if response.Code != http.StatusOK {
		t.Fatalf("verify: status=%d body=%s", response.Code, response.Body.String())
	}
	var verified struct {
		Verified bool `json:"verified"`
		Profile  struct {
			Lifecycle string `json:"lifecycle"`
		} `json:"profile"`
		RequiredConfirmations []any `json:"required_confirmations"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &verified); err != nil {
		t.Fatal(err)
	}
	if !verified.Verified || verified.Profile.Lifecycle != string(openapi.ProfileVerified) {
		t.Fatalf("verify did not verify: %s", response.Body.String())
	}
	if len(verified.RequiredConfirmations) != 0 {
		t.Fatalf("confirmed mapping was re-asked: %s", response.Body.String())
	}

	// The stored profile row must hold only the credential reference, never
	// the secret.
	record, exists, err := database.ChangeHostProfile(profileID)
	if err != nil || !exists {
		t.Fatal(err)
	}
	if strings.Contains(record.ProfileJSON, "si-test-secret-token") ||
		strings.Contains(record.InferenceReportJSON, "si-test-secret-token") {
		t.Fatal("secret material persisted")
	}
	if !strings.Contains(record.ProfileJSON, "env:SI_TEST_REVIEW_TOKEN") {
		t.Fatal("credential reference missing from stored profile")
	}

	// Revoke ends the lifecycle.
	response = serveChangeRequestAPI(server, "POST", "/api/change-host-profiles/"+profileID+"/revoke", "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("revoke: status=%d", response.Code)
	}
}

func TestChangeHostProfileImportRejectsUnsafeInputs(t *testing.T) {
	database := openCollabAPIDB(t)
	server := New(database, nil)

	importWith := func(document, sampleURL string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, _ := writer.CreateFormFile("document", "openapi.json")
		part.Write([]byte(document))
		writer.WriteField("display_name", "Fixture")
		writer.WriteField("api_base_url", "https://review.internal/api")
		writer.WriteField("sample_change_url", sampleURL)
		writer.WriteField("credential_mode", "environment")
		writer.WriteField("credential_env_name", "SI_TEST_REVIEW_TOKEN")
		writer.Close()
		request := httptest.NewRequest("POST", "/api/change-host-profiles/import", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		response := httptest.NewRecorder()
		server.Mux.ServeHTTP(response, request)
		return response
	}

	// External $ref is rejected with its stable code.
	response := importWith(`{
	  "openapi": "3.0.3",
	  "paths": {"/x": {"get": {"responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "https://evil.example/x.json"}}}}}}}}
	}`, "https://review.internal/projects/team/repo/pulls/1")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "openapi_external_reference_rejected") {
		t.Fatalf("external $ref: status=%d body=%s", response.Code, response.Body.String())
	}

	// A sample URL carrying a token in the query is rejected.
	response = importWith(`{"openapi":"3.0.3","paths":{"/x":{"get":{"responses":{"200":{"description":"ok"}}}}}}`,
		"https://review.internal/projects/team/repo/pulls/1?token=secret")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("token-bearing sample URL: status=%d", response.Code)
	}
}
