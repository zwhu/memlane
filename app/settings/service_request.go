package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zwhu/memlane/internal/controlplane"
)

func (c Client) request(ctx context.Context, method, path string, input any) ([]byte, error) {
	contentType := "application/json"
	var body io.Reader
	if form, ok := input.(url.Values); ok {
		body = strings.NewReader(form.Encode())
		contentType = "application/x-www-form-urlencoded"
	} else if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return nil, e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Config.ServiceURL, "/")+path, body)
	if e != nil {
		return nil, e
	}
	if c.Config.TokenFile != "" {
		token, e := readCachedToken(c.Config.TokenFile, 0, false)
		if e != nil {
			return nil, fmt.Errorf("official credential unavailable")
		}
		req.Header.Set("X-Api-Key", token)
	}
	req.Header.Set("Content-Type", contentType)
	hc := controlplane.NewLoopbackHTTPClient(c.HTTP, 45*time.Second)
	res, e := hc.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 256))
		return nil, &OfficialHTTPError{Status: res.StatusCode, Body: strings.TrimSpace(string(snippet))}
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 8*1024*1024+1))
	if e != nil {
		return nil, e
	}
	if len(b) > 8*1024*1024 {
		return nil, fmt.Errorf("official response exceeds reading limit")
	}
	return b, nil
}
