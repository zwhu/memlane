// p4-code-check validates only fresh synthetic repositories with real Enola/Rust.
// It is not bundled and cannot enable production project scans.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/codegraph"
	"github.com/zwhu/memlane/internal/engine"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func require(ok bool, message string) {
	if !ok {
		panic(message)
	}
}
func tree(root string) map[string]string {
	out := map[string]string{}
	must(filepath.WalkDir(root, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		var raw []byte
		if d.Type()&os.ModeSymlink != 0 {
			p, e := os.Readlink(path)
			if e != nil {
				return e
			}
			raw = []byte(p)
		} else {
			raw, e = os.ReadFile(path)
			if e != nil {
				return e
			}
		}
		h := sha256.Sum256(raw)
		out[rel] = hex.EncodeToString(h[:])
		return nil
	}))
	return out
}
func main() { os.Exit(run()) }
func run() (exit int) {
	binary := flag.String("worker", "", "absolute rebuilt worker")
	model := flag.String("model", "", "absolute accepted model component")
	library := flag.String("library", "", "absolute ORT library")
	enola := flag.String("enola", "", "absolute fixed extractor")
	flag.Parse()
	report := map[string]any{"status": "failed", "synthetic_only": true, "actual_rust": true, "actual_enola": true, "actual_app": false, "actual_daily_agent": false, "fixture_faults": []string{"foreign_dataset", "import_replay_conflict", "oversize_source", "unsupported_query"}, "cases": []string{}}
	var client *engine.Client
	cases := []string{}
	root, e := os.MkdirTemp("", "memlane-p4-code-")
	must(e)
	report["root"] = root
	defer func() {
		if recovered := recover(); recovered != nil {
			report["error"] = fmt.Sprint(recovered)
			exit = 1
		}
		if client != nil {
			if e := client.Close(); e != nil {
				report["cleanup_error"] = e.Error()
				report["status"] = "failed"
				exit = 1
			}
		}
		report["cases"] = cases
		_ = json.NewEncoder(os.Stdout).Encode(report)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	repo := filepath.Join(root, "repo")
	must(os.Mkdir(repo, 0700))
	cmd := exec.Command("/usr/bin/git", "init", repo)
	_, e = cmd.CombinedOutput()
	must(e)
	samples := map[string]string{
		"package.json":           "{\"name\":\"synthetic-code\",\"type\":\"module\"}",
		"src/greet.ts":           "export function greet(name: string): string { return `Hello ${name}`; }\n",
		"src/main.ts":            "import {greet} from \"./greet.js\";\nexport function run(): string { return greet(\"synthetic\"); }\n",
		"pyproject.toml":         "[project]\nname=\"synthetic-code\"\nversion=\"0.0.1\"\n",
		"app/greet.py":           "def greet(name: str) -> str:\n    return \"Hello \" + name\n",
		"app/main.py":            "from app.greet import greet\n\ndef run():\n    return greet(\"synthetic\")\n",
		".gitignore":             "ignored.ts\n",
		"ignored.ts":             "export function mustNotIndexIgnored() {}\n",
		"node_modules/hidden.ts": "export function mustNotIndexDependency() {}\n",
		".env.ts":                "export const mustNotIndexSecret = 'synthetic';\n",
		"mcp-arch.yaml":          "output: /invalid/synthetic\n",
	}
	for name, content := range samples {
		p := filepath.Join(repo, name)
		must(os.MkdirAll(filepath.Dir(p), 0700))
		must(os.WriteFile(p, []byte(content), 0600))
	}
	must(os.WriteFile(filepath.Join(root, "external.ts"), []byte("export function mustNotIndexExternal() {}"), 0600))
	must(os.Symlink(filepath.Join(root, "external.ts"), filepath.Join(repo, "external.ts")))
	cmd = exec.Command("/usr/bin/git", "-C", repo, "add", "-f", "ignored.ts")
	_, e = cmd.CombinedOutput()
	must(e)
	before := tree(repo)
	alias := filepath.Join(filepath.Dir(repo), strings.ToUpper(filepath.Base(repo)))
	if original, e := os.Stat(repo); e == nil {
		if other, e := os.Stat(alias); e == nil && os.SameFile(original, other) {
			_, e = codegraph.Extract(ctx, codegraph.ExtractOptions{Executable: *enola, Worktree: repo, Directory: filepath.Join(alias, "must-not-create"), Dataset: uuid.NewString()})
			require(e != nil, "case alias output accepted")
			_, e = codegraph.Capture(ctx, repo, filepath.Join(alias, "must-not-copy"))
			require(e != nil, "case alias shadow accepted")
			one, _ := json.Marshal(before)
			two, _ := json.Marshal(tree(repo))
			require(string(one) == string(two), "case alias rejection wrote source")
			cases = append(cases, "actual_case_insensitive_output_alias_rejected_without_writes")
		}
	}
	dataset := uuid.NewString()
	extracted, e := codegraph.Extract(ctx, codegraph.ExtractOptions{Executable: *enola, Worktree: repo, Directory: filepath.Join(root, "extract"), Dataset: dataset})
	must(e)
	require(len(extracted.Manifest.Files) == 6, "ignore/source snapshot wrong file count")
	for _, n := range extracted.Graph.Nodes {
		require(!strings.Contains(n.Name, "mustNotIndex"), "ignored content indexed")
	}
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(tree(repo))
	require(string(a) == string(b), "source repo changed during extraction")
	cases = append(cases, "actual_TS_Python_extraction_ignores_and_original_zero_writes")
	cfg := engine.Config{Executable: *binary, DataDir: filepath.Join(root, "engine"), ModelDir: *model, Library: *library, ExpectedVersion: "0.0.2", DenyNetwork: true}
	client, e = engine.Start(ctx, cfg)
	must(e)
	receipt, e := client.CodeBuild(ctx, extracted.Graph)
	must(e)
	report["receipt"] = receipt
	query := func(mode, text, node string) codegraph.Result {
		q, e := client.CodeQuery(ctx, codegraph.Query{Dataset: dataset, Snapshot: receipt.Snapshot, Mode: mode, Text: text, Node: node, Limit: 30, Hops: 3})
		must(e)
		return q
	}
	symbols := query("symbols", "greet", "")
	require(len(symbols.Nodes) == 2, "TS/Python symbols not found")
	for _, n := range symbols.Nodes {
		require(n.FileSHA != "" && n.Line == 1, "symbol source missing")
	}
	files := query("files", "main", "")
	require(len(files.Nodes) == 2, "source files missing")
	calls := query("calls", "run", "")
	byID := func(result codegraph.Result) map[string]codegraph.Node {
		out := map[string]codegraph.Node{}
		for _, n := range result.Nodes {
			out[n.ID] = n
		}
		return out
	}
	exact := func(result codegraph.Result, kind, sourceFile, targetFile, sourceKind, targetKind string) bool {
		nodes := byID(result)
		for _, edge := range result.Edges {
			a, b := nodes[edge.Source], nodes[edge.Target]
			if edge.Kind == kind && a.File == sourceFile && b.File == targetFile && a.Kind == sourceKind && b.Kind == targetKind {
				return true
			}
		}
		return false
	}
	require(exact(calls, "calls", "src/main.ts", "src/greet.ts", "symbol", "symbol"), "TypeScript run to greet call missing")
	require(exact(calls, "calls", "app/main.py", "app/greet.py", "symbol", "symbol"), "Python run to greet call missing")
	deps := query("dependencies", "main", "")
	require(exact(deps, "imports", "src/main.ts", "src/greet.ts", "dependency", "file"), "TypeScript main to greet import missing")
	require(exact(deps, "imports", "app/main.py", "app/greet.py", "dependency", "file"), "Python main to greet import missing")
	sha := map[string]string{}
	for _, f := range extracted.Manifest.Files {
		sha[f.Path] = f.SHA256
	}
	for _, result := range []codegraph.Result{symbols, files, calls, deps} {
		require(result.Receipt.GraphDigest == receipt.GraphDigest && result.Manifest.Digest == receipt.SourceDigest, "query source receipt mismatch")
		for _, n := range result.Nodes {
			if n.Kind != "external" && n.Kind != "module" {
				require(n.FileSHA == sha[n.File] && n.FileSHA != "", "query source hash mismatch")
			}
		}
	}
	var impact codegraph.Result
	for _, symbol := range symbols.Nodes {
		impact = query("impact", "", symbol.ID)
		main := "src/main.ts"
		if symbol.Language == "python" {
			main = "app/main.py"
		}
		require(exact(impact, "calls", main, symbol.File, "symbol", "symbol"), "static impact missing exact caller")
		found := false
		for _, n := range impact.Nodes {
			if n.Kind == "file" && n.File == main && n.FileSHA == sha[main] {
				found = true
			}
		}
		require(found, "static impact missing caller file source")
	}
	report["queries"] = map[string]any{"symbols": symbols, "files": files, "calls": calls, "dependencies": deps, "impact": impact}
	cases = append(cases, "actual_Ladybug_symbols_files_calls_dependencies_static_impact")
	_, e = client.CodeQuery(ctx, codegraph.Query{Dataset: uuid.NewString(), Snapshot: receipt.Snapshot, Mode: "symbols", Text: "greet", Limit: 10, Hops: 1})
	require(e != nil, "foreign dataset returned graph")
	cases = append(cases, "independent_code_dataset_excludes_foreign_scope")
	again, e := client.CodeBuild(ctx, extracted.Graph)
	must(e)
	require(again.GraphDigest == receipt.GraphDigest, "replay receipt changed")
	cases = append(cases, "immutable_snapshot_idempotent_replay")
	must(client.Close())
	client = nil
	client, e = engine.Start(ctx, cfg)
	must(e)
	require(len(query("symbols", "greet", "").Nodes) == 2, "graph lost after worker restart")
	cases = append(cases, "actual_worker_restart_persists_Ladybug_graph")
	mutated := extracted.Graph
	mutated.Nodes = append([]codegraph.Node{}, extracted.Graph.Nodes...)
	mutated.Nodes[0].Name += " synthetic replay conflict"
	_, e = client.CodeBuild(ctx, mutated)
	require(e != nil, "replay overwrote immutable graph")
	cases = append(cases, "explicit_import_conflict_fault_rejected")
	must(os.Rename(filepath.Join(repo, "app/greet.py"), filepath.Join(repo, "app/renamed.py")))
	must(os.Remove(filepath.Join(repo, "src/main.ts")))
	updated, e := codegraph.Extract(ctx, codegraph.ExtractOptions{Executable: *enola, Worktree: repo, Directory: filepath.Join(root, "update"), Dataset: dataset})
	must(e)
	require(updated.Manifest.Digest != extracted.Manifest.Digest, "source update not detected")
	newReceipt, e := client.CodeBuild(ctx, updated.Graph)
	must(e)
	fresh, e := client.CodeQuery(ctx, codegraph.Query{Dataset: dataset, Snapshot: newReceipt.Snapshot, Mode: "files", Text: "", Limit: 30, Hops: 1})
	must(e)
	expected := map[string]bool{}
	for _, f := range updated.Manifest.Files {
		expected[f.Path] = true
	}
	require(len(fresh.Nodes) == len(expected), "new snapshot missing source files")
	for _, n := range fresh.Nodes {
		require(n.File != "src/main.ts" && n.File != "app/greet.py", "deleted/renamed source in new snapshot")
		require(expected[n.File], "new snapshot returned foreign file")
		delete(expected, n.File)
	}
	require(len(expected) == 0, "new snapshot incomplete file set")
	cases = append(cases, "actual_deleted_renamed_sources_new_snapshot_excludes_old_nodes")
	must(os.WriteFile(filepath.Join(repo, "too-large.ts"), make([]byte, codegraph.MaxFileBytes+1), 0600))
	_, e = codegraph.Capture(ctx, repo, "")
	require(e != nil, "size budget not enforced")
	cases = append(cases, "explicit_oversize_source_fault_rejected")
	must(os.Remove(filepath.Join(repo, "too-large.ts")))
	must(os.WriteFile(filepath.Join(repo, "control\x01.ts"), []byte("export function syntheticControl() {}"), 0600))
	_, e = codegraph.Capture(ctx, repo, "")
	require(e != nil, "control-character path accepted")
	control := extracted.Graph
	control.Dataset = uuid.NewString()
	control.Snapshot = uuid.NewSHA1(uuid.MustParse(control.Dataset), []byte(control.Manifest.Digest)).String()
	control.Nodes = append([]codegraph.Node{}, extracted.Graph.Nodes...)
	control.Nodes[0].Name += "\x01"
	_, e = client.CodeBuild(ctx, control)
	var controlRemote *engine.RemoteError
	require(errors.As(e, &controlRemote) && controlRemote.Code == "engine_error" && controlRemote.Message == "invalid code node", "worker did not reject control-character code source explicitly")
	controlFollow, e := client.CodeQuery(ctx, codegraph.Query{Dataset: dataset, Snapshot: newReceipt.Snapshot, Mode: "symbols", Text: "greet", Limit: 30, Hops: 1})
	must(e)
	require(len(controlFollow.Nodes) == 2, "worker failed after control-character rejection")
	cases = append(cases, "control_character_source_and_worker_import_rejected")
	_, e = client.CodeQuery(ctx, codegraph.Query{Dataset: dataset, Snapshot: receipt.Snapshot, Mode: "cypher", Text: "MATCH", Limit: 10, Hops: 1})
	var remote *engine.RemoteError
	require(errors.As(e, &remote) && remote.Code == "engine_error" && remote.Message == "invalid code query", "arbitrary query did not return explicit rejection")
	select {
	case <-client.Done():
		panic("worker crashed after invalid query")
	default:
	}
	follow, e := client.CodeQuery(ctx, codegraph.Query{Dataset: dataset, Snapshot: newReceipt.Snapshot, Mode: "symbols", Text: "greet", Limit: 30, Hops: 1})
	must(e)
	require(len(follow.Nodes) == 2, "worker unavailable after rejecting invalid query")
	cases = append(cases, "invalid_query_rejected_worker_stays_usable")
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		panic(ctx.Err())
	}
	report["status"] = "passed"
	report["completed_at"] = time.Now().UTC()
	return 0
}
