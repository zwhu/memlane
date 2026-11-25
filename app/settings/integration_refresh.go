package settings

import (
	"context"
	"sync"
	"time"
)

func (s *IntegrationState) generationReady() bool {
	return s.GenerationLoaded && s.GenerationError == ""
}

func (a *Controller) RefreshIntegrations() {
	client, ok := a.accessClient()
	s := &a.integrations
	if !ok || s.Loading || s.Busy || !a.canDispatch() {
		return
	}
	opsDir := a.generationOperationsDir()
	s.Loading = true
	s.Request++
	s.NextReceiptCheck = time.Now().Add(10 * time.Second)
	request := s.Request
	config := a.config
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		pending, err := loadGenerationOperationsFromDir(opsDir)
		if err != nil {
			a.dispatch(func() {
				if request == s.Request {
					s.Loading = false
					s.Error = "无法读取待核对操作，已停止发送新配置请求"
				}
			})
			return
		}
		var connections ConnectionsDirectory
		var generation GenerationDirectory
		var connErr, genErr error
		var wg sync.WaitGroup
		if !config.GenerationSetupOnly {
			wg.Add(1)
			go func() {
				defer wg.Done()
				connErr = client.request(ctx, "GET", "/v1/connections?force=1", nil, &connections)
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			genErr = client.request(ctx, "GET", "/v1/generation", nil, &generation)
		}()
		wg.Wait()
		connectionsLoaded := !config.GenerationSetupOnly && connErr == nil
		if connectionsLoaded {
			loadAgentVersionsFn(ctx, config, connections.Connections)
		}
		a.dispatch(func() {
			if request != s.Request {
				return
			}
			s.Loading = false
			if s.Busy || a.config.ProjectControl == nil || *a.config.ProjectControl != client.Config ||
				a.config.GenerationSetupOnly != config.GenerationSetupOnly {
				return
			}
			s.PendingOperations = pending
			s.ConnectionsError = ControlUserError(connErr)
			s.GenerationError = ControlUserError(genErr)
			s.Error = s.GenerationError
			if s.Error == "" {
				s.Error = s.ConnectionsError
			}
			if genErr == nil {
				s.Generation = generation
				s.GenerationLoaded = true
				s.Loaded = true
			}
			if connErr != nil {
				a.captureReadFailed()
			}
			if connectionsLoaded {
				a.acceptConnectionsDirectory(connections)
				s.ConnectionsLoaded = true
				s.Loaded = true
				if s.HostVersions == nil {
					s.HostVersions = map[string]string{}
				}
				for _, row := range connections.Connections {
					s.HostVersions[row.Kind] = row.HostVersion
				}
			}
			if genErr == nil || connectionsLoaded {
				if s.Selected == nil {
					s.Selected = map[string]bool{}
				}
				if s.ItemMessages == nil {
					s.ItemMessages = map[string]string{}
				}
			}
			if connectionsLoaded {
				a.preloadPinnedConnectionPreviews()
			}
		})
	}()
}
