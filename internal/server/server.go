package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/cjdupless/plugo-notebook/internal/notebook"
	"github.com/cjdupless/plugo-notebook/internal/runner"
	"github.com/cjdupless/plugo-notebook/internal/web"
)

const AppVersion = "0.1.0"

// Server exposes Plugo's HTTP API and embedded web application.
type Server struct {
	store    *notebook.Store
	executor *runner.Executor
	mux      *http.ServeMux
}

// New creates an HTTP handler serving both the Plugo web UI and its JSON API.
func New(store *notebook.Store, executor *runner.Executor) (*Server, error) {
	s := &Server{
		store:    store,
		executor: executor,
		mux:      http.NewServeMux(),
	}

	s.registerRoutes()
	if err := s.registerStatic(); err != nil {
		return nil, err
	}
	return s, nil
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/info", s.handleInfo)
	s.mux.HandleFunc("GET /api/notebooks", s.handleListNotebooks)
	s.mux.HandleFunc("POST /api/notebooks", s.handleCreateNotebook)
	s.mux.HandleFunc("GET /api/notebooks/{id}", s.handleGetNotebook)
	s.mux.HandleFunc("PUT /api/notebooks/{id}", s.handleSaveNotebook)
	s.mux.HandleFunc("DELETE /api/notebooks/{id}", s.handleDeleteNotebook)
	s.mux.HandleFunc("POST /api/notebooks/{id}/cells/{cellId}/run", s.handleRunCell)
	s.mux.HandleFunc("POST /api/notebooks/{id}/cells/{cellId}/stop", s.handleStopCell)
	s.mux.HandleFunc("GET /api/notebooks/{id}/export.go", s.handleExportGo)
	s.mux.HandleFunc("POST /api/format", s.handleFormat)
	s.mux.HandleFunc("POST /api/inspect", s.handleInspect)
}

func (s *Server) registerStatic() error {
	staticFS, err := fs.Sub(web.StaticFiles, "static")
	if err != nil {
		return fmt.Errorf("load embedded static files: %w", err)
	}
	fileServer := http.FileServer(http.FS(staticFS))
	s.mux.Handle("/", fileServer)
	return nil
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"app":       "Plugo",
		"version":   AppVersion,
		"goVersion": s.executor.GoVersion(),
		"workDir":   s.store.Dir(),
	})
}

func (s *Server) handleListNotebooks(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"notebooks": list,
	})
}

func (s *Server) handleCreateNotebook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	nb, err := s.store.Create(req.Title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"notebook": nb,
		"scope":    runner.InspectScope(nb.Cells),
	})
}

func (s *Server) handleGetNotebook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	nb, err := s.store.Get(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"notebook": nb,
		"scope":    runner.InspectScope(nb.Cells),
	})
}

func (s *Server) handleSaveNotebook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var nb notebook.Notebook
	if err := json.NewDecoder(r.Body).Decode(&nb); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload: "+err.Error())
		return
	}
	nb.ID = id
	if err := s.store.Save(&nb); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"notebook": &nb,
		"scope":    runner.InspectScope(nb.Cells),
	})
}

func (s *Server) handleDeleteNotebook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

func (s *Server) handleRunCell(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cellID := r.PathValue("cellId")

	var req struct {
		Title string          `json:"title"`
		Cells []notebook.Cell `json:"cells"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	nb, err := s.store.Get(id)
	if err != nil {
		if errors.Is(err, notebook.ErrNotFound) && len(req.Cells) > 0 {
			nb = &notebook.Notebook{ID: id, Title: req.Title, Cells: req.Cells}
		} else {
			writeStoreError(w, err)
			return
		}
	}

	if len(req.Cells) > 0 {
		nb.Cells = req.Cells
	}
	if strings.TrimSpace(req.Title) != "" {
		nb.Title = req.Title
	}

	targetIdx := -1
	for i := range nb.Cells {
		if nb.Cells[i].ID == cellID {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		writeError(w, http.StatusNotFound, "cell not found")
		return
	}

	runKey := id + ":" + cellID
	out, _, err := s.executor.Execute(r.Context(), runKey, nb.Cells, cellID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Increment execution count and persist output to the notebook.
	maxCount := 0
	for _, c := range nb.Cells {
		if c.ExecCount > maxCount {
			maxCount = c.ExecCount
		}
	}
	nb.Cells[targetIdx].ExecCount = maxCount + 1
	nb.Cells[targetIdx].Output = out
	_ = s.store.Save(nb)

	writeJSON(w, http.StatusOK, map[string]any{
		"output":    out,
		"execCount": nb.Cells[targetIdx].ExecCount,
		"scope":     runner.InspectScope(nb.Cells),
	})
}

func (s *Server) handleStopCell(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cellID := r.PathValue("cellId")
	canceled := s.executor.Cancel(id + ":" + cellID)
	writeJSON(w, http.StatusOK, map[string]any{"canceled": canceled})
}

func (s *Server) handleExportGo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	nb, err := s.store.Get(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	code, err := runner.ExportGo(nb.Title, nb.Cells)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	filename := nb.ID + ".go"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	}
	_, _ = w.Write([]byte(code))
}

func (s *Server) handleFormat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	formatted, err := runner.FormatCell(req.Source)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"formatted": formatted,
	})
}

func (s *Server) handleInspect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cells        []notebook.Cell `json:"cells"`
		TargetCellID string          `json:"targetCellId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}

	resp := map[string]any{
		"scope": runner.InspectScope(req.Cells),
	}
	if req.TargetCellID != "" {
		composed, err := runner.Compose(req.Cells, req.TargetCellID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp["generatedSource"] = composed.DisplayCode
		resp["autoImports"] = composed.AutoImports
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, notebook.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, notebook.ErrInvalidID):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
