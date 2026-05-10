package cookie

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSet_EnvUnsetEmitsInsecureCookie(t *testing.T) {
	t.Setenv("ENV", "")
	rr := httptest.NewRecorder()

	Set(rr, Spec{
		Name:     "demo",
		Value:    "value",
		Path:     "/",
		MaxAge:   60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies len = %d, want 1", len(cookies))
	}
	if cookies[0].Secure {
		t.Fatal("ENV unset should emit Secure=false")
	}
}

func TestSet_ProductionEnvEmitsSecureCookie(t *testing.T) {
	t.Setenv("ENV", "production")
	rr := httptest.NewRecorder()

	Set(rr, Spec{
		Name:     "demo",
		Value:    "value",
		Path:     "/",
		MaxAge:   60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies len = %d, want 1", len(cookies))
	}
	if !cookies[0].Secure {
		t.Fatal("ENV=production should emit Secure=true")
	}
}
