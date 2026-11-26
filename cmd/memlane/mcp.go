package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zwhu/memlane/core/hooks"
)

const (
	mcpMaxFrameBytes    = (1 << 20) + 4096
	mcpMaxResponseBytes = 8 << 20
	mcpDefaultTimeout   = 30 * time.Second
	mcpMaxConcurrency   = 8
)

type mcpBridgeConfig struct {
	GatewayURL string
	KeyFile    string
	Timeout    time.Duration
	Processing bool
	Client     *http.Client
}

func runMCP(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	gatewayURL := flags.String("gateway-url", os.Getenv("MEMLANE_GATEWAY_URL"), "loopback gateway URL")
	keyFile := flags.String("key-file", os.Getenv("MEMLANE_AGENT_KEY_FILE"), "private agent key file")
	configPath := flags.String("config", "", "runtime configuration fallback")
	host := flags.String("host", "", "host name when resolving key file from runtime config")
	timeout := flags.Duration("timeout", mcpDefaultTimeout, "gateway request timeout")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return 0
	} else if err != nil || flags.NArg() != 0 {
		return 2
	}
	if *timeout <= 0 {
		*timeout = mcpDefaultTimeout
	}
	processing := os.Getenv("MEMLANE_PROCESSING_SESSION") == "1"
	cfg := mcpBridgeConfig{
		GatewayURL: strings.TrimSpace(*gatewayURL),
		KeyFile:    strings.TrimSpace(*keyFile),
		Timeout:    *timeout,
		Processing: processing,
	}
	if !processing && (cfg.GatewayURL == "" || cfg.KeyFile == "") {
		if runtimeCfg, err := readRuntime(*configPath); err == nil {
			cfg.GatewayURL = firstNonempty(cfg.GatewayURL, runtimeCfg.GatewayURL)
			if cfg.KeyFile == "" && *host != "" {
				cfg.KeyFile = runtimeCfg.agentKeyFile(hooks.CanonicalHost(*host))
			}
		}
	}
	if !processing {
		if err := hooks.ValidateGatewayURL(cfg.GatewayURL); err != nil {
			return reportError(stderr, fmt.Errorf("invalid gateway URL: %w", err))
		}
		if cfg.KeyFile == "" || !filepath.IsAbs(cfg.KeyFile) {
			return reportError(stderr, errors.New("absolute --key-file path is required"))
		}
	}
	return reportError(stderr, serveMCPStdio(ctx, stdin, stdout, cfg))
}

func serveMCPStdio(ctx context.Context, stdin io.Reader, stdout io.Writer, cfg mcpBridgeConfig) error {
	if cfg.Timeout <= 0 {
		cfg.Timeout = mcpDefaultTimeout
	}
	client := cfg.Client
	if client == nil {
		tr := &http.Transport{
			Proxy:               nil,
			MaxIdleConnsPerHost: mcpMaxConcurrency,
			IdleConnTimeout:     30 * time.Second,
		}
		defer tr.CloseIdleConnections()
		client = &http.Client{
			Transport: tr,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	endpoint := ""
	if !cfg.Processing {
		addr, err := loopbackAddress(cfg.GatewayURL)
		if err != nil {
			return err
		}
		endpoint = "http://" + addr + "/mcp"
	}

	reader := bufio.NewReaderSize(stdin, 64*1024)
	var (
		writeMu  sync.Mutex
		writeErr error
		wg       sync.WaitGroup
		sem      = make(chan struct{}, mcpMaxConcurrency)
	)
	writeLine := func(line []byte) {
		if len(line) == 0 {
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		if writeErr != nil {
			return
		}
		if _, err := stdout.Write(line); err != nil {
			writeErr = err
			return
		}
		if line[len(line)-1] != '\n' {
			if _, err := stdout.Write([]byte{'\n'}); err != nil {
				writeErr = err
			}
		}
	}

	for {
		if ctx.Err() != nil {
			break
		}
		line, tooLong, eof, err := readBoundedLine(reader, mcpMaxFrameBytes)
		if err != nil {
			wg.Wait()
			return err
		}
		trimmed := bytes.TrimSpace(line)
		if tooLong {
			writeLine(mcpErrorJSON(nil, -32600, "Request too large"))
			if eof {
				break
			}
			continue
		}
		if len(trimmed) == 0 {
			if eof {
				break
			}
			continue
		}
		payload := append([]byte(nil), trimmed...)
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return nil
		}
		wg.Add(1)
		go func(raw []byte) {
			defer func() {
				<-sem
				wg.Done()
			}()
			var reply []byte
			if cfg.Processing {
				reply = handleProcessingMCP(raw)
			} else {
				reply = forwardMCPFrame(ctx, client, endpoint, cfg.KeyFile, cfg.Timeout, raw)
			}
			if len(reply) > 0 {
				writeLine(reply)
			}
		}(payload)
		if eof {
			break
		}
	}
	wg.Wait()
	writeMu.Lock()
	err := writeErr
	writeMu.Unlock()
	return err
}

func readBoundedLine(r *bufio.Reader, limit int) (line []byte, tooLong bool, eof bool, err error) {
	var buf bytes.Buffer
	for {
		chunk, sliceErr := r.ReadSlice('\n')
		if !tooLong {
			if buf.Len()+len(chunk) > limit {
				tooLong = true
				buf.Reset()
			} else {
				buf.Write(chunk)
			}
		}
		if sliceErr == nil {
			return buf.Bytes(), tooLong, false, nil
		}
		if errors.Is(sliceErr, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(sliceErr, io.EOF) {
			return buf.Bytes(), tooLong, true, nil
		}
		return nil, false, false, sliceErr
	}
}

func forwardMCPFrame(ctx context.Context, client *http.Client, endpoint, keyFile string, timeout time.Duration, raw []byte) []byte {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return mcpErrorJSON(nil, -32700, "Parse error")
	}
	token, err := hooks.ReadPrivateKey(keyFile)
	if err != nil {
		return mcpErrorForFrame(raw, -32001, "Memlane MCP key unavailable")
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return mcpErrorForFrame(raw, -32603, "Memlane gateway unavailable")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Api-Key", token)

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return mcpErrorForFrame(raw, -32603, "Memlane gateway unavailable")
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusAccepted, http.StatusNoContent:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return mcpErrorForFrame(raw, -32001, "Memlane MCP authentication failed")
	case http.StatusRequestEntityTooLarge:
		return mcpErrorForFrame(raw, -32600, "Request too large")
	case http.StatusOK:
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, mcpMaxResponseBytes+1))
		if readErr != nil || len(body) > mcpMaxResponseBytes {
			return mcpErrorForFrame(raw, -32603, "Memlane gateway response invalid")
		}
		trimmed := bytes.TrimSpace(body)
		if len(trimmed) == 0 {
			return nil
		}
		var compacted bytes.Buffer
		if err := json.Compact(&compacted, trimmed); err != nil {
			return mcpErrorForFrame(raw, -32603, "Memlane gateway response invalid")
		}
		return compacted.Bytes()
	default:
		return mcpErrorForFrame(raw, -32603, "Memlane gateway unavailable")
	}
}

func handleProcessingMCP(raw []byte) []byte {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return mcpErrorJSON(nil, -32700, "Parse error")
	}
	if raw[0] == '[' {
		var batch []json.RawMessage
		if json.Unmarshal(raw, &batch) != nil || len(batch) == 0 {
			return mcpErrorJSON(nil, -32600, "Invalid Request")
		}
		replies := make([]json.RawMessage, 0, len(batch))
		for _, item := range batch {
			if reply := handleProcessingSingle(bytes.TrimSpace(item)); len(reply) > 0 {
				replies = append(replies, reply)
			}
		}
		if len(replies) == 0 {
			return nil
		}
		out, _ := json.Marshal(replies)
		return out
	}
	return handleProcessingSingle(raw)
}

func validMCPID(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return true
	}
	first := raw[0]
	return first == '"' || first == '-' || (first >= '0' && first <= '9')
}

func handleProcessingSingle(raw []byte) []byte {
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &req) != nil || req.JSONRPC != "2.0" || req.Method == "" || !validMCPID(req.ID) {
		return mcpErrorJSON(nil, -32600, "Invalid Request")
	}
	if req.ID == nil {
		return nil
	}
	switch req.Method {
	case "initialize":
		version := "2025-03-26"
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if len(req.Params) > 0 && json.Unmarshal(req.Params, &params) == nil {
			if params.ProtocolVersion == "2024-11-05" || params.ProtocolVersion == "2025-03-26" {
				version = params.ProtocolVersion
			}
		}
		return mcpResultJSON(req.ID, map[string]any{
			"protocolVersion": version,
			"serverInfo":      map[string]string{"name": "memlane", "version": "1.0.0"},
			"capabilities":    map[string]any{"tools": map[string]bool{"listChanged": false}},
		})
	case "ping", "notifications/initialized":
		return mcpResultJSON(req.ID, struct{}{})
	case "tools/list":
		return mcpResultJSON(req.ID, map[string]any{"tools": []any{}})
	case "tools/call":
		return mcpResultJSON(req.ID, map[string]any{
			"isError": true,
			"content": []map[string]string{{"type": "text", "text": "Memlane tools are disabled during processing sessions"}},
		})
	default:
		return mcpErrorJSON(req.ID, -32601, "Method not found")
	}
}

func mcpErrorForFrame(raw []byte, code int, message string) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '[' {
		var batch []json.RawMessage
		if json.Unmarshal(trimmed, &batch) != nil || len(batch) == 0 {
			return mcpErrorJSON(nil, -32600, "Invalid Request")
		}
		replies := make([]json.RawMessage, 0, len(batch))
		for _, item := range batch {
			if reply := mcpErrorForSingle(bytes.TrimSpace(item), code, message); len(reply) > 0 {
				replies = append(replies, reply)
			}
		}
		if len(replies) == 0 {
			return nil
		}
		out, _ := json.Marshal(replies)
		return out
	}
	return mcpErrorForSingle(trimmed, code, message)
}

func mcpErrorForSingle(raw []byte, code int, message string) []byte {
	if len(raw) == 0 || raw[0] != '{' {
		return mcpErrorJSON(nil, -32600, "Invalid Request")
	}
	var env struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		ID      json.RawMessage `json:"id"`
	}
	if json.Unmarshal(raw, &env) != nil || env.JSONRPC != "2.0" || env.Method == "" || !validMCPID(env.ID) {
		return mcpErrorJSON(nil, -32600, "Invalid Request")
	}
	if env.ID == nil {
		return nil
	}
	return mcpErrorJSON(env.ID, code, message)
}

func mcpResultJSON(id json.RawMessage, result any) []byte {
	out, _ := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
	return out
}

func mcpErrorJSON(id json.RawMessage, code int, message string) []byte {
	if id == nil {
		id = json.RawMessage("null")
	}
	out, _ := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Error: struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: code, Message: message},
	})
	return out
}
