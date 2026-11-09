package official

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestObservationUsesOnlyBasicHealthWithoutModelOrLogin(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/health" || r.Header.Get("Authorization") != "" {
			t.Error("startup performed a model/login action", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"status":"ready","health":"healthy","version":"1.6.7"}`))
	}))
	defer server.Close()
	if status := observeEnvironmentHealth(context.Background(), server.URL); status.Phase != "ready" || calls != 1 {
		t.Fatal("health observation did not finish", status, calls)
	}
}

func TestObservationRejectsIncompatibleOrUnhealthyService(t *testing.T) {
	for _, body := range []string{`{"status":"ready","health":"healthy","version":"0"}`, `{"status":"starting","health":"healthy","version":"1.6.7"}`, `{"status":"ready","health":"failed","version":"1.6.7"}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		status := observeEnvironmentHealth(context.Background(), server.URL)
		server.Close()
		if status.Phase == "ready" {
			t.Fatal("unhealthy service was accepted", body)
		}
	}
}
