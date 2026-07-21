// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// SSO/OIDC HTTP layer (EE, gated by FeatureSSO): the public login flow
// (providers list → start → callback) and the admin provider/claim-mapping
// CRUD. The protocol bits use coreos/go-oidc for ID-token verification and
// x/oauth2 for the authorization-code + PKCE exchange; persistence + the
// role/team re-sync live in identity/sso.go. See docs/sso.md.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/pkg/license"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

// oidcProviderCache memoises discovery (issuer → *oidc.Provider). Issuer config
// is effectively static; a process-lifetime cache avoids a discovery round-trip
// per login.
var oidcProviderCache sync.Map

func oidcProvider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	if v, ok := oidcProviderCache.Load(issuer); ok {
		return v.(*oidc.Provider), nil
	}
	p, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	oidcProviderCache.Store(issuer, p)
	return p, nil
}

func (h *Handlers) ssoConfig(p identity.AuthProvider, prov *oidc.Provider, redirectURI string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     p.ClientID,
		ClientSecret: p.ClientSecret,
		Endpoint:     prov.Endpoint(),
		RedirectURL:  redirectURI,
		Scopes:       ssoScopes(p.Scopes),
	}
}

func ssoScopes(s string) []string {
	out := strings.Fields(s)
	hasOpenID := false
	for _, sc := range out {
		if sc == oidc.ScopeOpenID {
			hasOpenID = true
		}
	}
	if !hasOpenID {
		out = append([]string{oidc.ScopeOpenID}, out...)
	}
	return out
}

// ssoCallbackURL is the OIDC redirect_uri Sluicio registers and sends. It MUST
// be byte-identical at /start and at /callback, and match an allowed callback
// in the IdP — so it's built from publicBaseURL (SLUICIO_APP_URL when set),
// not the raw request, which varies behind a reverse proxy.
func ssoCallbackURL(r *http.Request) string { return publicBaseURL(r) + "/api/v1/auth/sso/callback" }

// ── public login flow ────────────────────────────────────────────────────

type ssoProviderWire struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ssoProviders: GET /api/v1/auth/sso/providers — pre-auth; the login page
// renders a "Sign in with …" button per enabled provider. Empty when SSO is
// unlicensed (the feature simply doesn't surface).
func (h *Handlers) ssoProviders(w http.ResponseWriter, r *http.Request) {
	if !h.featureEntitled(license.FeatureSSO) {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"providers": []ssoProviderWire{}})
		return
	}
	list, err := h.Identity.ListAllEnabledProviders(r.Context())
	if err != nil {
		h.Logger.Error("sso list providers failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]ssoProviderWire, 0, len(list))
	for _, p := range list {
		out = append(out, ssoProviderWire{ID: p.ID.String(), Name: p.Name})
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"providers": out})
}

// ssoStart: GET /api/v1/auth/sso/{id}/start — builds the IdP authorize URL with
// state + nonce + PKCE and 302s there.
func (h *Handlers) ssoStart(w http.ResponseWriter, r *http.Request) {
	if !h.featureEntitled(license.FeatureSSO) {
		http.NotFound(w, r)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		ssoFail(w, r, "invalid provider")
		return
	}
	p, err := h.Identity.GetAuthProviderByID(r.Context(), id)
	if err != nil || !p.Enabled {
		ssoFail(w, r, "unknown or disabled provider")
		return
	}
	prov, err := oidcProvider(r.Context(), p.IssuerURL)
	if err != nil {
		h.Logger.Error("sso discovery failed", "issuer", p.IssuerURL, "err", err)
		ssoFail(w, r, "could not reach the identity provider")
		return
	}
	state, err1 := randToken(24)
	nonce, err2 := randToken(24)
	if err1 != nil || err2 != nil {
		ssoFail(w, r, "internal error")
		return
	}
	verifier := oauth2.GenerateVerifier()
	st := identity.SSOLoginState{
		State:        state,
		ProviderID:   p.ID,
		Nonce:        nonce,
		CodeVerifier: verifier,
		RedirectTo:   sanitizeReturn(r.URL.Query().Get("return")),
		ExpiresAt:    time.Now().Add(identity.SSOExpiry),
	}
	if err := h.Identity.CreateSSOLoginState(r.Context(), st); err != nil {
		h.Logger.Error("sso state persist failed", "err", err)
		ssoFail(w, r, "internal error")
		return
	}
	cfg := h.ssoConfig(p, prov, ssoCallbackURL(r))
	authURL := cfg.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	http.Redirect(w, r, authURL, http.StatusFound)
}

// ssoCallback: GET /api/v1/auth/sso/callback — verifies the result, links or
// provisions the user, re-syncs role + teams, mints a session.
func (h *Handlers) ssoCallback(w http.ResponseWriter, r *http.Request) {
	if !h.featureEntitled(license.FeatureSSO) {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		ssoFail(w, r, "provider error: "+e)
		return
	}
	st, err := h.Identity.ConsumeSSOLoginState(r.Context(), q.Get("state"))
	if err != nil {
		ssoFail(w, r, "login session expired or invalid — please try again")
		return
	}
	p, err := h.Identity.GetAuthProviderByID(r.Context(), st.ProviderID)
	if err != nil || !p.Enabled {
		ssoFail(w, r, "unknown or disabled provider")
		return
	}
	prov, err := oidcProvider(r.Context(), p.IssuerURL)
	if err != nil {
		ssoFail(w, r, "could not reach the identity provider")
		return
	}
	cfg := h.ssoConfig(p, prov, ssoCallbackURL(r))
	tok, err := cfg.Exchange(r.Context(), q.Get("code"), oauth2.VerifierOption(st.CodeVerifier))
	if err != nil {
		h.Logger.Warn("sso token exchange failed", "err", err)
		ssoFail(w, r, "token exchange failed")
		return
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		ssoFail(w, r, "no id_token returned")
		return
	}
	idTok, err := prov.Verifier(&oidc.Config{ClientID: p.ClientID}).Verify(r.Context(), rawID)
	if err != nil {
		h.Logger.Warn("sso id token verify failed", "err", err)
		ssoFail(w, r, "ID token verification failed")
		return
	}
	if idTok.Nonce != st.Nonce {
		ssoFail(w, r, "nonce mismatch")
		return
	}
	var claims map[string]any
	if err := idTok.Claims(&claims); err != nil {
		ssoFail(w, r, "could not read claims")
		return
	}
	email := strings.TrimSpace(strFromClaims(claims, p.ClaimEmail))
	name := strFromClaims(claims, p.ClaimName)
	sub := idTok.Subject
	if sub == "" {
		sub = strFromClaims(claims, p.ClaimSub)
	}
	groups := stringsFromClaims(claims, p.ClaimGroups)

	userID, err := h.resolveSSOUser(r.Context(), p, sub, email, name)
	if err != nil {
		ssoFail(w, r, err.Error())
		return
	}
	if err := h.Identity.LinkSubject(r.Context(), p.ID, sub, userID); err != nil {
		h.Logger.Error("sso link subject failed", "err", err)
	}
	if err := h.Identity.ApplyClaimMappings(r.Context(), p, userID, groups); err != nil {
		h.Logger.Error("sso apply claim mappings failed", "err", err)
	}
	sess, err := h.Identity.CreateSession(r.Context(), userID, sessionTTL, r.UserAgent())
	if err != nil {
		ssoFail(w, r, "could not start session")
		return
	}
	// Stamp last_login_at like the password flow (finishLogin) does — otherwise
	// SSO-only users always read "Last login — Never".
	if err := h.Identity.TouchLastLogin(r.Context(), userID); err != nil {
		h.Logger.Warn("sso: touch last_login_at failed", "err", err)
	}
	if user, err := h.Identity.GetUserByID(r.Context(), userID); err == nil {
		h.recordAuthAudit(r.Context(), user, nil, "login.succeeded", clientIP(r),
			map[string]any{"method": "sso", "provider": p.Name})
	}
	http.SetCookie(w, sessionCookie(sess.ID, sess.ExpiresAt))
	http.Redirect(w, r, st.RedirectTo, http.StatusFound)
}

// resolveSSOUser finds the user by linked subject, then by verified email, else
// JIT-provisions (when enabled). Returns a user-facing error otherwise.
func (h *Handlers) resolveSSOUser(ctx context.Context, p identity.AuthProvider, sub, email, name string) (uuid.UUID, error) {
	if sub != "" {
		if uid, ok, _ := h.Identity.FindUserBySubject(ctx, p.ID, sub); ok {
			return uid, nil
		}
	}
	if email != "" {
		if u, err := h.Identity.GetUserByEmail(ctx, email); err == nil {
			return u.ID, nil
		}
	}
	if !p.JITProvisioning {
		return uuid.Nil, &ssoError{"no Sluicio account is provisioned for you — ask an admin"}
	}
	if email == "" {
		return uuid.Nil, &ssoError{"the identity provider returned no email, so an account can't be created"}
	}
	u, err := h.Identity.CreateUser(ctx, email, name)
	if err != nil {
		h.Logger.Error("sso jit provision failed", "err", err)
		return uuid.Nil, &ssoError{"account provisioning failed"}
	}
	return u.ID, nil
}

type ssoError struct{ msg string }

func (e *ssoError) Error() string { return e.msg }

// ssoFail bounces the browser back to the SPA login with a surfaced message.
func ssoFail(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/login?sso_error="+url.QueryEscape(msg), http.StatusFound)
}

func sanitizeReturn(v string) string {
	// Only same-site relative paths — never an absolute URL (open-redirect).
	if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
		return v
	}
	return "/"
}

func strFromClaims(claims map[string]any, key string) string {
	if claims == nil || key == "" {
		return ""
	}
	if v, ok := claims[key].(string); ok {
		return v
	}
	return ""
}

func stringsFromClaims(claims map[string]any, key string) []string {
	if claims == nil || key == "" {
		return nil
	}
	switch v := claims[key].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	}
	return nil
}

// ── admin CRUD (gated: admin + FeatureSSO) ───────────────────────────────

type authProviderBody struct {
	Name            string `json:"name"`
	IssuerURL       string `json:"issuer_url"`
	ClientID        string `json:"client_id"`
	ClientSecret    string `json:"client_secret"`
	ClaimEmail      string `json:"claim_email"`
	ClaimName       string `json:"claim_name"`
	ClaimSub        string `json:"claim_sub"`
	ClaimGroups     string `json:"claim_groups"`
	Scopes          string `json:"scopes"`
	DefaultRole     string `json:"default_role"`
	JITProvisioning bool   `json:"jit_provisioning"`
	Enabled         bool   `json:"enabled"`
}

func (b authProviderBody) toProvider(orgID uuid.UUID) identity.AuthProvider {
	return identity.AuthProvider{
		OrgID:           orgID,
		Name:            strings.TrimSpace(b.Name),
		Kind:            "oidc",
		IssuerURL:       strings.TrimSpace(b.IssuerURL),
		ClientID:        strings.TrimSpace(b.ClientID),
		ClientSecret:    b.ClientSecret,
		ClaimEmail:      b.ClaimEmail,
		ClaimName:       b.ClaimName,
		ClaimSub:        b.ClaimSub,
		ClaimGroups:     b.ClaimGroups,
		Scopes:          b.Scopes,
		DefaultRole:     identity.Role(b.DefaultRole),
		JITProvisioning: b.JITProvisioning,
		Enabled:         b.Enabled,
	}
}

func (h *Handlers) listAuthProviders(w http.ResponseWriter, r *http.Request) {
	list, err := h.Identity.ListAuthProviders(r.Context(), middleware.OrgID(r))
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if list == nil {
		list = []identity.AuthProvider{}
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"providers": list})
}

func (h *Handlers) createAuthProvider(w http.ResponseWriter, r *http.Request) {
	var b authProviderBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if b.Name == "" || b.IssuerURL == "" || b.ClientID == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "name, issuer_url and client_id are required")
		return
	}
	p, err := h.Identity.CreateAuthProvider(r.Context(), b.toProvider(middleware.OrgID(r)))
	if err != nil {
		h.Logger.Error("create auth provider failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	h.recordAudit(r, "auth_provider.created", "auth_provider", p.ID.String(), map[string]any{"name": p.Name, "issuer": p.IssuerURL})
	httpserver.WriteJSON(w, http.StatusCreated, p)
}

func (h *Handlers) updateAuthProvider(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid provider id")
		return
	}
	var b authProviderBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p := b.toProvider(middleware.OrgID(r))
	p.ID = id
	updated, err := h.Identity.UpdateAuthProvider(r.Context(), middleware.OrgID(r), p)
	if err != nil {
		h.Logger.Error("update auth provider failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	oidcProviderCache.Delete(updated.IssuerURL) // re-discover on next login
	h.recordAudit(r, "auth_provider.updated", "auth_provider", updated.ID.String(), map[string]any{"name": updated.Name})
	httpserver.WriteJSON(w, http.StatusOK, updated)
}

func (h *Handlers) deleteAuthProvider(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid provider id")
		return
	}
	if err := h.Identity.DeleteAuthProvider(r.Context(), middleware.OrgID(r), id); err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "auth_provider.deleted", "auth_provider", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// claim mappings — org-scoped via the parent provider.

func (h *Handlers) providerInOrg(r *http.Request) (identity.AuthProvider, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return identity.AuthProvider{}, false
	}
	p, err := h.Identity.GetAuthProvider(r.Context(), middleware.OrgID(r), id)
	if err != nil {
		return identity.AuthProvider{}, false
	}
	return p, true
}

func (h *Handlers) listClaimMappings(w http.ResponseWriter, r *http.Request) {
	p, ok := h.providerInOrg(r)
	if !ok {
		httpserver.WriteError(w, http.StatusNotFound, "provider not found")
		return
	}
	list, err := h.Identity.ListClaimMappings(r.Context(), p.ID)
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if list == nil {
		list = []identity.ClaimMapping{}
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"mappings": list})
}

func (h *Handlers) createClaimMapping(w http.ResponseWriter, r *http.Request) {
	p, ok := h.providerInOrg(r)
	if !ok {
		httpserver.WriteError(w, http.StatusNotFound, "provider not found")
		return
	}
	var b struct {
		ClaimValue string  `json:"claim_value"`
		OrgRole    string  `json:"org_role"`
		GroupID    *string `json:"group_id"`
		GroupRole  string  `json:"group_role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(b.ClaimValue) == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "claim_value is required")
		return
	}
	m := identity.ClaimMapping{ProviderID: p.ID, ClaimValue: strings.TrimSpace(b.ClaimValue), OrgRole: identity.Role(b.OrgRole), GroupRole: identity.Role(b.GroupRole)}
	if b.GroupID != nil && *b.GroupID != "" {
		gid, err := uuid.Parse(*b.GroupID)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid group_id")
			return
		}
		m.GroupID = &gid
	}
	if m.OrgRole == "" && m.GroupID == nil {
		httpserver.WriteError(w, http.StatusBadRequest, "set an org_role and/or a group_id")
		return
	}
	created, err := h.Identity.CreateClaimMapping(r.Context(), m)
	if err != nil {
		h.Logger.Error("create claim mapping failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, created)
}

func (h *Handlers) deleteClaimMapping(w http.ResponseWriter, r *http.Request) {
	p, ok := h.providerInOrg(r)
	if !ok {
		httpserver.WriteError(w, http.StatusNotFound, "provider not found")
		return
	}
	mid, err := uuid.Parse(r.PathValue("mid"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid mapping id")
		return
	}
	if err := h.Identity.DeleteClaimMapping(r.Context(), p.ID, mid); err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
