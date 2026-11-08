// Actual signed component preparation/upgrade/rollback in a new synthetic root.
// This checker is not bundled and does not configure a production update feed.
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/codegraph"
	"github.com/zwhu/memlane/internal/components"
	"github.com/zwhu/memlane/internal/engine"
)

func must(e error) {
	if e != nil {
		panic(e)
	}
}
func require(ok bool, message string) {
	if !ok {
		panic(message)
	}
}
func descriptor(path string) components.Descriptor {
	f, e := os.Open(path)
	must(e)
	defer f.Close()
	hash := sha256.New()
	_, e = io.Copy(hash, f)
	must(e)
	info, e := f.Stat()
	must(e)
	_, e = f.Seek(0, 0)
	must(e)
	z, e := gzip.NewReader(f)
	must(e)
	defer z.Close()
	t := tar.NewReader(z)
	var raw []byte
	var total int64
	for {
		h, e := t.Next()
		if e == io.EOF {
			break
		}
		must(e)
		total += h.Size
		if h.Name == "component.json" {
			raw, e = io.ReadAll(t)
			must(e)
		}
	}
	var m components.Metadata
	must(json.Unmarshal(raw, &m))
	manifest := sha256.Sum256(raw)
	return components.Descriptor{ID: m.ID, Version: m.Version, Platform: m.Platform, ProtocolMin: m.ProtocolMin, ProtocolMax: m.ProtocolMax, Bytes: info.Size(), UnpackedBytes: total, SHA256: hex.EncodeToString(hash.Sum(nil)), ManifestSHA256: hex.EncodeToString(manifest[:])}
}
func main() { os.Exit(run()) }
func run() (exit int) {
	oldPath := flag.String("old", "", "actual old worker archive")
	newPath := flag.String("new", "", "actual selected code worker archive")
	modelPath := flag.String("model", "", "actual model archive")
	extractorPath := flag.String("extractor", "", "actual pinned Enola archive")
	legacyCodePath := flag.String("old-code", "", "optional actual 0.0.2 worker archive for old graph compatibility")
	flag.Parse()
	report := map[string]any{"status": "failed", "synthetic_only": true, "actual_app": false, "actual_rust": false, "actual_enola": false, "formal_update_channel": false}
	var worker *engine.Client
	defer func() {
		if e := recover(); e != nil {
			report["error"] = fmt.Sprint(e)
			exit = 1
		}
		if worker != nil {
			must(worker.Close())
		}
		must(json.NewEncoder(os.Stdout).Encode(report))
	}()
	root, e := os.MkdirTemp("", "memlane-p4-code-components-")
	must(e)
	report["root"] = root
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	paths := []string{*oldPath, *newPath, *modelPath, *extractorPath}
	if *legacyCodePath != "" {
		paths = append(paths, *legacyCodePath)
	}
	descriptors := make([]components.Descriptor, len(paths))
	for i, p := range paths {
		require(filepath.IsAbs(p), "absolute archive path required")
		descriptors[i] = descriptor(p)
	}
	require(descriptors[0].Version == "0.0.1" && descriptors[1].Version == "0.0.3" && descriptors[3].Version == "0.4.30-v351", "unverified component versions")
	public, key, e := ed25519.GenerateKey(rand.Reader)
	must(e)
	var selected atomic.Int32
	var badSignature, truncate atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/feed" {
			payload, e := json.Marshal(components.Feed{Version: 1, Components: []components.Descriptor{descriptors[selected.Load()], descriptors[2], descriptors[3]}})
			if e != nil {
				http.Error(w, "fixture serialization failure", 500)
				return
			}
			signature := ed25519.Sign(key, payload)
			if badSignature.Load() {
				signature[0] ^= 1
			}
			_ = json.NewEncoder(w).Encode(components.SignedFeed{Payload: payload, Signature: base64.StdEncoding.EncodeToString(signature)})
			return
		}
		for i := range paths {
			if r.URL.Path == fmt.Sprintf("/archive-%d", i) {
				if i == 1 && truncate.Load() {
					w.Header().Set("Content-Length", fmt.Sprint(descriptors[i].Bytes))
					w.WriteHeader(200)
					f, e := os.Open(paths[i])
					if e != nil {
						return
					}
					defer f.Close()
					_, _ = io.CopyN(w, f, 4096)
					return
				}
				http.ServeFile(w, r, paths[i])
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	for i := range descriptors {
		descriptors[i].URL = fmt.Sprintf("%s/archive-%d", server.URL, i)
	}
	manager, e := components.New(filepath.Join(root, "components"), public, 1, true)
	must(e)
	prepare := func() []components.Installed {
		feed, e := manager.Feed(ctx, server.URL+"/feed")
		must(e)
		out := []components.Installed{}
		for _, d := range feed.Components {
			installed, e := manager.Prepare(ctx, d, nil)
			must(e)
			out = append(out, installed)
		}
		return out
	}
	start := func(installed []components.Installed) {
		if worker != nil {
			must(worker.Close())
			worker = nil
		}
		w, m := installed[0], installed[1]
		require(w.Metadata.Model == m.Metadata.ID, "worker/model component mismatch")
		worker, e = engine.Start(ctx, engine.Config{Executable: filepath.Join(w.Directory, w.Metadata.Executable), Library: filepath.Join(w.Directory, w.Metadata.Library), DataDir: filepath.Join(root, "index"), ModelDir: m.Directory, ExpectedVersion: w.Metadata.Version, DenyNetwork: true})
		must(e)
		report["actual_rust"] = true
	}
	old := prepare()
	start(old)
	record := engine.Record{Dataset: uuid.NewString(), Memory: uuid.NewString(), Revision: 1, Text: "Synthetic component upgrade preserves project coding conventions. 中文约定：优先保留原文。"}
	rawReceipt, e := worker.Index(ctx, record)
	must(e)
	report["raw_record"] = record
	report["raw_index_receipt"] = rawReceipt
	rawEvidence := []map[string]any{}
	checkRaw := func() {
		hits, e := worker.Search(ctx, record.Dataset, "component upgrade coding conventions", 5)
		must(e)
		found := false
		for _, h := range hits {
			if h.Memory == record.Memory && h.Revision == 1 {
				require(h.Dataset == record.Dataset && h.Generation == worker.Generation && h.Generation == rawReceipt.Generation, "raw scope or generation changed")
				require(h.StartByte >= 0 && h.EndByte > h.StartByte && h.EndByte <= len(record.Text), "invalid raw source byte range")
				require(utf8.ValidString(record.Text[h.StartByte:h.EndByte]) && h.Text == record.Text[h.StartByte:h.EndByte], "raw source text changed across upgrade/rollback")
				found = true
			}
		}
		require(found, "raw record lost across upgrade/rollback")
		rawEvidence = append(rawEvidence, map[string]any{"generation": worker.Generation, "hits": hits})
		report["raw_queries"] = rawEvidence
	}
	checkRaw()
	require(worker.CodeRecipe == "", "old worker unexpectedly claims code capability")
	_, e = worker.CodeQuery(ctx, codegraph.Query{Dataset: record.Dataset, Snapshot: uuid.NewString(), Mode: "symbols", Limit: 5, Hops: 1})
	require(e != nil, "old worker accepted code query")
	var legacyQuery *codegraph.Query
	var legacyReceipt *codegraph.Receipt
	if *legacyCodePath != "" {
		require(descriptors[4].Version == "0.0.2", "unverified legacy code version")
		selected.Store(4)
		legacyInstalled := prepare()
		start(legacyInstalled)
		checkRaw()
		legacyRepo := filepath.Join(root, "legacy-repo")
		output, e := exec.Command("/usr/bin/git", "init", legacyRepo).CombinedOutput()
		if e != nil {
			panic(string(output))
		}
		must(os.Mkdir(filepath.Join(legacyRepo, "src"), 0700))
		must(os.WriteFile(filepath.Join(legacyRepo, "src/legacy.ts"), []byte("export function legacyGraph(){return 2;}\n"), 0600))
		x := legacyInstalled[2]
		graph, e := codegraph.Extract(ctx, codegraph.ExtractOptions{Executable: filepath.Join(x.Directory, x.Metadata.Executable), Worktree: legacyRepo, Directory: filepath.Join(root, "legacy-extract"), Dataset: uuid.NewString()})
		must(e)
		r, e := worker.CodeBuild(ctx, graph.Graph)
		must(e)
		legacyReceipt = &r
		legacyQuery = &codegraph.Query{Dataset: r.Dataset, Snapshot: r.Snapshot, Mode: "symbols", Text: "legacyGraph", Limit: 5, Hops: 1}
		oldGraph, e := worker.CodeQuery(ctx, *legacyQuery)
		must(e)
		require(len(oldGraph.Nodes) == 1, "legacy 0.0.2 graph missing")
		report["legacy_graph_receipt"] = r
	}
	selected.Store(1)
	badSignature.Store(true)
	_, e = manager.Feed(ctx, server.URL+"/feed")
	require(e != nil, "invalid feed signature accepted")
	badSignature.Store(false)
	feed, e := manager.Feed(ctx, server.URL+"/feed")
	must(e)
	truncate.Store(true)
	_, e = manager.Prepare(ctx, feed.Components[0], nil)
	require(e != nil, "truncated component accepted")
	truncate.Store(false)
	checkRaw()
	partial := filepath.Join(manager.Root, "downloads", descriptors[1].ID+"-"+descriptors[1].Version+"-"+descriptors[1].SHA256[:16]+".part")
	partialInfo, e := os.Stat(partial)
	must(e)
	require(partialInfo.Size() == 4096, "truncated download did not preserve the measured 4096 bytes")
	newInstalled := prepare()
	report["upgrade_download_bytes"] = map[string]int64{"failed_attempt": partialInfo.Size(), "retry": newInstalled[0].DownloadedBytes, "archive": descriptors[1].Bytes}
	require(newInstalled[0].DownloadedBytes+partialInfo.Size() == descriptors[1].Bytes, "resumed upgrade download bytes not measured")
	start(newInstalled)
	checkRaw()
	if legacyQuery != nil {
		oldGraph, e := worker.CodeQuery(ctx, *legacyQuery)
		must(e)
		require(len(oldGraph.Nodes) == 1 && oldGraph.Receipt.GraphDigest == legacyReceipt.GraphDigest && oldGraph.Receipt.Snapshot == legacyReceipt.Snapshot, "new worker changed legacy graph")
		report["legacy_graph_new_worker_query"] = oldGraph
	}
	require(worker.CodeRecipe == "enola-0.4.30-v351" && worker.CodeRootModules, "new worker code/root handshake missing")
	repo := filepath.Join(root, "repo")
	out, e := exec.Command("/usr/bin/git", "init", repo).CombinedOutput()
	if e != nil {
		panic(string(out))
	}
	must(os.WriteFile(filepath.Join(repo, "sample.ts"), []byte("export function upgradeGraph(){return 2;}\n"), 0600))
	must(os.WriteFile(filepath.Join(repo, "sample.py"), []byte("def upgrade_graph():\n    return 2\n"), 0600))
	x := newInstalled[2]
	extracted, e := codegraph.Extract(ctx, codegraph.ExtractOptions{Executable: filepath.Join(x.Directory, x.Metadata.Executable), Worktree: repo, Directory: filepath.Join(root, "extract"), Dataset: uuid.NewString()})
	must(e)
	report["actual_enola"] = true
	receipt, e := worker.CodeBuild(ctx, extracted.Graph)
	must(e)
	q := codegraph.Query{Dataset: receipt.Dataset, Snapshot: receipt.Snapshot, Mode: "symbols", Text: "upgradeGraph", Limit: 5, Hops: 1}
	result, e := worker.CodeQuery(ctx, q)
	must(e)
	checkCode := func(result codegraph.Result) {
		require(len(result.Nodes) == 1 && result.Nodes[0].Kind == "symbol" && result.Nodes[0].File == "sample.ts" && result.Nodes[0].Line == 1, "new code symbol source missing")
		require(result.Receipt.Snapshot == receipt.Snapshot && result.Receipt.GraphDigest == receipt.GraphDigest && result.Manifest.Digest == receipt.SourceDigest, "code source snapshot/receipt changed")
		sha := ""
		for _, f := range extracted.Manifest.Files {
			if f.Path == "sample.ts" {
				sha = f.SHA256
			}
		}
		require(result.Nodes[0].FileSHA == sha && sha != "" && result.Nodes[0].ExtractorID != "", "code node source identity missing")
	}
	checkCode(result)
	mixed, e := worker.CodeQuery(ctx, codegraph.Query{Dataset: receipt.Dataset, Snapshot: receipt.Snapshot, Mode: "symbols", Text: "upgrade", Limit: 5, Hops: 1})
	must(e)
	require(len(mixed.Nodes) == 2, "mixed root TS/Python symbols missing")
	sourcePaths := map[string]bool{}
	for _, n := range mixed.Nodes {
		sourcePaths[n.File] = true
		require(n.FileSHA != "" && n.ExtractorID != "", "mixed source identity missing")
	}
	require(sourcePaths["sample.ts"] && sourcePaths["sample.py"], "mixed root language isolation lost")
	roots := 0
	for _, n := range extracted.Graph.Nodes {
		if n.Kind == "module" && n.File == "." {
			roots++
			require(n.Language == "" && n.ExtractorID != "", "shared root module not retained")
		}
	}
	require(roots == 1, "mixed root module duplicated")
	report["mixed_root_query"] = mixed
	report["code_receipt"] = receipt
	report["code_query"] = result
	selected.Store(0)
	rollback := prepare()
	require(rollback[0].Reused && rollback[0].DownloadedBytes == 0, "rollback did not reuse verified old component")
	start(rollback)
	checkRaw()
	_, e = worker.CodeQuery(ctx, q)
	require(e != nil, "rollback falsely supports code graph")
	selected.Store(1)
	restored := prepare()
	require(restored[0].Reused && restored[0].DownloadedBytes == 0, "restore redownloaded code worker")
	start(restored)
	checkRaw()
	result, e = worker.CodeQuery(ctx, q)
	must(e)
	checkCode(result)
	report["code_restored_query"] = result
	report["downloads"] = map[string]any{"initial": old, "upgrade": newInstalled, "rollback": rollback, "restore": restored}
	report["cases"] = []string{"signed_feed_actual_old_raw_worker", "invalid_signature_rejected", "truncated_upgrade_preserves_running_old_worker", "actual_upgrade_raw_and_code", "actual_rollback_raw_preserved_code_rejected", "actual_restore_preserves_Ladybug_snapshot"}
	report["status"] = "passed"
	return 0
}
