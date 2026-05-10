package cookie

import (
	"net/http"
	"os"
)

// Spec describes the cookie fields handlers may set directly. Security
// attributes that depend on deployment environment are centralized here.
type Spec struct {
	Name     string
	Value    string
	Path     string
	MaxAge   int
	HttpOnly bool
	SameSite http.SameSite
}

// Set writes a cookie with centralized security attributes.
func Set(w http.ResponseWriter, spec Spec) {
	http.SetCookie(w, New(spec))
}

// New builds a cookie with centralized security attributes.
func New(spec Spec) *http.Cookie {
	return &http.Cookie{
		Name:     spec.Name,
		Value:    spec.Value,
		Path:     spec.Path,
		MaxAge:   spec.MaxAge,
		HttpOnly: spec.HttpOnly,
		Secure:   secure(),
		SameSite: spec.SameSite,
	}
}

func secure() bool {
	return os.Getenv("ENV") == "production"
}
