package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cjdupless/plugo-notebook/internal/notebook"
	"github.com/cjdupless/plugo-notebook/internal/runner"
)

func TestServerEndToEndFlow(t *testing.T) {
	dir := t.TempDir()
	store, err := notebook.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	nb, err := store.EnsureStarterNotebook()
	if err != nil {
		t.Fatalf("EnsureStarterNotebook: %v", err)
	}

	exec := runner.NewExecutor(15*time.Second, dir)
	srv, err := New(store, exec)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	// 1. Serves embedded index.html at /
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Plugo") {
		t.Fatalf("GET / failed: code=%d", rec.Code)
	}

	// 2. GET /api/info
	req = httptest.NewRequest(http.MethodGet, "/api/info", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"app":"Plugo"`) {
		t.Fatalf("GET /api/info failed: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// 3. Run the 4th starter cell (which depends on Gopher & AppName from cell 2!)
	runURL := "/api/notebooks/" + nb.ID + "/cells/cwelcome4/run"
	req = httptest.NewRequest(http.MethodPost, runURL, bytes.NewBufferString(`{}`))
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST %s failed: code=%d body=%s", runURL, rec.Code, rec.Body.String())
	}

	var runResp struct {
		Output    notebook.ExecOutput `json:"output"`
		ExecCount int                 `json:"execCount"`
		Scope     []notebook.Symbol   `json:"scope"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &runResp); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	if runResp.Output.Status != "ok" {
		t.Fatalf("expected ok status, got %q stderr=%s", runResp.Output.Status, runResp.Output.Stderr)
	}
	if !strings.Contains(runResp.Output.Stdout, "Plugo Team (3 members)") || !strings.Contains(runResp.Output.Stdout, "Ada") {
		t.Fatalf("unexpected stdout: %q", runResp.Output.Stdout)
	}
	if len(runResp.Scope) < 3 {
		t.Fatalf("expected shared scope symbols, got %+v", runResp.Scope)
	}

	// 4. Export .go file
	req = httptest.NewRequest(http.MethodGet, "/api/notebooks/"+nb.ID+"/export.go", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "type Gopher struct") {
		t.Fatalf("GET export.go failed: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// 5. Format snippet via /api/format
	req = httptest.NewRequest(http.MethodPost, "/api/format", bytes.NewBufferString(`{"source":"x:=1+2\nfmt.Println(x)"}`))
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `x := 1 + 2`) {
		t.Fatalf("POST /api/format failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
}
