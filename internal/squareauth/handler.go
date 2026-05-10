// Handler — chi routes for the Square OAuth demo flow.
//
//	GET  /                            landing page with "Connect Square" button
//	GET  /auth/square                 OAuth start; sets state cookie; redirects to Square
//	GET  /auth/square/callback        OAuth callback; exchanges code; stores token; sets session cookie; redirects to /dashboard
//	GET  /dashboard                   server-rendered dashboard reading the connected merchant's Square data
//	POST /auth/square/disconnect      delete the stored token; clear session cookie; redirect to /
//
// Session is a HttpOnly, HMAC-SHA256-signed cookie carrying the internal
// merchant_id (UUID) plus a signature keyed on SESSION_SECRET. Cookie
// format: "<uuid>.<base64url-sig>". CSRF state is a separate short-lived
// HttpOnly cookie carrying a hash of the random state value sent to
// Square. Cookie signing landed in GRO-857 / Sprint 2 T-D — closes Sec C5
// (forging the plaintext UUID would otherwise let any caller load any
// merchant's Square data including last-4 of card).
package squareauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	webcookie "github.com/ruptiv/canary/internal/web/cookie"
)

const (
	sessionCookieName = "demo_merchant"
	stateCookieName   = "square_oauth_state"
	sessionMaxAge     = 7 * 24 * 3600 // 7 days
	stateMaxAge       = 5 * 60        // 5 minutes
)

// Mount registers the demo + OAuth routes on r. All routes are public —
// the demo is OAuth-gated, not API-key gated.
func (s *Service) Mount(r chi.Router) {
	r.Get("/", s.handleLanding)
	r.Get("/auth/square", s.handleAuthorize)
	r.Get("/auth/square/callback", s.handleCallback)
	r.Get("/dashboard", s.handleDashboard)
	r.Post("/auth/square/disconnect", s.handleDisconnect)
	// /auth/demo is mounted unconditionally — the handler itself
	// gates on DEV_DEMO_LOGIN=1 and 404s when disabled. Mounting
	// unconditionally keeps the route table stable across deploys
	// (operators reading routes-seen.json don't see the route appear
	// and disappear based on env state).
	r.Get("/auth/demo", s.handleDevDemoLogin)
}

// devDemoMerchantID is the seeded "Acme Main Street (Square)" merchant
// from deploy/schema/99_seed.sql. The dev demo login signs a cookie
// pointing at this UUID. Operators must have run `make db-seed` so this
// row exists; without it requireTenantMiddleware will resolve OK but
// merchant lookups will return empty data.
const devDemoMerchantID = "33333333-0000-0000-0000-000000000001"

// DevDemoLoginEnabled reports whether the dev-only demo login bypass is
// turned on. Surfaced so the web login page can conditionally render the
// "Demo Login" button. Production never sets this env var.
func (s *Service) DevDemoLoginEnabled() bool {
	v := os.Getenv("DEV_DEMO_LOGIN")
	return v == "1" || v == "true" || v == "TRUE"
}

// handleDevDemoLogin is a dev-only login bypass. When DEV_DEMO_LOGIN=1
// is set on the gateway, this signs a session cookie for the seeded
// demo merchant (33333333-0000-0000-0000-000000000001) and redirects
// to /dashboard. Without the env flag the route returns 404 — production
// gateways simply behave as if it doesn't exist.
//
// Use case: local dev without Square sandbox credentials. Without this
// path, operators who haven't wired SQUARE_APPLICATION_ID etc into
// compose can't get a session and the merchant pages are unreachable.
//
// Security: gated on a process-wide env var, not user-controllable. The
// 404 response when disabled doesn't reveal the route exists. Production
// images run with DEV_DEMO_LOGIN unset.
func (s *Service) handleDevDemoLogin(w http.ResponseWriter, r *http.Request) {
	if !s.DevDemoLoginEnabled() {
		http.NotFound(w, r)
		return
	}
	merchantID, err := uuid.Parse(devDemoMerchantID)
	if err != nil {
		// Static UUID; should never fail to parse. Defensive.
		s.logger.Error("squareauth demo: parsing static UUID failed", zap.Error(err))
		http.Error(w, "demo merchant id parse failed", http.StatusInternalServerError)
		return
	}
	webcookie.Set(w, webcookie.Spec{
		Name:     sessionCookieName,
		Value:    s.signCookieValue(merchantID),
		Path:     "/",
		MaxAge:   sessionMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	s.logger.Info("squareauth demo: signed dev session cookie",
		zap.String("merchant_id", merchantID.String()))
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

// ─── Landing ────────────────────────────────────────────────────────────────

func (s *Service) handleLanding(w http.ResponseWriter, r *http.Request) {
	connected := false
	if mID, ok := s.merchantFromCookie(r); ok {
		_, err := s.LoadToken(r.Context(), mID)
		if err == nil {
			connected = true
		}
	}
	data := map[string]any{
		"Connected":   connected,
		"Environment": s.cfg.Environment,
	}
	renderHTML(w, landingTmpl, data)
}

// ─── OAuth start ───────────────────────────────────────────────────────────

func (s *Service) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ApplicationID == "" || s.cfg.ApplicationSecret == "" || s.cfg.RedirectURI == "" {
		http.Error(w, "Square OAuth not configured (SQUARE_APPLICATION_ID / SECRET / REDIRECT_URI missing)",
			http.StatusServiceUnavailable)
		return
	}
	state := NewState()
	hashed := HashState(state)

	webcookie.Set(w, webcookie.Spec{
		Name:     stateCookieName,
		Value:    hashed,
		Path:     "/",
		MaxAge:   stateMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, s.AuthorizeURL(state), http.StatusFound)
}

// ─── OAuth callback ────────────────────────────────────────────────────────

func (s *Service) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	if errStr := q.Get("error"); errStr != "" {
		http.Error(w, "Square OAuth error: "+errStr+" / "+q.Get("error_description"),
			http.StatusBadRequest)
		return
	}

	code := q.Get("code")
	state := q.Get("state")
	if code == "" || state == "" {
		http.Error(w, "missing code or state", http.StatusBadRequest)
		return
	}

	// Validate CSRF state cookie matches the round-trip state.
	cookie, err := r.Cookie(stateCookieName)
	if err != nil || cookie.Value == "" {
		http.Error(w, "missing state cookie (try the connect flow again)", http.StatusBadRequest)
		return
	}
	if cookie.Value != HashState(state) {
		http.Error(w, "state mismatch (CSRF)", http.StatusBadRequest)
		return
	}
	// Clear state cookie; one-shot.
	webcookie.Set(w, webcookie.Spec{
		Name:   stateCookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	tr, err := s.ExchangeCode(r.Context(), code)
	if err != nil {
		s.logger.Error("squareauth code exchange", zap.Error(err))
		http.Error(w, "code exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	internalMerchantID, err := s.StoreToken(r.Context(), tr)
	if err != nil {
		s.logger.Error("squareauth store token", zap.Error(err))
		http.Error(w, "token storage failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Set session cookie carrying the signed internal merchant_id.
	// HMAC-SHA256 signature prevents cookie forgery.
	webcookie.Set(w, webcookie.Spec{
		Name:     sessionCookieName,
		Value:    s.signCookieValue(internalMerchantID),
		Path:     "/",
		MaxAge:   sessionMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

// ─── Dashboard ─────────────────────────────────────────────────────────────

func (s *Service) handleDashboard(w http.ResponseWriter, r *http.Request) {
	mID, ok := s.merchantFromCookie(r)
	if !ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	creds, err := s.LoadToken(r.Context(), mID)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		s.logger.Error("squareauth load token", zap.Error(err))
		http.Error(w, "load token failed", http.StatusInternalServerError)
		return
	}

	// Auto-refresh when token expires within 5 minutes. Square revokes the
	// old refresh token on success, so we must persist the new pair immediately.
	if creds.IsExpiring(5 * time.Minute) {
		if newTR, err := s.RefreshToken(r.Context(), creds.RefreshToken); err == nil {
			if _, storeErr := s.StoreToken(r.Context(), newTR); storeErr == nil {
				creds.AccessToken = newTR.AccessToken
				s.logger.Info("squareauth token refreshed", zap.String("merchant_id", mID.String()))
			} else {
				s.logger.Warn("squareauth refresh store failed", zap.Error(storeErr))
			}
		} else {
			s.logger.Warn("squareauth refresh failed — proceeding with stored token", zap.Error(err))
		}
	}

	merchant, err := s.GetMerchant(r.Context(), creds.AccessToken, creds.MerchantIDSquare)
	if err != nil {
		s.logger.Error("squareauth get merchant", zap.Error(err))
		http.Error(w, "Square API merchant call failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	locations, err := s.ListLocations(r.Context(), creds.AccessToken)
	if err != nil {
		s.logger.Warn("squareauth list locations", zap.Error(err))
		locations = nil
	}

	payments, err := s.ListPayments(r.Context(), creds.AccessToken, 10)
	if err != nil {
		s.logger.Warn("squareauth list payments", zap.Error(err))
		payments = nil
	}

	data := map[string]any{
		"Merchant":       merchant,
		"Locations":      locations,
		"Payments":       payments,
		"Environment":    s.cfg.Environment,
		"InternalID":     mID.String(),
		"SquareID":       creds.MerchantIDSquare,
		"PaymentsCount":  len(payments),
		"LocationsCount": len(locations),
	}
	renderHTML(w, dashboardTmpl, data)
}

// ─── Disconnect ────────────────────────────────────────────────────────────

func (s *Service) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if mID, ok := s.merchantFromCookie(r); ok {
		_ = s.DeleteToken(r.Context(), mID)
	}
	webcookie.Set(w, webcookie.Spec{
		Name:   sessionCookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// ─── helpers ───────────────────────────────────────────────────────────────

func (s *Service) merchantFromCookie(r *http.Request) (uuid.UUID, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return uuid.Nil, false
	}
	return s.verifyCookieValue(c.Value)
}

// MerchantFromRequest is the exported wrapper around merchantFromCookie.
// Other packages (web, devops) inject this as a resolver so they can
// derive the tenant UUID from the session without importing private
// cookie-handling internals. T-B.
func (s *Service) MerchantFromRequest(r *http.Request) (uuid.UUID, bool) {
	return s.merchantFromCookie(r)
}

// signCookieValue builds the cookie body "<uuid>.<base64url-hmac>".
// HMAC-SHA256 over the UUID string, keyed on s.sessionKey (loaded from
// SESSION_SECRET at construction). When sessionKey is empty (only
// possible in dev where the gateway didn't pass through the env), we
// fall back to the bare UUID for backward compatibility — but log a
// warning. Production deployments must set SESSION_SECRET (already
// required by config.Load).
func (s *Service) signCookieValue(id uuid.UUID) string {
	idStr := id.String()
	if len(s.sessionKey) == 0 {
		s.logger.Warn("squareauth: SESSION_SECRET not loaded; cookie unsigned (dev-only fallback)")
		return idStr
	}
	mac := hmac.New(sha256.New, s.sessionKey)
	mac.Write([]byte(idStr))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return idStr + "." + sig
}

// verifyCookieValue parses "<uuid>.<sig>" and confirms the HMAC.
// Returns (uuid.Nil, false) on any mismatch — callers must NOT treat
// the bare UUID prefix as authoritative when the signature is missing
// or wrong. Constant-time comparison via hmac.Equal.
//
// Dev fallback: when s.sessionKey is empty, the cookie format degrades
// to a bare UUID (signCookieValue emits no separator) and verify
// accepts it. This path only fires when SESSION_SECRET is unset, which
// the gateway's config.Load already requires for production. See
// New() for the warning that fires at construction.
func (s *Service) verifyCookieValue(value string) (uuid.UUID, bool) {
	dot := strings.IndexByte(value, '.')
	if dot < 0 {
		// No signature separator. Two cases:
		//   1. Dev fallback (sessionKey empty) — accept bare UUID.
		//   2. Forged cookie or pre-T-D plaintext UUID — reject.
		if len(s.sessionKey) > 0 {
			return uuid.Nil, false
		}
		id, err := uuid.Parse(value)
		if err != nil {
			return uuid.Nil, false
		}
		return id, true
	}
	idStr, sigStr := value[:dot], value[dot+1:]
	id, err := uuid.Parse(idStr)
	if err != nil {
		return uuid.Nil, false
	}
	if len(s.sessionKey) == 0 {
		// Cookie has a separator but we have no key to verify against.
		// Accept the UUID prefix — degraded dev mode. Production never
		// reaches here because SESSION_SECRET is required.
		return id, true
	}
	wantSig, err := base64.RawURLEncoding.DecodeString(sigStr)
	if err != nil {
		return uuid.Nil, false
	}
	mac := hmac.New(sha256.New, s.sessionKey)
	mac.Write([]byte(idStr))
	gotSig := mac.Sum(nil)
	if !hmac.Equal(gotSig, wantSig) {
		return uuid.Nil, false
	}
	return id, true
}

func renderHTML(w http.ResponseWriter, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

// ─── templates (inline; reuse internal/web idioms in a later pass) ─────────

var landingTmpl = template.Must(template.New("landing").Funcs(tmplFuncs).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <title>Canary — Live Demo</title>
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="max-w-xl mx-auto mt-16 px-6 text-gray-900 leading-relaxed">
  <h1 class="text-2xl font-semibold mb-1">
    Canary — running on GCP
    <span class="inline-block px-2 py-0.5 rounded text-sm bg-amber-100 text-amber-800 font-normal ml-1">{{.Environment}}</span>
  </h1>
  <p class="text-gray-500 mb-8">Multi-POS retail platform. Connect your Square sandbox account to see your data.</p>

  {{if .Connected}}
    <p>You're connected. <a href="/dashboard" class="text-blue-600 underline">Open dashboard →</a></p>
  {{else}}
    <p>
      <a href="/auth/square"
         class="inline-block px-5 py-2 bg-blue-600 text-white rounded-md font-semibold no-underline hover:bg-blue-700 transition-colors">
        Connect Square
      </a>
    </p>
  {{end}}

  <div class="mt-16 pt-4 border-t border-gray-200 text-gray-400 text-sm space-x-3">
    <a href="https://canary.growdirect.io" class="hover:text-gray-600 no-underline">Architecture →</a>
    <span>·</span>
    <a href="https://canary.growdirect.io/sdds/" class="hover:text-gray-600 no-underline">SDDs</a>
    <span>·</span>
    <a href="https://github.com/ruptiv/canary" class="hover:text-gray-600 no-underline">GitHub</a>
    <span>·</span>
    <span>GrowDirect LLC · sandbox · token storage encrypted at rest</span>
  </div>
</body>
</html>`))

var dashboardTmpl = template.Must(template.New("dashboard").Funcs(tmplFuncs).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <title>Canary Dashboard — {{if .Merchant}}{{.Merchant.BusinessName}}{{end}}</title>
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="max-w-5xl mx-auto mt-8 px-6 text-gray-900 leading-normal">

  {{if .Merchant}}
  <h1 class="text-2xl font-semibold mb-1">{{.Merchant.BusinessName}}</h1>
  <p class="text-gray-500 text-sm mb-6 space-x-1">
    <span class="inline-block px-2 py-0.5 rounded text-xs bg-amber-100 text-amber-800">{{.Environment}}</span>
    {{if eq .Merchant.Status "ACTIVE"}}
    <span class="inline-block px-2 py-0.5 rounded text-xs bg-emerald-100 text-emerald-800">{{.Merchant.Status}}</span>
    {{else}}
    <span class="inline-block px-2 py-0.5 rounded text-xs bg-gray-100 text-gray-700">{{.Merchant.Status}}</span>
    {{end}}
    <span>{{.Merchant.Country}} · {{.Merchant.Currency}} · {{.Merchant.LanguageCode}}</span>
  </p>
  <p class="text-gray-400 text-xs mb-6">
    Square ID: <code class="font-mono">{{.SquareID}}</code> ·
    Internal ID: <code class="font-mono">{{.InternalID}}</code>
  </p>
  {{end}}

  <h2 class="text-sm font-semibold uppercase tracking-wide text-gray-500 border-b border-gray-200 pb-1 mt-8">
    Locations ({{.LocationsCount}})
  </h2>
  {{if .Locations}}
  <table class="w-full border-collapse text-sm mt-2 mb-6">
    <thead>
      <tr>
        <th class="text-left px-2 py-2 text-xs font-semibold text-gray-500 uppercase tracking-wide border-b border-gray-100">Name</th>
        <th class="text-left px-2 py-2 text-xs font-semibold text-gray-500 uppercase tracking-wide border-b border-gray-100">Status</th>
        <th class="text-left px-2 py-2 text-xs font-semibold text-gray-500 uppercase tracking-wide border-b border-gray-100">Type</th>
        <th class="text-left px-2 py-2 text-xs font-semibold text-gray-500 uppercase tracking-wide border-b border-gray-100">City</th>
        <th class="text-left px-2 py-2 text-xs font-semibold text-gray-500 uppercase tracking-wide border-b border-gray-100">Region</th>
        <th class="text-left px-2 py-2 text-xs font-semibold text-gray-500 uppercase tracking-wide border-b border-gray-100">Country</th>
      </tr>
    </thead>
    <tbody>
      {{range .Locations}}
      <tr class="hover:bg-gray-50">
        <td class="px-2 py-2 border-b border-gray-100">{{.Name}}</td>
        <td class="px-2 py-2 border-b border-gray-100">{{.Status}}</td>
        <td class="px-2 py-2 border-b border-gray-100">{{.Type}}</td>
        <td class="px-2 py-2 border-b border-gray-100">{{.Address.Locality}}</td>
        <td class="px-2 py-2 border-b border-gray-100">{{.Address.Region}}</td>
        <td class="px-2 py-2 border-b border-gray-100">{{.Country}}</td>
      </tr>
      {{end}}
    </tbody>
  </table>
  {{else}}
  <p class="text-gray-400 text-sm mt-2 mb-6">No locations.</p>
  {{end}}

  <h2 class="text-sm font-semibold uppercase tracking-wide text-gray-500 border-b border-gray-200 pb-1 mt-8">
    Recent payments ({{.PaymentsCount}})
  </h2>
  {{if .Payments}}
  <table class="w-full border-collapse text-sm mt-2 mb-6">
    <thead>
      <tr>
        <th class="text-left px-2 py-2 text-xs font-semibold text-gray-500 uppercase tracking-wide border-b border-gray-100">When</th>
        <th class="text-left px-2 py-2 text-xs font-semibold text-gray-500 uppercase tracking-wide border-b border-gray-100">Amount</th>
        <th class="text-left px-2 py-2 text-xs font-semibold text-gray-500 uppercase tracking-wide border-b border-gray-100">Status</th>
        <th class="text-left px-2 py-2 text-xs font-semibold text-gray-500 uppercase tracking-wide border-b border-gray-100">Card</th>
        <th class="text-left px-2 py-2 text-xs font-semibold text-gray-500 uppercase tracking-wide border-b border-gray-100">Source</th>
        <th class="text-left px-2 py-2 text-xs font-semibold text-gray-500 uppercase tracking-wide border-b border-gray-100">Location</th>
      </tr>
    </thead>
    <tbody>
      {{range .Payments}}
      <tr class="hover:bg-gray-50">
        <td class="px-2 py-2 border-b border-gray-100 tabular-nums">{{.CreatedAt.Format "2006-01-02 15:04"}}</td>
        <td class="px-2 py-2 border-b border-gray-100 tabular-nums font-medium">{{formatAmount .Amount.Amount .Amount.Currency}}</td>
        <td class="px-2 py-2 border-b border-gray-100">{{.Status}}</td>
        <td class="px-2 py-2 border-b border-gray-100 font-mono text-xs">
          {{if .CardDetails.Card.CardBrand}}{{.CardDetails.Card.CardBrand}} ····{{.CardDetails.Card.Last4}}{{else}}—{{end}}
        </td>
        <td class="px-2 py-2 border-b border-gray-100">{{.SourceType}}</td>
        <td class="px-2 py-2 border-b border-gray-100 font-mono text-xs">{{.LocationID}}</td>
      </tr>
      {{end}}
    </tbody>
  </table>
  {{else}}
  <p class="text-gray-400 text-sm mt-2 mb-6">No payments yet. Run a sandbox transaction in Square Dashboard to see one here.</p>
  {{end}}

  <p class="mt-8">
    <form class="inline" method="post" action="/auth/square/disconnect">
      <button type="submit" class="text-blue-600 underline bg-transparent border-0 cursor-pointer p-0 text-sm">
        Disconnect
      </button>
    </form>
  </p>

  <div class="mt-16 pt-4 border-t border-gray-200 text-gray-400 text-xs space-x-3">
    <a href="https://canary.growdirect.io" class="hover:text-gray-600 no-underline">Architecture →</a>
    <span>·</span>
    <a href="https://canary.growdirect.io/sdds/" class="hover:text-gray-600 no-underline">SDDs</a>
    <span>·</span>
    <a href="https://github.com/ruptiv/canary" class="hover:text-gray-600 no-underline">GitHub</a>
    <span>·</span>
    <span>sandbox · data pulled live from Square Connect API</span>
  </div>
</body>
</html>`))

var tmplFuncs = template.FuncMap{
	"formatAmount": func(amount int64, currency string) string {
		return fmt.Sprintf("%s%.2f", currencySymbol(currency), float64(amount)/100.0)
	},
}

func currencySymbol(currency string) string {
	switch currency {
	case "USD":
		return "$"
	case "EUR":
		return "€"
	case "GBP":
		return "£"
	default:
		return ""
	}
}

// silence unused warnings for things we'll use in later days
var _ = time.Time{}
