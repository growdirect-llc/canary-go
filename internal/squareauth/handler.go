// Handler — chi routes for the Square OAuth demo flow.
//
//   GET  /                            landing page with "Connect Square" button
//   GET  /auth/square                 OAuth start; sets state cookie; redirects to Square
//   GET  /auth/square/callback        OAuth callback; exchanges code; stores token; sets session cookie; redirects to /dashboard
//   GET  /dashboard                   server-rendered dashboard reading the connected merchant's Square data
//   POST /auth/square/disconnect      delete the stored token; clear session cookie; redirect to /
//
// Session is a HttpOnly cookie carrying the internal merchant_id (UUID).
// CSRF state is a separate short-lived HttpOnly cookie carrying a hash of
// the random state value sent to Square.
package squareauth

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
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

	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    hashed,
		Path:     "/",
		MaxAge:   stateMaxAge,
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
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
	http.SetCookie(w, &http.Cookie{
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

	// Set session cookie carrying the internal merchant_id.
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    internalMerchantID.String(),
		Path:     "/",
		MaxAge:   sessionMaxAge,
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
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
		"Merchant":      merchant,
		"Locations":     locations,
		"Payments":      payments,
		"Environment":   s.cfg.Environment,
		"InternalID":    mID.String(),
		"SquareID":      creds.MerchantIDSquare,
		"PaymentsCount": len(payments),
		"LocationsCount": len(locations),
	}
	renderHTML(w, dashboardTmpl, data)
}

// ─── Disconnect ────────────────────────────────────────────────────────────

func (s *Service) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if mID, ok := s.merchantFromCookie(r); ok {
		_ = s.DeleteToken(r.Context(), mID)
	}
	http.SetCookie(w, &http.Cookie{
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
	id, err := uuid.Parse(c.Value)
	if err != nil {
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
  <style>
    body { font-family: system-ui, -apple-system, sans-serif; max-width: 640px; margin: 4em auto; padding: 0 1.5em; color: #111; line-height: 1.55; }
    h1 { font-size: 1.6em; margin-bottom: 0.3em; }
    .sub { color: #666; margin-bottom: 2em; }
    a.btn { display: inline-block; padding: 0.7em 1.2em; background: #006aff; color: white; text-decoration: none; border-radius: 6px; font-weight: 600; }
    a.btn:hover { background: #0050c8; }
    .env { display: inline-block; padding: 2px 8px; border-radius: 4px; font-size: 0.85em; background: #fef3c7; color: #92400e; }
    .footer { margin-top: 4em; padding-top: 1em; border-top: 1px solid #eee; color: #999; font-size: 0.85em; }
  </style>
</head>
<body>
  <h1>Canary — running on GCP <span class="env">{{.Environment}}</span></h1>
  <p class="sub">Multi-POS retail platform. Connect your Square sandbox account to see your data.</p>

  {{if .Connected}}
    <p>You're connected. <a href="/dashboard">Open dashboard →</a></p>
  {{else}}
    <p><a class="btn" href="/auth/square">Connect Square</a></p>
  {{end}}

  <div class="footer">
    growdirect.io · GrowDirect LLC · sandbox demo · token storage encrypted at rest
  </div>
</body>
</html>`))

var dashboardTmpl = template.Must(template.New("dashboard").Funcs(tmplFuncs).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <title>Canary Dashboard — {{if .Merchant}}{{.Merchant.BusinessName}}{{end}}</title>
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <style>
    body { font-family: system-ui, -apple-system, sans-serif; max-width: 960px; margin: 2em auto; padding: 0 1.5em; color: #111; line-height: 1.5; }
    h1 { font-size: 1.5em; margin-bottom: 0.2em; }
    h2 { font-size: 1.1em; margin-top: 2em; color: #444; border-bottom: 1px solid #ddd; padding-bottom: 0.3em; }
    .meta { color: #666; font-size: 0.95em; margin-bottom: 1.5em; }
    .pill { display: inline-block; padding: 2px 8px; border-radius: 4px; font-size: 0.85em; margin-right: 0.4em; }
    .pill.env { background: #fef3c7; color: #92400e; }
    .pill.status-active { background: #d1fae5; color: #065f46; }
    .pill.status-other { background: #e5e7eb; color: #374151; }
    table { width: 100%; border-collapse: collapse; margin-bottom: 1em; font-size: 0.95em; }
    th, td { text-align: left; padding: 0.5em 0.6em; border-bottom: 1px solid #eee; }
    th { color: #555; font-weight: 600; font-size: 0.9em; text-transform: uppercase; letter-spacing: 0.04em; }
    .footer { margin-top: 4em; padding-top: 1em; border-top: 1px solid #eee; color: #999; font-size: 0.85em; }
    form.inline { display: inline; }
    button.link { background: none; border: none; color: #006aff; cursor: pointer; font: inherit; padding: 0; text-decoration: underline; }
  </style>
</head>
<body>
  {{if .Merchant}}
  <h1>{{.Merchant.BusinessName}}</h1>
  <p class="meta">
    <span class="pill env">{{.Environment}}</span>
    <span class="pill status-{{if eq .Merchant.Status "ACTIVE"}}active{{else}}other{{end}}">{{.Merchant.Status}}</span>
    {{.Merchant.Country}} · {{.Merchant.Currency}} · {{.Merchant.LanguageCode}}
  </p>
  <p class="meta">Square ID: <code>{{.SquareID}}</code> · Internal ID: <code>{{.InternalID}}</code></p>
  {{end}}

  <h2>Locations ({{.LocationsCount}})</h2>
  {{if .Locations}}
  <table>
    <tr><th>Name</th><th>Status</th><th>Type</th><th>City</th><th>Region</th><th>Country</th></tr>
    {{range .Locations}}
    <tr>
      <td>{{.Name}}</td>
      <td>{{.Status}}</td>
      <td>{{.Type}}</td>
      <td>{{.Address.Locality}}</td>
      <td>{{.Address.Region}}</td>
      <td>{{.Country}}</td>
    </tr>
    {{end}}
  </table>
  {{else}}
  <p>No locations.</p>
  {{end}}

  <h2>Recent payments ({{.PaymentsCount}})</h2>
  {{if .Payments}}
  <table>
    <tr><th>When</th><th>Amount</th><th>Status</th><th>Card</th><th>Source</th><th>Location</th></tr>
    {{range .Payments}}
    <tr>
      <td>{{.CreatedAt.Format "2006-01-02 15:04"}}</td>
      <td>{{formatAmount .Amount.Amount .Amount.Currency}}</td>
      <td>{{.Status}}</td>
      <td>{{if .CardDetails.Card.CardBrand}}{{.CardDetails.Card.CardBrand}} ····{{.CardDetails.Card.Last4}}{{else}}—{{end}}</td>
      <td>{{.SourceType}}</td>
      <td>{{.LocationID}}</td>
    </tr>
    {{end}}
  </table>
  {{else}}
  <p>No payments yet. Run a sandbox transaction in Square Dashboard to see one here.</p>
  {{end}}

  <p style="margin-top: 2em;">
    <form class="inline" method="post" action="/auth/square/disconnect">
      <button class="link" type="submit">Disconnect</button>
    </form>
  </p>

  <div class="footer">
    growdirect.io · sandbox demo · data pulled live from Square Connect API
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
