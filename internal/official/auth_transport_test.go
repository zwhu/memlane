package official

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Transport-only regression for the official 1.6.7 header contract. This
// synthetic HTTP server is not a service/model/onboarding validation fixture.
func TestOfficialAPIKeyAndLoginTokenUseTheirOwnTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		valid := r.URL.Path == "/api-key" && r.Header.Get("X-Api-Key") == "synthetic-api-key" && r.Header.Get("Authorization") == "" || r.URL.Path == "/login-token" && r.Header.Get("Authorization") == "Bearer synthetic.header.signature" && r.Header.Get("X-Api-Key") == ""
		if !valid {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"authorized":true}`))
	}))
	defer server.Close()
	for path, credential := range map[string]string{"/api-key": "synthetic-api-key", "/login-token": "synthetic.header.signature"} {
		var response struct {
			Authorized bool `json:"authorized"`
		}
		if err := Request(context.Background(), server.URL, path, "GET", credential, "", nil, &response); err != nil || !response.Authorized {
			t.Fatalf("official transport rejected: %s, %v", path, err)
		}
	}
}
