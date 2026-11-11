package official

// Synthetic local auth/dataset responders prove transport and isolation only;
// the public prepared-process gate is deliberately not bypassed in production.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestProjectMaterialPrivatePrincipalDatasetAndNoLegacyTask(t *testing.T) {
	project, library, instance, dataset := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	var registered, minted, created, calls atomic.Int64
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/api/v1/auth/login":
			r.ParseForm()
			token := "project.jwt.token"
			if r.Form.Get("username") == "synthetic-owner@example.invalid" {
				token = "owner.jwt.token"
			}
			json.NewEncoder(w).Encode(map[string]string{"access_token": token})
		case "/api/v1/auth/register":
			registered.Add(1)
			var p map[string]string
			json.NewDecoder(r.Body).Decode(&p)
			if p["email"] != "memlane-project-"+project+"@example.com" || len(p["password"]) != 64 {
				t.Error("private principal mismatch")
			}
			w.Write([]byte(`{}`))
		case "/api/v1/auth/api-keys":
			minted.Add(1)
			if r.Header.Get("Authorization") != "Bearer project.jwt.token" {
				t.Error("key mint principal mismatch")
			}
			w.Write([]byte(`{"key":"synthetic-project-key"}`))
		case "/api/v1/memlane/identity":
			json.NewEncoder(w).Encode(map[string]any{"instance": instance, "cognee_version": "1.6.7", "capabilities": []string{"material_submission_v1"}})
		case "/api/v1/datasets":
			created.Add(1)
			if r.Method != "POST" || r.Header.Get("X-Api-Key") != "synthetic-project-key" {
				t.Error("dataset was not project-private")
			}
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			expected := "memlane_materials_" + strings.ReplaceAll(library, "-", "") + "_" + strings.ReplaceAll(project, "-", "")
			if body["name"] != expected {
				t.Error("legacy/shared dataset selected", body)
			}
			json.NewEncoder(w).Encode(map[string]string{"id": dataset, "name": expected})
		default:
			t.Error("unexpected task/model/plugin request", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer endpoint.Close()
	c := Controller{Root: t.TempDir()}
	if err := c.SaveSettings(Settings{Environment: map[string]string{"DEFAULT_USER_EMAIL": "synthetic-owner@example.invalid", "DEFAULT_USER_PASSWORD": "synthetic-owner-password"}}); err != nil {
		t.Fatal(err)
	}
	// Public entry refuses a missing prepared service without any network/auth.
	if _, err := c.PrepareProjectMaterialDataset(context.Background(), project, library); err == nil || calls.Load() != 0 {
		t.Fatal("public helper started or trusted an unprepared service", err)
	}
	for i := 0; i < 2; i++ {
		material, err := c.prepareProjectMaterialDatasetAt(context.Background(), endpoint.URL, project, library)
		if err != nil {
			t.Fatal(err)
		}
		if material.Instance != instance || material.Dataset != dataset || material.Token != "synthetic-project-key" {
			t.Fatal(material)
		}
		raw, _ := json.Marshal(material)
		if strings.Contains(string(raw), "synthetic-project-key") {
			t.Fatal("private token escaped to JSON")
		}
	}
	if registered.Load() != 1 || minted.Load() != 1 || created.Load() != 2 {
		t.Fatal("principal/key not reused or dataset not idempotent", registered.Load(), minted.Load(), created.Load())
	}
	tokenPath := filepath.Join(c.Root, "agents", project, "cognee-access.token")
	if err := os.Chmod(tokenPath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.prepareProjectMaterialDatasetAt(context.Background(), endpoint.URL, project, library); err == nil {
		t.Fatal("public credential accepted")
	}
}
