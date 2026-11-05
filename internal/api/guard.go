package api

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
)

// wsTokenPrefix marks the WebSocket subprotocol that carries the token,
// because browsers cannot set an Authorization header on WebSocket requests.
const wsTokenPrefix = "kubehack.token."

// guard rejects requests from anything other than the app's own UI. Any web
// page open in a browser can reach a loopback port, so all three checks matter:
// Host stops DNS rebinding, Origin stops cross-site requests, and the token
// stops every other local caller.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.validHost(r.Host) {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}

		origin := r.Header.Get("Origin")
		if origin != "" {
			if _, ok := s.origins[origin]; !ok {
				http.Error(w, "invalid origin", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}

		// Preflight requests never carry credentials.
		if r.Method == http.MethodOptions && origin != "" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if !s.validToken(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) validHost(host string) bool {
	return host == fmt.Sprintf("127.0.0.1:%d", s.Port()) ||
		host == fmt.Sprintf("localhost:%d", s.Port())
}

func (s *Server) validToken(r *http.Request) bool {
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return s.tokenMatches(token)
	}
	for _, header := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, protocol := range strings.Split(header, ",") {
			token, ok := strings.CutPrefix(strings.TrimSpace(protocol), wsTokenPrefix)
			if ok && s.tokenMatches(token) {
				return true
			}
		}
	}
	return false
}

func (s *Server) tokenMatches(token string) bool {
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1
}
