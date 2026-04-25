package api

import (
	"net/http"
	"time"
)

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	user, err := a.db.Authenticate(r.Context(), req.Username, req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if user == nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	sess, err := a.db.CreateSession(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    sess.Token,
		Expires:  sess.ExpiresAt,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       user.ID,
		"username": user.Username,
		"role":     user.Role,
	})
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	cookie, err := r.Cookie("session")
	if err == nil && cookie.Value != "" {
		_ = a.db.DeleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:    "session",
		Value:   "",
		Expires: time.Unix(0, 0),
		Path:    "/",
		MaxAge:  -1,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       u.ID,
		"username": u.Username,
		"role":     u.Role,
	})
}

func (a *API) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Role == "" {
		req.Role = "viewer"
	}
	u, err := a.db.CreateUser(r.Context(), req.Username, req.Password, req.Role)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": u.ID, "username": u.Username, "role": u.Role,
	})
}

func (a *API) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.db.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type row struct {
		ID        int64  `json:"id"`
		Username  string `json:"username"`
		Role      string `json:"role"`
		CreatedAt string `json:"created_at"`
	}
	out := make([]row, len(users))
	for i, u := range users {
		out[i] = row{u.ID, u.Username, u.Role, u.CreatedAt.Format(time.RFC3339)}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "DELETE required")
		return
	}
	idStr := pathSegment(r.URL.Path, "/api/v1/users/")
	var id int64
	if _, err := parseID(idStr, &id); err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	me := userFromCtx(r.Context())
	if me.ID == id {
		writeError(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}
	if err := a.db.DeleteUser(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
