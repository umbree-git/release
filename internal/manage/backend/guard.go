package backend

import (
	"net/http"
	"net/url"
)

type Guard struct{}

func (g *Guard) Client() *http.Client { return &http.Client{} }

func (g *Guard) CheckURL(u *url.URL) error { return nil }
