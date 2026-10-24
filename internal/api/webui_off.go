//go:build !webui

package api

import "net/http"

func (s *Server) registerWebUI(mux *http.ServeMux) {}

type webOptions struct{}
