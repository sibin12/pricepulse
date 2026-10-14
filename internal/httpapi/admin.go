package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"pricepulse/internal/domain"
	"pricepulse/internal/repository"
)

// Admin dashboard endpoints. Every route here is wrapped in requireAdmin.

func (h *handler) adminStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	stats, err := h.users.Stats(ctx)
	if err != nil {
		h.serverError(w, "admin stats", err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h *handler) adminGetSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	s, err := h.users.Settings(ctx)
	if err != nil {
		h.serverError(w, "get settings", err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// adminUpdateSettings replaces the global defaults that apply to every user
// without a personal override. Changes take effect immediately (the worker
// reads intervals from the database on every poll).
func (h *handler) adminUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Defaults                   domain.Limits `json:"defaults"`
		SignupsOpen                bool          `json:"signups_open"`
		PublicLookups              bool          `json:"public_lookups"`
		PublicCheckIntervalMinutes int           `json:"public_check_interval_minutes"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	d := req.Defaults
	if msg := validateMaxProducts(d.MaxProducts); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	if msg := validateInterval(d.CheckIntervalMinutes); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	if msg := validateInterval(req.PublicCheckIntervalMinutes); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, "public "+msg)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	s, err := h.users.UpdateSettings(ctx, domain.AppSettings{
		Defaults:                   d,
		SignupsOpen:                req.SignupsOpen,
		PublicLookups:              req.PublicLookups,
		PublicCheckIntervalMinutes: req.PublicCheckIntervalMinutes,
	})
	if err != nil {
		h.serverError(w, "update settings", err)
		return
	}
	h.logger.Printf("admin %s updated global settings: %+v signups_open=%v public_lookups=%v public_interval=%dm",
		currentUser(r).Email, s.Defaults, s.SignupsOpen, s.PublicLookups, s.PublicCheckIntervalMinutes)
	writeJSON(w, http.StatusOK, s)
}

func (h *handler) adminListUsers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	users, err := h.users.ListUsers(ctx)
	if err != nil {
		h.serverError(w, "list users", err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

// adminGetUser returns one user plus their products, for the edit page.
func (h *handler) adminGetUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	u, err := h.users.AdminUser(ctx, id)
	if err != nil {
		h.userError(w, "get user", err)
		return
	}
	products, err := h.repo.List(ctx, id)
	if err != nil {
		h.serverError(w, "list user products", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "products": products})
}

// adminUpdateUser sets role, suspension and overrides. Overrides are sent in
// full every time: null means "use the global default", so the admin UI can
// switch any setting back to the default.
func (h *handler) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Role      domain.Role      `json:"role"`
		Disabled  bool             `json:"disabled"`
		Overrides domain.Overrides `json:"overrides"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Role != domain.RoleUser && req.Role != domain.RoleAdmin {
		writeError(w, http.StatusUnprocessableEntity, `role must be "user" or "admin"`)
		return
	}
	// Admins can't demote or suspend themselves, so there is always at least
	// one working admin and nobody locks themselves out by accident.
	if id == currentUser(r).ID && (req.Role != domain.RoleAdmin || req.Disabled) {
		writeError(w, http.StatusUnprocessableEntity, "you can't remove your own admin role or suspend your own account")
		return
	}
	o := req.Overrides
	if o.MaxProducts != nil {
		if msg := validateMaxProducts(*o.MaxProducts); msg != "" {
			writeError(w, http.StatusUnprocessableEntity, msg)
			return
		}
	}
	if o.CheckIntervalMinutes != nil {
		if msg := validateInterval(*o.CheckIntervalMinutes); msg != "" {
			writeError(w, http.StatusUnprocessableEntity, msg)
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	u, err := h.users.UpdateUser(ctx, id, req.Role, req.Disabled, o)
	if err != nil {
		h.userError(w, "update user", err)
		return
	}
	h.logger.Printf("admin %s updated user %d (%s): role=%s disabled=%v", currentUser(r).Email, u.ID, u.Email, u.Role, u.Disabled)
	writeJSON(w, http.StatusOK, u)
}

func (h *handler) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if id == currentUser(r).ID {
		writeError(w, http.StatusUnprocessableEntity, "you can't delete your own account")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	if err := h.users.DeleteUser(ctx, id); err != nil {
		h.userError(w, "delete user", err)
		return
	}
	h.logger.Printf("admin %s deleted user %d", currentUser(r).Email, id)
	w.WriteHeader(http.StatusNoContent)
}

// userError maps ErrNotFound to a 404 about users (repoError says "product").
func (h *handler) userError(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	h.serverError(w, op, err)
}

func validateMaxProducts(n int) string {
	if n < 0 || n > domain.MaxProductsLimit {
		return fmt.Sprintf("max products must be between 0 and %d", domain.MaxProductsLimit)
	}
	return ""
}

func validateInterval(m int) string {
	if m < domain.MinCheckIntervalMinutes || m > domain.MaxCheckIntervalMinutes {
		return fmt.Sprintf("check interval must be between %d minutes and %d minutes (7 days)",
			domain.MinCheckIntervalMinutes, domain.MaxCheckIntervalMinutes)
	}
	return ""
}
