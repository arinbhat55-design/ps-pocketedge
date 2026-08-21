package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// userResponse is store.User minus the password hash.
type userResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

func toUserResponse(u store.User) userResponse {
	return userResponse{
		ID:        u.ID,
		Email:     u.Email,
		Role:      u.Role,
		CreatedAt: u.CreatedAt,
	}
}

func isValidRole(role string) bool {
	return role == "admin" || role == "viewer"
}

// handleGetMe returns the caller's own user record, letting the Flutter app
// learn its role without decoding the JWT client-side.
func handleGetMe(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := auth.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		user, err := st.GetUserByID(r.Context(), claims.UserID)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load user", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, toUserResponse(*user))
	}
}

// handleChangePassword lets the caller change their own password, given
// their current one.
func handleChangePassword(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := auth.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if len(req.NewPassword) < 8 {
			http.Error(w, "new password must be at least 8 characters", http.StatusBadRequest)
			return
		}

		user, err := st.GetUserByID(r.Context(), claims.UserID)
		if err != nil {
			log.Error("failed to load user", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if !auth.CheckPassword(user.PasswordHash, req.CurrentPassword) {
			http.Error(w, "current password is incorrect", http.StatusUnauthorized)
			return
		}

		hash, err := auth.HashPassword(req.NewPassword)
		if err != nil {
			log.Error("failed to hash password", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if err := st.UpdateUserPassword(r.Context(), user.ID, hash); err != nil {
			log.Error("failed to update password", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func handleListUsers(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		users, err := st.ListUsers(r.Context())
		if err != nil {
			log.Error("failed to list users", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		resp := make([]userResponse, len(users))
		for i, u := range users {
			resp[i] = toUserResponse(u)
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleCreateUser(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Email == "" || len(req.Password) < 8 {
			http.Error(w, "email is required and password must be at least 8 characters", http.StatusBadRequest)
			return
		}
		if !isValidRole(req.Role) {
			http.Error(w, "role must be 'admin' or 'viewer'", http.StatusBadRequest)
			return
		}

		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			log.Error("failed to hash password", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		id, err := st.CreateUser(r.Context(), req.Email, hash, req.Role)
		if errors.Is(err, store.ErrDuplicateEmail) {
			http.Error(w, "email already in use", http.StatusConflict)
			return
		}
		if err != nil {
			log.Error("failed to create user", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusCreated, map[string]string{"id": id})
	}
}

// handleUpdateUserRole changes a user's role, refusing to demote the last
// remaining admin.
func handleUpdateUserRole(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		Role string `json:"role"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if !isValidRole(req.Role) {
			http.Error(w, "role must be 'admin' or 'viewer'", http.StatusBadRequest)
			return
		}

		target, err := st.GetUserByID(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load user", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if target.Role == "admin" && req.Role != "admin" {
			adminCount, err := st.CountAdmins(r.Context())
			if err != nil {
				log.Error("failed to count admins", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			if adminCount <= 1 {
				http.Error(w, "cannot demote the last remaining admin", http.StatusBadRequest)
				return
			}
		}

		if err := st.UpdateUserRole(r.Context(), id, req.Role); err != nil {
			log.Error("failed to update user role", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// handleResetUserPassword lets an admin set another user's password
// directly — there's no email channel to deliver a reset link through in
// this self-hosted tool, same reasoning as SeedAdmin's generated-password
// fallback.
func handleResetUserPassword(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		Password string `json:"password"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if len(req.Password) < 8 {
			http.Error(w, "password must be at least 8 characters", http.StatusBadRequest)
			return
		}

		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			log.Error("failed to hash password", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if err := st.UpdateUserPassword(r.Context(), id, hash); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "user not found", http.StatusNotFound)
				return
			}
			log.Error("failed to reset password", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// handleDeleteUser refuses to let a caller delete their own account or
// delete the last remaining admin.
func handleDeleteUser(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := auth.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := r.PathValue("id")
		if id == claims.UserID {
			http.Error(w, "cannot delete your own account", http.StatusBadRequest)
			return
		}

		target, err := st.GetUserByID(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load user", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if target.Role == "admin" {
			adminCount, err := st.CountAdmins(r.Context())
			if err != nil {
				log.Error("failed to count admins", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			if adminCount <= 1 {
				http.Error(w, "cannot delete the last remaining admin", http.StatusBadRequest)
				return
			}
		}

		if err := st.DeleteUser(r.Context(), id); err != nil {
			log.Error("failed to delete user", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
