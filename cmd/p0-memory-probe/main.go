// P0-only command for isolated functional verification. Not bundled in the App.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/memory"
	"github.com/zwhu/memlane/internal/store"
)

type probe struct {
	binary, data, model, library string
	longTerm                     bool
	client                       *engine.Client
}

func (p *probe) start(ctx context.Context) error {
	if p.client != nil {
		return nil
	}
	client, err := engine.Start(ctx, engine.Config{Executable: p.binary, DataDir: p.data, ModelDir: p.model, Library: p.library, DenyNetwork: p.longTerm})
	if err == nil {
		p.client = client
	}
	return err
}

func (p *probe) call(ctx context.Context, input any, out any) error {
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, p.binary, p.data, p.model)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Dir = filepath.Dir(p.data)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "RUST_LOG=error", "TELEMETRY_DISABLED=true", "ORT_DYLIB_PATH=" + p.library}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	result, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("engine process: %w", err)
	}
	var wrapper struct {
		Result json.RawMessage `json:"result"`
	}
	if err = json.Unmarshal(result, &wrapper); err != nil {
		return err
	}
	return json.Unmarshal(wrapper.Result, out)
}

func (p *probe) Index(ctx context.Context, r engine.Record) (engine.Receipt, error) {
	if p.longTerm {
		if err := p.start(ctx); err != nil {
			return engine.Receipt{}, err
		}
		return p.client.Index(ctx, r)
	}
	var out struct {
		Receipts []engine.Receipt `json:"receipts"`
	}
	err := p.call(ctx, map[string]any{"op": "index", "records": []engine.Record{r}}, &out)
	if err != nil {
		return engine.Receipt{}, err
	}
	if len(out.Receipts) != 1 {
		return engine.Receipt{}, errors.New("invalid receipt count")
	}
	return out.Receipts[0], nil
}

func (p *probe) Search(ctx context.Context, dataset, query string, k int) ([]engine.Candidate, error) {
	if p.longTerm {
		if err := p.start(ctx); err != nil {
			return nil, err
		}
		return p.client.Search(ctx, dataset, query, k)
	}
	var out struct {
		Candidates []engine.Candidate `json:"candidates"`
	}
	err := p.call(ctx, map[string]any{"op": "search", "dataset": dataset, "query": query, "top_k": k}, &out)
	return out.Candidates, err
}

type request struct {
	Op        string           `json:"op"`
	Mutation  store.Mutation   `json:"mutation"`
	Memory    string           `json:"memory"`
	Datasets  []string         `json:"datasets"`
	Query     string           `json:"query"`
	Limit     int              `json:"limit"`
	Candidate engine.Candidate `json:"candidate"`
	Dataset   string           `json:"dataset"`
	ChunkIDs  []string         `json:"chunk_ids"`
}

func main() {
	data := flag.String("data", "", "isolated absolute data directory")
	model := flag.String("model", "", "absolute model directory")
	binary := flag.String("engine", "", "absolute Rust index probe executable")
	library := flag.String("library", "", "absolute ONNX dynamic library")
	longTerm := flag.Bool("worker-mode", false, "use packaged long-running JSONL worker")
	flag.Parse()
	for _, p := range []string{*data, *model, *binary, *library} {
		if !filepath.IsAbs(p) {
			fmt.Fprintln(os.Stderr, "all paths must be explicit and absolute")
			os.Exit(2)
		}
	}
	db, err := store.Open(filepath.Join(*data, "memlane.sqlite"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()
	worker := &probe{binary: *binary, data: filepath.Join(*data, "index"), model: *model, library: *library, longTerm: *longTerm}
	defer func() {
		if worker.client != nil {
			worker.client.Close()
		}
	}()
	svc := memory.Service{Store: db, Engine: worker}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var input request
		var output any
		err = json.Unmarshal(scanner.Bytes(), &input)
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		if err == nil {
			switch input.Op {
			case "apply":
				output, err = db.Apply(ctx, input.Mutation)
			case "index":
				err = svc.IndexCurrent(ctx, input.Memory)
				output = map[string]any{"indexed": err == nil}
			case "get":
				output, err = db.Current(ctx, input.Memory)
			case "search":
				output, err = svc.Search(ctx, input.Datasets, input.Query, input.Limit)
			case "raw_search":
				output, err = worker.Search(ctx, input.Dataset, input.Query, input.Limit)
			case "raw_remove":
				var result any
				if worker.longTerm {
					err = worker.start(ctx)
					if err == nil {
						err = worker.client.Call(ctx, "remove", map[string]any{"dataset": input.Dataset, "chunk_ids": input.ChunkIDs}, &result)
					}
				} else {
					err = worker.call(ctx, map[string]any{"op": "remove", "dataset": input.Dataset, "chunk_ids": input.ChunkIDs}, &result)
				}
				output = result
			case "engine_stop":
				if worker.client == nil {
					err = errors.New("no active worker")
				} else {
					err = worker.client.Close()
				}
			case "engine_start":
				if worker.client != nil {
					worker.client.Close()
					worker.client = nil
				}
				err = worker.start(ctx)
			case "resolve":
				var v store.Version
				var valid bool
				v, valid, err = db.Resolve(ctx, input.Candidate)
				output = map[string]any{"valid": valid, "version": v}
			default:
				err = errors.New("unsupported probe operation")
			}
		}
		cancel()
		if err != nil {
			encoder.Encode(map[string]any{"ok": false, "error": err.Error()})
		} else {
			encoder.Encode(map[string]any{"ok": true, "result": output})
		}
	}
	if err = scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
