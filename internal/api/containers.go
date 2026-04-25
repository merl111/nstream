package api

import (
	"fmt"
	"net/http"
	"time"

	"nstream/internal/neofs"
)

func (a *API) handleListContainers(w http.ResponseWriter, r *http.Request) {
	cs, err := a.db.ListContainers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type row struct {
		ID            int64   `json:"id"`
		CID           string  `json:"cid"`
		Name          string  `json:"name"`
		ScanEnabled   bool    `json:"scan_enabled"`
		LastScannedAt *string `json:"last_scanned_at"`
		CreatedAt     string  `json:"created_at"`
	}
	out := make([]row, len(cs))
	for i, c := range cs {
		r := row{
			ID:          c.ID,
			CID:         c.CID,
			Name:        c.Name,
			ScanEnabled: c.ScanEnabled,
			CreatedAt:   c.CreatedAt.Format(time.RFC3339),
		}
		if c.LastScannedAt != nil {
			s := c.LastScannedAt.Format(time.RFC3339)
			r.LastScannedAt = &s
		}
		out[i] = r
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleAddContainer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		CID  string `json:"cid"`
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil || req.CID == "" {
		writeError(w, http.StatusBadRequest, "cid and name required")
		return
	}
	if req.Name == "" {
		req.Name = req.CID
	}
	c, err := a.db.AddContainer(r.Context(), req.CID, req.Name)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (a *API) handleDeleteContainer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "DELETE required")
		return
	}
	idStr := pathSegment(r.URL.Path, "/api/v1/containers/")
	var id int64
	if _, err := parseID(idStr, &id); err != nil {
		writeError(w, http.StatusBadRequest, "invalid container id")
		return
	}
	if err := a.db.DeleteContainer(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCreateNeoFSContainer creates a brand-new NeoFS container and registers
// it in the local library. POST /api/v1/containers/create
func (a *API) handleCreateNeoFSContainer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Name       string `json:"name"`
		Replicas   uint32 `json:"replicas"`
		PublicRead bool   `json:"public_read"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Replicas == 0 {
		req.Replicas = 2
	}

	result, err := a.nfs.CreateContainer(r.Context(), neofs.CreateContainerOpts{
		Name:       req.Name,
		Replicas:   req.Replicas,
		PublicRead: req.PublicRead,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "neofs: "+err.Error())
		return
	}

	a.log.Info("container created on NeoFS",
		"cid", result.CID,
		"acl", fmt.Sprintf("0x%08X", result.ACLBits),
		"public_read", req.PublicRead,
	)

	c, err := a.db.AddContainer(r.Context(), result.CID.String(), req.Name)
	if err != nil {
		// Container was created on NeoFS but DB insert failed; still return the CID.
		writeJSON(w, http.StatusCreated, map[string]string{
			"cid":     result.CID.String(),
			"warning": "created on NeoFS but DB registration failed: " + err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (a *API) handleScanContainer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	// path: /api/v1/containers/{id}/scan
	seg := pathSegment(r.URL.Path, "/api/v1/containers/")
	// strip trailing /scan
	idStr := seg
	if len(idStr) > 5 && idStr[len(idStr)-5:] == "/scan" {
		idStr = idStr[:len(idStr)-5]
	}
	var id int64
	if _, err := parseID(idStr, &id); err != nil {
		writeError(w, http.StatusBadRequest, "invalid container id")
		return
	}
	c, err := a.db.GetContainerByID(r.Context(), id)
	if err != nil || c == nil {
		writeError(w, http.StatusNotFound, "container not found")
		return
	}
	// Trigger async scan; scanner is injected via a.scanner.
	if a.scanner != nil {
		go a.scanner.ScanContainer(*c)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "scan started"})
}
