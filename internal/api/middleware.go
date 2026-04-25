package api

import (
	"context"
	"net/http"

	"nstream/internal/db"
)

type contextKey int

const (
	ctxUser contextKey = iota
)

// userFromCtx retrieves the authenticated user from the request context.
func userFromCtx(ctx context.Context) *db.User {
	u, _ := ctx.Value(ctxUser).(*db.User)
	return u
}

// requireAuth is a middleware that validates the session cookie and injects the
// user into the request context. Responds 401 if missing or invalid.
func (a *API) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		sess, err := a.db.GetSession(r.Context(), cookie.Value)
		if err != nil || sess == nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		user, err := a.db.GetUserByID(r.Context(), sess.UserID)
		if err != nil || user == nil {
			writeError(w, http.StatusUnauthorized, "user not found")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireAdmin is a middleware that additionally checks the user has admin role.
func (a *API) requireAdmin(next http.Handler) http.Handler {
	return a.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := userFromCtx(r.Context())
		if u == nil || u.Role != db.RoleAdmin {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// chain composes middlewares left-to-right.
func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
