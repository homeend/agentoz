package server

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"

	"erbrus/internal/config"
)

// Presets over the API, for `erbrus preset show|set` (agents seeding a
// short name for their CLI) and `erbrus agents setup`.

type presetJSON struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Prompt   string `json:"prompt"`
	Args     string `json:"args"`
	Agent    string `json:"agent_name"`
}

func presetToJSON(name string, p config.Preset) presetJSON {
	return presetJSON{Name: name, Provider: p.Provider, Model: p.Model, Prompt: p.Prompt, Args: p.Args, Agent: p.Name}
}

func (s *Server) handleAPIGetPreset(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	p, ok := s.presetCfg(name)
	if !ok {
		httpError(w, http.StatusNotFound, "unknown preset")
		return
	}
	writeJSON(w, http.StatusOK, presetToJSON(name, p))
}

// presetPatchJSON: absent keys are kept, "" clears.
type presetPatchJSON struct {
	Provider *string `json:"provider"`
	Model    *string `json:"model"`
	Prompt   *string `json:"prompt"`
	Args     *string `json:"args"`
	Agent    *string `json:"agent_name"`
}

func (s *Server) handleAPIPutPreset(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" || !validModel(name) {
		httpError(w, http.StatusBadRequest, "bad preset name")
		return
	}
	var req presetPatchJSON
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	next, _ := s.presetCfg(name) // zero value when new
	if req.Provider != nil {
		next.Provider = *req.Provider
	}
	if req.Model != nil {
		next.Model = *req.Model
	}
	if req.Prompt != nil {
		next.Prompt = *req.Prompt
	}
	if req.Args != nil {
		next.Args = *req.Args
	}
	if req.Agent != nil {
		next.Name = *req.Agent
	}
	if next.Provider == "" {
		httpError(w, http.StatusUnprocessableEntity, "provider is required")
		return
	}
	if _, ok := s.providerCfg(next.Provider); !ok {
		httpError(w, http.StatusUnprocessableEntity, "unknown provider "+next.Provider)
		return
	}
	if s.configPath == "" {
		httpError(w, http.StatusBadRequest, "global config path not configured")
		return
	}
	doc, err := os.ReadFile(s.configPath)
	if err != nil && !os.IsNotExist(err) {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out, err := config.SetPreset(doc, name, config.PresetPatch{
		Provider: req.Provider, Model: req.Model, Prompt: req.Prompt, Args: req.Args, Name: req.Agent,
	})
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := writeFileAtomic(s.configPath, out); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setPresetCfg(name, next)
	writeJSON(w, http.StatusOK, presetToJSON(name, next))
}
