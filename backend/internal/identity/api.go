package identity

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/authn"
	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
)

// maxUserAgentLength bounds what is stored alongside a session.
const maxUserAgentLength = 200

// API exposes the identity use cases over HTTP.
type API struct {
	service *Service
	issuer  *TokenIssuer
}

// NewAPI builds the HTTP layer of the identity service.
func NewAPI(service *Service, issuer *TokenIssuer) *API {
	return &API{service: service, issuer: issuer}
}

// Routes registers the endpoints. Everything under /auth/me requires a valid
// access token; the rest is how a token is obtained in the first place.
func (a *API) Routes(mux *http.ServeMux, verifier *authn.Verifier) {
	mux.HandleFunc("POST /auth/register", a.register)
	mux.HandleFunc("POST /auth/login", a.login)
	mux.HandleFunc("POST /auth/refresh", a.refresh)
	mux.HandleFunc("POST /auth/logout", a.logout)
	mux.HandleFunc("GET /.well-known/jwks.json", a.jwks)

	authenticated := authn.RequireUser(verifier)
	mux.Handle("GET /auth/me", authenticated(http.HandlerFunc(a.profile)))
	mux.Handle("DELETE /auth/me", authenticated(http.HandlerFunc(a.deleteAccount)))
}

type registerRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type deleteAccountRequest struct {
	Password string `json:"password"`
}

type sessionResponse struct {
	User         userResponse `json:"user"`
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	TokenType    string       `json:"token_type"`
	ExpiresIn    int          `json:"expires_in"`
}

type userResponse struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var request registerRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	user, tokens, err := a.service.Register(r.Context(), request.Email, request.Name, request.Password, userAgentOf(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusCreated, toSessionResponse(user, tokens))
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	user, tokens, err := a.service.Login(r.Context(), request.Email, request.Password, userAgentOf(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, toSessionResponse(user, tokens))
}

func (a *API) refresh(w http.ResponseWriter, r *http.Request) {
	var request refreshRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	user, tokens, err := a.service.Refresh(r.Context(), request.RefreshToken, userAgentOf(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, toSessionResponse(user, tokens))
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	var request refreshRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	if err := a.service.Logout(r.Context(), request.RefreshToken); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (a *API) profile(w http.ResponseWriter, r *http.Request) {
	userID, err := authn.UserIDFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	user, err := a.service.Profile(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, toUserResponse(user))
}

func (a *API) deleteAccount(w http.ResponseWriter, r *http.Request) {
	userID, err := authn.UserIDFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	var request deleteAccountRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if request.Password == "" {
		httpx.WriteError(w, r, apperr.Invalid("password_required",
			"Confirm your password to delete the account.").
			WithDetails(map[string]string{"password": "must not be empty"}))
		return
	}

	if err := a.service.DeleteAccount(r.Context(), userID, request.Password); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusNoContent, nil)
}

// jwks publishes the public key the other services verify tokens with.
func (a *API) jwks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=300")
	httpx.WriteJSON(w, r, http.StatusOK, a.issuer.PublicJWKS())
}

func toSessionResponse(user User, tokens TokenPair) sessionResponse {
	return sessionResponse{
		User:         toUserResponse(user),
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(tokens.ExpiresIn.Seconds()),
	}
}

func toUserResponse(user User) userResponse {
	return userResponse{ID: user.ID, Email: user.Email, Name: user.Name, CreatedAt: user.CreatedAt}
}

// userAgentOf keeps a short note of the client that started the session, which
// helps someone reading the sessions of an account later.
func userAgentOf(r *http.Request) string {
	agent := r.Header.Get("User-Agent")
	if len(agent) > maxUserAgentLength {
		return agent[:maxUserAgentLength]
	}
	return agent
}
