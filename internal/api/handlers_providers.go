// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	"github.com/soma-intelligence/open-health-sync/internal/collect"
	"github.com/soma-intelligence/open-health-sync/internal/connector"
)

type providerJSON struct {
	Slug string `json:"slug"`
	// Name is how pages name the provider ("Apple Health"), else the slug.
	Name string `json:"name"`
	// Kinds lists what the connector does: oauth (the server pulls), push (the
	// provider notifies), device (an SDK uploads).
	Kinds []string `json:"kinds"`
}

func (s *server) mountProviders(mux *http.ServeMux) {
	s.tenantRoute(mux, "GET /v1/providers", s.listProviders)
}

// enabled lists the connectors enabled now.
func (s *server) enabled() []connector.Connector {
	if s.d.Providers == nil {
		return nil
	}
	return s.d.Providers.List()
}

func (s *server) listProviders(w http.ResponseWriter, r *http.Request) {
	list := s.enabled()
	out := make([]providerJSON, 0, len(list))
	for _, c := range list {
		out = append(out, providerJSON{Slug: c.Slug(), Name: collect.ProviderName(c.Slug()), Kinds: kinds(c)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// kinds lists what c does: oauth, push, device.
func kinds(c connector.Connector) []string {
	out := []string{}
	if _, ok := connector.AsOAuth(c); ok {
		out = append(out, "oauth")
	}
	if _, ok := connector.AsPush(c); ok {
		out = append(out, "push")
	}
	if _, ok := connector.AsDevice(c); ok {
		out = append(out, "device")
	}
	return out
}
