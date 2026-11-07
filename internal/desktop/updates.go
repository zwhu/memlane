package desktop

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/egoist/mygo"
)

type testUpdateSource struct {
	ManifestURL string
	Origin      string
	Client      *http.Client
}

func (s testUpdateSource) Manifest(ctx context.Context, _ bool) (io.ReadCloser, error) {
	return s.Open(ctx, s.ManifestURL)
}
func (s testUpdateSource) Open(ctx context.Context, address string) (io.ReadCloser, error) {
	u, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	if u.Scheme+"://"+u.Host != s.Origin || u.User != nil || u.Fragment != "" {
		return nil, errors.New("controlled update URL outside test source")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("update source HTTP %d", response.StatusCode)
	}
	return response.Body, nil
}

func ConfigureControlledUpdates(config RuntimeConfig) error {
	if config.UpdateManifestURL == "" {
		return nil
	}
	if !config.AllowLocalTestSource {
		return errors.New("custom test update source requires explicit test configuration")
	}
	u, err := url.Parse(config.UpdateManifestURL)
	if err != nil {
		return err
	}
	if u.Scheme != "https" || u.User != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		return errors.New("controlled update source must be local HTTPS")
	}
	certificates := x509.NewCertPool()
	if !certificates.AppendCertsFromPEM([]byte(config.UpdateCAPEM)) {
		return errors.New("controlled source certificate invalid")
	}
	client := &http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: certificates, MinVersion: tls.VersionTLS12}}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("controlled update redirects disabled") }}
	mygo.Updater.SetSource(testUpdateSource{ManifestURL: config.UpdateManifestURL, Origin: u.Scheme + "://" + u.Host, Client: client})
	return nil
}
