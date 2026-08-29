package server

import (
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bbsteel/session-insight/internal/changehost/openapi"
)

// runtimeReviewPlatform serves two ordinary reviews (1234 sample, 5678 query
// target), one capture-racing review (9999, head SHA changes between reads),
// and one drifting review (8888, content anchor missing).
func runtimeReviewPlatform(t *testing.T, detailCalls *atomic.Int32) *httptest.Server {
	t.Helper()
	headSHA := "0123456789abcdef0123456789abcdef01234567"
	detail := func(w http.ResponseWriter, r *http.Request, number, title string) {
		head := headSHA
		if number == "9999" {
			// Every read returns a different head: capture must race.
			head = fmt.Sprintf("%040x", detailCalls.Add(1))
		}
		source := fmt.Sprintf(`"source": {"latestCommit": "%s", "branch": "feature/x"},`, head)
		if number == "8888" {
			source = `"source": {"branch": "feature/x"},`
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"id": 88%[2]s, "number": %[2]s, "title": %[3]q, "state": "open",
			"web_url": "http://%[4]s/projects/team/repo/pulls/%[2]s",
			"repository": {"slug": "team/repo"},
			%[5]s
			"destination": {"branch": "main"}
		}`, "", number, title, r.Host, source)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "si-test-secret-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		detailCalls.Add(1)
		switch r.URL.Path {
		case "/api/projects/team/repo/reviews/1234":
			detail(w, r, "1234", "Sample change")
		case "/api/projects/team/repo/reviews/5678":
			detail(w, r, "5678", "Runtime change")
		case "/api/projects/team/repo/reviews/9999":
			detail(w, r, "9999", "Racing change")
		case "/api/projects/team/repo/reviews/8888":
			detail(w, r, "8888", "Drifting change")
		case "/api/projects/team/repo/reviews/1234/files":
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("cursor") == "p2" {
				fmt.Fprint(w, `{"values": [{"path": "src/b.go", "status": "added", "diff": "@@ -0,0 +1 @@"}]}`)
			} else {
				fmt.Fprint(w, `{"values": [{"path": "src/a.go", "status": "modified", "diff": "@@ -1 +1 @@"}], "next_cursor": "p2"}`)
			}
		case "/api/projects/team/repo/reviews/1234/commits":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"values": [{"sha": "%[1]s", "message": "sample commit", "author": "Ana", "authoredAt": "2026-08-01T09:00:00Z"}]}`, headSHA)
		case "/api/projects/team/repo/reviews/5678/files":
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("cursor") == "p2" {
				fmt.Fprint(w, `{"values": [{"path": "src/c.go", "status": "added", "diff": "@@ -0,0 +1 @@"}]}`)
			} else {
				fmt.Fprint(w, `{"values": [
					{"path": "src/a.go", "status": "modified", "diff": "@@ -1 +1 @@"},
					{"path": "src/b.go", "status": "deleted", "diff": "@@ -1 +0 @@"}
				], "next_cursor": "p2"}`)
			}
		case "/api/projects/team/repo/reviews/9999/files",
			"/api/projects/team/repo/reviews/9999/commits":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"values": []}`)
		case "/api/projects/team/repo/reviews/5678/commits":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"values": [
				{"sha": "%[1]s", "message": "runtime commit", "author": "Ana", "authoredAt": "2026-08-01T09:00:00Z"}
			]}`, headSHA)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

const runtimeOpenAPIDoc = `{
 "openapi": "3.0.3",
 "servers": [
  {
   "url": "%s/api"
  }
 ],
 "components": {
  "securitySchemes": {
   "token": {
    "type": "apiKey",
    "in": "header",
    "name": "PRIVATE-TOKEN"
   }
  }
 },
 "paths": {
  "/projects/{repository}/reviews/{number}": {
   "get": {
    "operationId": "getReview",
    "parameters": [
     {
      "name": "repository",
      "in": "path",
      "required": true,
      "schema": {
       "type": "string"
      }
     },
     {
      "name": "number",
      "in": "path",
      "required": true,
      "schema": {
       "type": "integer"
      }
     }
    ],
    "responses": {
     "200": {
      "description": "ok",
      "content": {
       "application/json": {
        "schema": {
         "type": "object",
         "properties": {
          "id": {
           "type": "integer"
          },
          "number": {
           "type": "integer"
          },
          "title": {
           "type": "string"
          },
          "state": {
           "type": "string"
          },
          "web_url": {
           "type": "string"
          }
         }
        }
       }
      }
     }
    }
   }
  },
  "/projects/{repository}/reviews/{number}/files": {
   "get": {
    "operationId": "listReviewFiles",
    "parameters": [
     {
      "name": "repository",
      "in": "path",
      "required": true,
      "schema": {
       "type": "string"
      }
     },
     {
      "name": "number",
      "in": "path",
      "required": true,
      "schema": {
       "type": "integer"
      }
     }
    ],
    "responses": {
     "200": {
      "description": "ok",
      "content": {
       "application/json": {
        "schema": {
         "type": "object",
         "properties": {
          "values": {
           "type": "array",
           "items": {
            "type": "object",
            "properties": {
             "path": {
              "type": "string"
             },
             "status": {
              "type": "string"
             },
             "diff": {
              "type": "string"
             }
            }
           }
          },
          "next_cursor": {
           "type": "string"
          }
         }
        }
       }
      }
     }
    }
   }
  },
  "/projects/{repository}/reviews/{number}/commits": {
   "get": {
    "operationId": "listReviewCommits",
    "parameters": [
     {
      "name": "repository",
      "in": "path",
      "required": true,
      "schema": {
       "type": "string"
      }
     },
     {
      "name": "number",
      "in": "path",
      "required": true,
      "schema": {
       "type": "integer"
      }
     }
    ],
    "responses": {
     "200": {
      "description": "ok",
      "content": {
       "application/json": {
        "schema": {
         "type": "object",
         "properties": {
          "values": {
           "type": "array",
           "items": {
            "type": "object",
            "properties": {
             "sha": {
              "type": "string"
             },
             "message": {
              "type": "string"
             }
            }
           }
          }
         }
        }
       }
      }
     }
    }
   }
  }
 }
}`

// multipartWriter builds a multipart import request body and returns its
// content type.
func multipartWriter(t *testing.T, body *strings.Builder, fields map[string]string, document string) string {
	t.Helper()
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("document", "openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(document)); err != nil {
		t.Fatal(err)
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return writer.FormDataContentType()
}

// activateFixtureProfile drives the full PR2 lifecycle against the runtime
// fixture: import, approve, probe, confirm the object id, verify, activate.
func activateFixtureProfile(t *testing.T, server *Server, fixture *httptest.Server, document string) (hostKey, profileID string) {
	t.Helper()
	var body strings.Builder
	writer := multipartWriter(t, &body, map[string]string{
		"display_name":        "Runtime Review",
		"api_base_url":        fixture.URL + "/api",
		"sample_change_url":   fixture.URL + "/projects/team/repo/pulls/1234",
		"credential_mode":     "environment",
		"credential_env_name": "SI_TEST_REVIEW_TOKEN",
	}, document)
	request := httptest.NewRequest("POST", "/api/change-host-profiles/import", strings.NewReader(body.String()))
	request.Header.Set("Content-Type", writer)
	response := httptest.NewRecorder()
	server.Mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("import: status=%d body=%s", response.Code, response.Body.String())
	}
	var imported struct {
		Profile struct {
			ProfileID string `json:"profile_id"`
		} `json:"profile"`
		Host struct {
			Key string `json:"key"`
		} `json:"host"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	hostKey, profileID = imported.Host.Key, imported.Profile.ProfileID

	response = serveChangeRequestAPI(server, "POST", "/api/change-hosts/"+hostKey+"/approve",
		`{"allow_http": true, "allow_private_network": true}`)
	if response.Code != http.StatusOK {
		t.Fatalf("approve: status=%d body=%s", response.Code, response.Body.String())
	}
	response = serveChangeRequestAPI(server, "POST", "/api/change-host-profiles/"+profileID+"/probe", "")
	if response.Code != http.StatusOK {
		t.Fatalf("probe: status=%d body=%s", response.Code, response.Body.String())
	}
	response = serveChangeRequestAPI(server, "PATCH", "/api/change-host-profiles/"+profileID+"/mapping",
		`{"selections": [{"role": "resolve_change", "field": "provider_object_id", "pointer": "/id"}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("mapping: status=%d body=%s", response.Code, response.Body.String())
	}
	response = serveChangeRequestAPI(server, "POST", "/api/change-host-profiles/"+profileID+"/verify", "")
	if response.Code != http.StatusOK {
		t.Fatalf("verify: status=%d body=%s", response.Code, response.Body.String())
	}
	response = serveChangeRequestAPI(server, "POST", "/api/change-host-profiles/"+profileID+"/activate", "")
	if response.Code != http.StatusOK {
		t.Fatalf("activate: status=%d body=%s", response.Code, response.Body.String())
	}
	var activated struct {
		Lifecycle string `json:"lifecycle"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &activated); err != nil {
		t.Fatal(err)
	}
	if activated.Lifecycle != string(openapi.ProfileActive) {
		t.Fatalf("profile not active after activation: %s", response.Body.String())
	}
	return hostKey, profileID
}

func TestOpenAPIProfileRuntimeQueryAfterActivation(t *testing.T) {
	t.Setenv("SI_TEST_REVIEW_TOKEN", "si-test-secret-token")
	var detailCalls atomic.Int32
	fixture := runtimeReviewPlatform(t, &detailCalls)
	defer fixture.Close()

	database := openCollabAPIDB(t)
	server := New(database, nil)
	hostKey, profileID := activateFixtureProfile(t, server, fixture,
		fmt.Sprintf(runtimeOpenAPIDoc, fixture.URL))

	// Acceptance §17.6: another change URL on the same platform resolves.
	response := serveChangeRequestAPI(server, "POST", "/api/change-hosts/"+hostKey+"/refresh",
		fmt.Sprintf(`{"reference": "%s/projects/team/repo/pulls/5678"}`, fixture.URL))
	if response.Code != http.StatusOK {
		t.Fatalf("refresh: status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "si-test-secret-token") {
		t.Fatal("token leaked into the refresh response")
	}
	var lookup map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &lookup); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(lookup)
	if !strings.Contains(string(encoded), "Runtime change") {
		t.Fatalf("refresh did not resolve the runtime change: %s", encoded)
	}

	// The persisted snapshot carries the adapter revision provenance.
	var snapshotCount int
	if err := database.Conn().QueryRow(
		`SELECT COUNT(*) FROM change_request_snapshots WHERE profile_id = ?`, profileID,
	).Scan(&snapshotCount); err != nil || snapshotCount != 1 {
		t.Fatalf("snapshot profile provenance missing: count=%d err=%v", snapshotCount, err)
	}
	// Cursor pagination pulled the second page (three files total).
	var fileCount int
	if err := database.Conn().QueryRow(
		`SELECT COUNT(*) FROM change_request_files`,
	).Scan(&fileCount); err != nil {
		t.Fatal(err)
	}
	if fileCount != 3 {
		t.Fatalf("cursor-paginated file set incomplete: %d files", fileCount)
	}
	var commitCount int
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM change_request_commits`).Scan(&commitCount); err != nil {
		t.Fatal(err)
	}
	if commitCount != 1 {
		t.Fatalf("commit dimension incomplete: %d commits", commitCount)
	}

	// A fresh server over the same database restores active-profile parsers at
	// startup (design §16.4): the same refresh keeps working after a restart.
	restarted := New(database, nil)
	response = serveChangeRequestAPI(restarted, "POST", "/api/change-hosts/"+hostKey+"/refresh",
		fmt.Sprintf(`{"reference": "%s/projects/team/repo/pulls/5678"}`, fixture.URL))
	if response.Code != http.StatusOK {
		t.Fatalf("refresh after restart: status=%d body=%s", response.Code, response.Body.String())
	}

	// Capture race: the racing review must not publish a snapshot.
	response = serveChangeRequestAPI(server, "POST", "/api/change-hosts/"+hostKey+"/refresh",
		fmt.Sprintf(`{"reference": "%s/projects/team/repo/pulls/9999"}`, fixture.URL))
	if response.Code != http.StatusConflict {
		t.Fatalf("capture race must surface as conflict: status=%d body=%s", response.Code, response.Body.String())
	}

	// Schema drift: the drifting review drops the content anchor; the profile
	// degrades instead of silently mis-mapping.
	response = serveChangeRequestAPI(server, "POST", "/api/change-hosts/"+hostKey+"/refresh",
		fmt.Sprintf(`{"reference": "%s/projects/team/repo/pulls/8888"}`, fixture.URL))
	if response.Code == http.StatusOK {
		t.Fatalf("drifted anchor must not produce a snapshot: %s", response.Body.String())
	}
	profile, exists, err := database.ChangeHostProfile(profileID)
	if err != nil || !exists {
		t.Fatal(err)
	}
	if profile.Lifecycle != openapi.ProfileDegraded || profile.LastFailureCode != string(openapi.IssueSchemaDrift) {
		t.Fatalf("profile must degrade on drift: lifecycle=%s code=%s", profile.Lifecycle, profile.LastFailureCode)
	}
}

// TestOpenAPIProfileMetadataOnlyDegradation covers a platform exposing only a
// detail endpoint: metadata resolves, and every other dimension degrades to
// unsupported without fabricated data (design §6.3/§11.3).
func TestOpenAPIProfileMetadataOnlyDegradation(t *testing.T) {
	t.Setenv("SI_TEST_REVIEW_TOKEN", "si-test-secret-token")
	var detailCalls atomic.Int32
	fixture := runtimeReviewPlatform(t, &detailCalls)
	defer fixture.Close()

	database := openCollabAPIDB(t)
	server := New(database, nil)
	doc := map[string]any{}
	if err := json.Unmarshal([]byte(fmt.Sprintf(runtimeOpenAPIDoc, fixture.URL)), &doc); err != nil {
		t.Fatal(err)
	}
	// Detail-only document: no files/commits paths.
	paths := doc["paths"].(map[string]any)
	for key := range paths {
		if key != "/projects/{repository}/reviews/{number}" {
			delete(paths, key)
		}
	}
	docRaw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	hostKey, _ := activateFixtureProfile(t, server, fixture, string(docRaw))

	response := serveChangeRequestAPI(server, "POST", "/api/change-hosts/"+hostKey+"/refresh",
		fmt.Sprintf(`{"reference": "%s/projects/team/repo/pulls/5678"}`, fixture.URL))
	if response.Code != http.StatusOK {
		t.Fatalf("refresh: status=%d body=%s", response.Code, response.Body.String())
	}
	var fileCount, commitCount int
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM change_request_files`).Scan(&fileCount); err != nil {
		t.Fatal(err)
	}
	if err := database.Conn().QueryRow(`SELECT COUNT(*) FROM change_request_commits`).Scan(&commitCount); err != nil {
		t.Fatal(err)
	}
	if fileCount != 0 || commitCount != 0 {
		t.Fatalf("metadata-only snapshot must not fabricate files or commits: files=%d commits=%d", fileCount, commitCount)
	}
	var completeness string
	if err := database.Conn().QueryRow(`SELECT completeness_json FROM change_request_snapshots LIMIT 1`).Scan(&completeness); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(completeness, `"unavailable"`) {
		t.Fatalf("degraded dimensions must be unavailable, not fabricated: %s", completeness)
	}
}
