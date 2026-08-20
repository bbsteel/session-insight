package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func testHTTPServer(t *testing.T) (*server, *httptest.Server) {
	t.Helper()
	store := testStore(t)
	srv := newServer(store, t.TempDir(), nil, 5)
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)
	return srv, ts
}

func postJSON(t *testing.T, url string, body string) (int, map[string]any) {
	t.Helper()
	res, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out) //nolint:errcheck
	return res.StatusCode, out
}

func uploadFile(t *testing.T, ts *httptest.Server, agent, logical string, content []byte, filename string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("agent", agent)          //nolint:errcheck
	w.WriteField("logical_name", logical) //nolint:errcheck
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(content) //nolint:errcheck
	w.Close()         //nolint:errcheck
	res, err := http.Post(ts.URL+"/api/upload", w.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out) //nolint:errcheck
	return res.StatusCode, out
}

func TestServerUploadAndState(t *testing.T) {
	_, ts := testHTTPServer(t)

	code, out := uploadFile(t, ts, "claude", "04-thinking", pngBytes(t, 1), "shot.png")
	if code != http.StatusOK {
		t.Fatalf("upload = %d %v", code, out)
	}

	res, err := http.Get(ts.URL + "/api/state?agent=claude")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	var state stateResponse
	if err := json.NewDecoder(res.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	var thinking *itemState
	for i := range state.Items {
		if state.Items[i].ID == "04-thinking" {
			thinking = &state.Items[i]
		}
	}
	if thinking == nil {
		t.Fatal("state must include 04-thinking")
	}
	if thinking.Slots[0].Status != StatusCaptured {
		t.Fatalf("default slot status = %s, want captured", thinking.Slots[0].Status)
	}
	if thinking.Slots[1].Status != StatusMissing {
		t.Fatalf("toggled slot status = %s, want missing (allowed gap)", thinking.Slots[1].Status)
	}

	// The uploaded image is served only through the catalog-gated endpoint.
	imgRes, err := http.Get(ts.URL + thinking.Slots[0].ImageURL)
	if err != nil {
		t.Fatal(err)
	}
	imgRes.Body.Close() //nolint:errcheck
	if imgRes.StatusCode != http.StatusOK || imgRes.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("image serve = %d %s", imgRes.StatusCode, imgRes.Header.Get("Content-Type"))
	}
}

func TestServerRejectsBadUploads(t *testing.T) {
	_, ts := testHTTPServer(t)
	if code, _ := uploadFile(t, ts, "claude", "04-thinking", []byte("plain text"), "x.txt"); code != http.StatusBadRequest {
		t.Fatalf("text upload = %d, want 400", code)
	}
	if code, _ := uploadFile(t, ts, "claude", "99-nope", pngBytes(t, 1), "x.png"); code != http.StatusBadRequest {
		t.Fatalf("unknown logical name = %d, want 400", code)
	}
	if code, _ := uploadFile(t, ts, "../claude", "04-thinking", pngBytes(t, 1), "x.png"); code != http.StatusBadRequest {
		t.Fatalf("traversal agent = %d, want 400", code)
	}
}

func TestServerNotApplicableRequiresReason(t *testing.T) {
	_, ts := testHTTPServer(t)
	code, _ := postJSON(t, ts.URL+"/api/not-applicable",
		`{"agent":"claude","logical_name":"09-tool-timeout","value":true,"reason":""}`)
	if code != http.StatusBadRequest {
		t.Fatalf("not_applicable without reason = %d, want 400", code)
	}
	code, _ = postJSON(t, ts.URL+"/api/not-applicable",
		`{"agent":"claude","logical_name":"09-tool-timeout","value":true,"reason":"adapter research: CLI has no timeout state"}`)
	if code != http.StatusOK {
		t.Fatalf("not_applicable with reason = %d, want 200", code)
	}
}

func TestServerWorkOrderEndpoint(t *testing.T) {
	srv, ts := testHTTPServer(t)
	if code, out := postJSON(t, ts.URL+"/api/work-order", `{"agent":"claude"}`); code != http.StatusBadRequest {
		t.Fatalf("work order without inputs = %d %v, want 400", code, out)
	}
	if code, out := uploadFile(t, ts, "claude", "04-thinking", pngBytes(t, 1), "shot.png"); code != http.StatusOK {
		t.Fatalf("upload = %d %v", code, out)
	}
	code, out := postJSON(t, ts.URL+"/api/work-order", `{"agent":"claude"}`)
	if code != http.StatusOK {
		t.Fatalf("work order = %d %v", code, out)
	}
	wo, _ := out["work_order"].(map[string]any)
	if wo == nil || wo["dir"] == "" {
		t.Fatalf("work order response missing record: %v", out)
	}
	_ = srv // state verified via API above
}

func TestServerImageGating(t *testing.T) {
	_, ts := testHTTPServer(t)
	res, err := http.Get(ts.URL + "/api/image?agent=claude&hash=" + strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close() //nolint:errcheck
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown image hash = %d, want 404", res.StatusCode)
	}
}

// TestServerOmitsURLForMissingBlob pins that a locally unavailable capture is
// reported without an image URL (the endpoint would 404).
func TestServerOmitsURLForMissingBlob(t *testing.T) {
	srv, ts := testHTTPServer(t)
	code, out := uploadFile(t, ts, "claude", "04-thinking", pngBytes(t, 1), "shot.png")
	if code != http.StatusOK {
		t.Fatalf("upload = %d %v", code, out)
	}
	capture, _ := out["capture"].(map[string]any)
	hash, _ := capture["hash"].(string)
	ext, _ := capture["ext"].(string)
	if err := os.Remove(srv.store.blobPath("claude", hash, ext)); err != nil {
		t.Fatal(err)
	}

	res, err := http.Get(ts.URL + "/api/state?agent=claude")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	var state stateResponse
	if err := json.NewDecoder(res.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	for _, item := range state.Items {
		if item.ID != "04-thinking" {
			continue
		}
		slot := item.Slots[0]
		if !slot.LocalUnavailable {
			t.Fatal("missing blob must set local_unavailable")
		}
		if slot.ImageURL != "" {
			t.Fatalf("missing blob must not emit an image URL, got %s", slot.ImageURL)
		}
		if slot.Capture == nil {
			t.Fatal("the capture record must stay visible even without its blob")
		}
		return
	}
	t.Fatal("state must include 04-thinking")
}
