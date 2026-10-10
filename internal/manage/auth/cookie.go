package auth

import (
	"net/http"
	"time"
)

func setCookie(w http.ResponseWriter, name, value string, expires time.Time, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     CookiePath,
		Expires:  expires,
		HttpOnly: httpOnly,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

func ClearCookies(w http.ResponseWriter) {
	for _, c := range []struct {
		name     string
		httpOnly bool
	}{{SessionCookie, true}, {CSRFCookie, false}} {
		http.SetCookie(w, &http.Cookie{
			Name: c.name, Value: "", Path: CookiePath, MaxAge: -1,
			HttpOnly: c.httpOnly, Secure: true, SameSite: http.SameSiteStrictMode,
		})
	}
}
