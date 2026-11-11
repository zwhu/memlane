package official

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/uuid"
)

// Token is transient host state, never JSON/UI/ledger/source metadata.
type ProjectMaterialDataset struct {
	Instance string `json:"instance"`
	Dataset  string `json:"dataset"`
	Project  string `json:"project"`
	Library  string `json:"library"`
	Endpoint string `json:"endpoint"`
	Token    string `json:"-"`
}

// Observe only an already prepared App-owned service. This path cannot start
// services, run first tasks, install plugins, select models or read Agent auth.
func (c *Controller) PrepareProjectMaterialDataset(ctx context.Context, project, library string) (ProjectMaterialDataset, error) {
	out := ProjectMaterialDataset{}
	for _, id := range []string{project, library} {
		if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
			return out, errors.New("canonical project and library required")
		}
	}
	if !filepath.IsAbs(c.Root) {
		return out, errors.New("prepared local Cognee environment required")
	}
	var settings Settings
	if err := privateJSON(filepath.Join(c.Root, "cognee-config.json"), &settings); err != nil || filepath.Base(settings.Root) != "cognee" {
		return out, errors.New("prepared private Cognee settings required")
	}
	ownerRoot := filepath.Dir(settings.Root)
	requestedRoot, rootErr := filepath.EvalSymlinks(c.Root)
	savedRoot, savedErr := filepath.EvalSymlinks(ownerRoot)
	if rootErr != nil || savedErr != nil || requestedRoot != savedRoot {
		return out, errors.New("private Cognee root identity changed")
	}
	c = &Controller{Root: ownerRoot, Config: c.Config}
	state, err := c.ReadState()
	if err != nil || state.Phase == "stopped" || state.PID <= 1 {
		return out, errors.New("prepared local Cognee environment required")
	}
	command, err := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(state.PID), "-o", "command=").Output()
	if err != nil {
		return out, errors.New("prepared local Cognee environment required")
	}
	suffix := " --config " + filepath.Join(c.Root, "cognee-config.json")
	actual := strings.TrimSpace(string(command))
	if !strings.HasSuffix(actual, suffix) {
		return out, errors.New("App-owned Cognee service required")
	}
	executable := strings.TrimSuffix(actual, suffix)
	installedRoot, err := filepath.EvalSymlinks(filepath.Join(c.Root, "runtime"))
	if err != nil {
		return out, errors.New("prepared Cognee component required")
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return out, errors.New("prepared Cognee component required")
	}
	relative, err := filepath.Rel(installedRoot, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || !filepath.IsAbs(executable) {
		return out, errors.New("App-owned Cognee component required")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return out, errors.New("prepared Cognee component required")
	}
	if status := observeEnvironmentHealth(ctx, state.Endpoint); status.Phase != "ready" {
		return out, errors.New("Cognee environment health unavailable")
	}
	// Serialize principal/key creation across independent daemon/App controllers.
	principalRoot, err := c.agentRoot(project)
	if err != nil {
		return out, err
	}
	if err = os.MkdirAll(principalRoot, 0700); err != nil {
		return out, err
	}
	if info, dirErr := os.Lstat(principalRoot); dirErr != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return out, errors.New("private project principal directory required")
	}
	lock, err := os.OpenFile(filepath.Join(principalRoot, "material-authorization.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return out, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return out, errors.New("project material authorization busy")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return c.prepareProjectMaterialDatasetAt(ctx, state.Endpoint, project, library)
}

func (c *Controller) prepareProjectMaterialDatasetAt(ctx context.Context, endpoint, project, library string) (ProjectMaterialDataset, error) {
	out := ProjectMaterialDataset{Endpoint: endpoint, Project: project, Library: library}
	ownerToken, err := c.OwnerToken(ctx, endpoint)
	if err != nil || ownerToken == "" {
		return out, errors.New("local Cognee owner authorization unavailable")
	}
	var identity struct {
		Instance     string   `json:"instance"`
		Version      string   `json:"cognee_version"`
		Capabilities []string `json:"capabilities"`
	}
	if err = Request(ctx, endpoint, "/api/v1/memlane/identity", "GET", ownerToken, "", nil, &identity); err != nil {
		return out, errors.New("local Cognee identity unavailable")
	}
	compatible := false
	for _, capability := range identity.Capabilities {
		if capability == "material_submission_v1" {
			compatible = true
		}
	}
	if parsed, e := uuid.Parse(identity.Instance); e != nil || parsed.String() != identity.Instance || identity.Version != "1.6.7" || !compatible {
		return out, errors.New("Cognee material submission component required")
	}
	principalRoot, rootErr := c.agentRoot(project)
	if rootErr != nil {
		return out, errors.New("private project identity required")
	}
	tokenPath := filepath.Join(principalRoot, "cognee-access.token")
	if info, tokenErr := os.Lstat(tokenPath); tokenErr == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 8192 {
			return out, errors.New("private project Cognee credential required")
		}
	} else if !os.IsNotExist(tokenErr) {
		return out, errors.New("private project Cognee credential unavailable")
	}
	token, err := c.projectToken(ctx, endpoint, project)
	if err != nil {
		return out, errors.New("private project Cognee authorization unavailable")
	}
	name := "memlane_materials_" + strings.ReplaceAll(library, "-", "") + "_" + strings.ReplaceAll(project, "-", "")
	body, _ := json.Marshal(map[string]string{"name": name})
	var dataset struct{ ID, Name string }
	if err = Request(ctx, endpoint, "/api/v1/datasets", "POST", token, "application/json", body, &dataset); err != nil {
		return out, errors.New("project material dataset unavailable")
	}
	if parsed, e := uuid.Parse(dataset.ID); e != nil || parsed.String() != dataset.ID || dataset.Name != name {
		return out, errors.New("project material dataset identity changed")
	}
	var confirmed struct {
		Instance string `json:"instance"`
		Version  string `json:"cognee_version"`
	}
	if err = Request(ctx, endpoint, "/api/v1/memlane/identity", "GET", token, "", nil, &confirmed); err != nil || confirmed.Instance != identity.Instance || confirmed.Version != identity.Version {
		return out, errors.New("project material service identity changed")
	}
	out.Instance, out.Dataset, out.Token = identity.Instance, dataset.ID, token
	return out, nil
}
