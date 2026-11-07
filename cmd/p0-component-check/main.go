// P0-only functional component checks using real runtime/model archives.
package main

import (
	"archive/tar"
	"bytes"
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
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/components"
	"github.com/zwhu/memlane/internal/engine"
)

func require(ok bool, message any) {
	if !ok {
		panic(fmt.Sprint(message))
	}
}
func digest(path string) string {
	f, err := os.Open(path)
	require(err == nil, err)
	defer f.Close()
	h := sha256.New()
	_, err = io.Copy(h, f)
	require(err == nil, err)
	return hex.EncodeToString(h.Sum(nil))
}
func descriptor(path string) components.Descriptor {
	f, err := os.Open(path)
	require(err == nil, err)
	defer f.Close()
	g, err := gzip.NewReader(f)
	require(err == nil, err)
	defer g.Close()
	r := tar.NewReader(g)
	var meta components.Metadata
	var manifest []byte
	var total int64
	for {
		h, e := r.Next()
		if e == io.EOF {
			break
		}
		require(e == nil, e)
		total += h.Size
		if h.Name == "component.json" {
			manifest, err = io.ReadAll(r)
			require(err == nil, err)
			require(json.Unmarshal(manifest, &meta) == nil, "invalid metadata")
		}
	}
	h := sha256.Sum256(manifest)
	info, err := os.Stat(path)
	require(err == nil, err)
	return components.Descriptor{ID: meta.ID, Version: meta.Version, Platform: meta.Platform, ProtocolMin: meta.ProtocolMin, ProtocolMax: meta.ProtocolMax, Bytes: info.Size(), UnpackedBytes: total, SHA256: digest(path), ManifestSHA256: hex.EncodeToString(h[:])}
}

func main() {
	repo := flag.String("repo", "", "repository with handoff executor")
	workerArchive := flag.String("worker", "", "actual Rust worker archive")
	modelArchive := flag.String("model", "", "actual model archive")
	single := flag.String("single-config", "", "P0 child process configuration")
	flag.Parse()
	if *single != "" {
		runChild(*single)
		return
	}
	report := map[string]any{"status": "failed", "synthetic_only": true}
	defer func() {
		if err := recover(); err != nil {
			report["error"] = fmt.Sprint(err)
		}
		data, _ := json.MarshalIndent(report, "", "  ")
		_ = os.WriteFile(filepath.Join(*repo, "build/p0-engine/component-verification.json"), append(data, '\n'), 0600)
		fmt.Println(string(data))
		if report["status"] != "passed" {
			os.Exit(1)
		}
	}()
	root, err := os.MkdirTemp("", "memlane-components-p0-")
	require(err == nil, err)
	defer os.RemoveAll(root)
	key, private, err := ed25519.GenerateKey(rand.Reader)
	require(err == nil, err)
	descriptors := []components.Descriptor{descriptor(*workerArchive), descriptor(*modelArchive)}
	archives := map[string]string{descriptors[0].ID: *workerArchive, descriptors[1].ID: *modelArchive}
	var mu sync.Mutex
	mode := "normal"
	var payload []byte
	requests := map[string]int{}
	var ranges []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		state := mode
		requests[r.URL.Path]++
		feed := append([]byte(nil), payload...)
		if r.Header.Get("Range") != "" {
			ranges = append(ranges, r.Header.Get("Range"))
		}
		mu.Unlock()
		if r.URL.Path == "/feed" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(feed)
			return
		}
		mu.Lock()
		path, ok := archives[strings.TrimPrefix(r.URL.Path, "/")]
		mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		f, e := os.Open(path)
		if e != nil {
			http.Error(w, "source unavailable", 500)
			return
		}
		defer f.Close()
		info, _ := f.Stat()
		if state == "truncate" && r.Header.Get("Range") == "" {
			w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
			_, _ = io.CopyN(w, f, 128*1024)
			return
		}
		if state == "corrupt" {
			w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
			b := []byte{0}
			_, _ = w.Write(b)
			_, _ = f.Seek(1, io.SeekStart)
			_, _ = io.Copy(w, f)
			return
		}
		http.ServeContent(w, r, info.Name(), time.Time{}, f)
	}))
	defer server.Close()
	setFeed := func(ds []components.Descriptor, tamper bool) {
		data, e := json.Marshal(components.Feed{Version: 1, Components: ds})
		require(e == nil, e)
		signature := ed25519.Sign(private, data)
		if tamper {
			signature[0] ^= 1
		}
		signed, e := json.Marshal(components.SignedFeed{Payload: data, Signature: base64.StdEncoding.EncodeToString(signature)})
		require(e == nil, e)
		mu.Lock()
		payload = signed
		mu.Unlock()
	}
	for i := range descriptors {
		descriptors[i].URL = server.URL + "/" + descriptors[i].ID
	}
	setFeed(descriptors, false)
	manager, err := components.New(filepath.Join(root, "installed"), key, 1, true)
	require(err == nil, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	steps := []map[string]any{}
	step := func(slug, focus string, action func() map[string]any) {
		handoff := func(s, result string) {
			cmd := exec.Command("python3", filepath.Join(*repo, "scripts/p0-handoff.py"), s, focus, "--result", result)
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			require(cmd.Run() == nil, "handoff failed")
		}
		handoff(slug, "本步待验收")
		entry := map[string]any{"step": slug, "status": "running"}
		steps = append(steps, entry)
		report["steps"] = steps
		started := time.Now()
		evidence := action()
		entry["status"] = "passed"
		entry["evidence"] = evidence
		entry["wall_ms"] = float64(time.Since(started).Microseconds()) / 1000
		data, _ := json.MarshalIndent(report, "", "  ")
		require(os.WriteFile(filepath.Join(*repo, "build/p0-engine/component-verification.json"), append(data, '\n'), 0600) == nil, "evidence write failed")
		summary, _ := json.Marshal(evidence)
		handoff(slug+"-done", string(summary))
	}
	var engineInstall, modelInstall components.Installed
	progress := []components.Progress{}
	step("d1-signed-download", "实际签名清单、引擎及模型下载、哈希、版本目录安装与本地推理", func() map[string]any {
		_, e := manager.Feed(ctx, server.URL+"/feed")
		require(e == nil, e)
		engineInstall, e = manager.Prepare(ctx, descriptors[0], func(p components.Progress) { progress = append(progress, p) })
		require(e == nil, e)
		modelInstall, e = manager.Prepare(ctx, descriptors[1], func(p components.Progress) { progress = append(progress, p) })
		require(e == nil, e)
		client, e := engine.Start(ctx, engine.Config{Executable: filepath.Join(engineInstall.Directory, engineInstall.Metadata.Executable), Library: filepath.Join(engineInstall.Directory, engineInstall.Metadata.Library), ModelDir: modelInstall.Directory, DataDir: filepath.Join(root, "knowledge")})
		require(e == nil, e)
		defer client.Close()
		dataset, memory := uuid.NewString(), uuid.NewString()
		receipt, e := client.Index(ctx, engine.Record{Dataset: dataset, Memory: memory, Revision: 1, Text: "downloadedComponentToken：自动准备后的本地原文"})
		require(e == nil, e)
		hits, e := client.Search(ctx, dataset, "downloadedComponentToken", 3)
		require(e == nil && len(hits) > 0 && hits[0].Memory == memory, fmt.Sprint(hits, e))
		require(receipt.Generation == hits[0].Generation, "generation mismatch")
		return map[string]any{"downloaded_bytes": engineInstall.DownloadedBytes + modelInstall.DownloadedBytes, "versions_installed": true, "real_index_search": true, "progress_events": len(progress)}
	})
	step("d1-failure-resume", "真实传输中断、Range重试、校验失败重试及已有版本保留", func() map[string]any {
		fresh, e := components.New(filepath.Join(root, "retry"), key, 1, true)
		require(e == nil, e)
		_, e = fresh.Feed(ctx, server.URL+"/feed")
		require(e == nil, e)
		mu.Lock()
		mode = "truncate"
		mu.Unlock()
		_, e = fresh.Prepare(ctx, descriptors[0], nil)
		require(e != nil, "truncated source accepted")
		mu.Lock()
		mode = "normal"
		mu.Unlock()
		resumed, e := fresh.Prepare(ctx, descriptors[0], nil)
		require(e == nil, e)
		require(resumed.DownloadedBytes == descriptors[0].Bytes-128*1024, "range did not reuse partial transfer")
		bad, e := components.New(filepath.Join(root, "checksum"), key, 1, true)
		require(e == nil, e)
		_, e = bad.Feed(ctx, server.URL+"/feed")
		require(e == nil, e)
		mu.Lock()
		mode = "corrupt"
		mu.Unlock()
		_, e = bad.Prepare(ctx, descriptors[0], nil)
		require(e != nil && strings.Contains(e.Error(), "SHA-256"), e)
		mu.Lock()
		mode = "normal"
		mu.Unlock()
		_, e = bad.Prepare(ctx, descriptors[0], nil)
		require(e == nil, e)
		reused, e := manager.Prepare(ctx, descriptors[0], nil)
		require(e == nil && reused.Reused && reused.DownloadedBytes == 0, e)
		return map[string]any{"interrupted_download_rejected": true, "range_resumed_bytes": resumed.DownloadedBytes, "checksum_failure_retried": true, "valid_version_preserved": true}
	})
	step("d1-trust-compatibility", "签名篡改、协议不兼容及未授权描述符实际拒绝", func() map[string]any {
		setFeed(descriptors, true)
		_, e := manager.Feed(ctx, server.URL+"/feed")
		require(e != nil, "invalid signature accepted")
		incompatible := append([]components.Descriptor(nil), descriptors...)
		incompatible[0].ProtocolMin = 2
		incompatible[0].ProtocolMax = 2
		setFeed(incompatible, false)
		_, e = manager.Feed(ctx, server.URL+"/feed")
		require(e != nil, "incompatible protocol accepted")
		unapproved := descriptors[0]
		unapproved.Version = "unapproved"
		_, e = manager.Prepare(ctx, unapproved, nil)
		require(e != nil, "unapproved descriptor accepted")
		setFeed(descriptors, false)
		_, e = manager.Feed(ctx, server.URL+"/feed")
		require(e == nil, e)
		return map[string]any{"invalid_signature_rejected": true, "protocol_incompatible_rejected": true, "unapproved_descriptor_rejected": true}
	})

	step("d1-link-safety", "实际签名归档的内部链接安装及外部、未声明链接拒绝", func() map[string]any {
		checks := map[string]bool{}
		for _, fixture := range []struct {
			id, target      string
			declared, valid bool
		}{
			{"p0-valid-links", "payload.bin", true, true},
			{"p0-external-link", "../../outside", true, false},
			{"p0-absolute-link", "/memlane-p0-forbidden", true, false},
			{"p0-unlisted-link", "payload.bin", false, false},
			{"p0-cycle-link", "alias.bin", true, false},
		} {
			archive := linkFixture(root, fixture.id, descriptors[0].Platform, fixture.target, fixture.declared)
			d := descriptor(archive)
			d.URL = server.URL + "/" + fixture.id
			mu.Lock()
			archives[fixture.id] = archive
			mu.Unlock()
			setFeed([]components.Descriptor{d}, false)
			fresh, e := components.New(filepath.Join(root, fixture.id), key, 1, true)
			require(e == nil, e)
			_, e = fresh.Feed(ctx, server.URL+"/feed")
			require(e == nil, e)
			installed, e := fresh.Prepare(ctx, d, nil)
			if fixture.valid {
				require(e == nil, e)
				target, e := os.Readlink(filepath.Join(installed.Directory, "alias.bin"))
				require(e == nil && target == "payload.bin", e)
				data, e := os.ReadFile(filepath.Join(installed.Directory, "alias.bin"))
				require(e == nil && string(data) == "synthetic link payload", e)
				require(os.Remove(filepath.Join(installed.Directory, "alias.bin")) == nil, "remove test link")
				require(os.Symlink("../../outside", filepath.Join(installed.Directory, "alias.bin")) == nil, "tamper test link")
				repaired, e := fresh.Prepare(ctx, d, nil)
				require(e == nil && !repaired.Reused, e)
				target, e = os.Readlink(filepath.Join(repaired.Directory, "alias.bin"))
				require(e == nil && target == "payload.bin", e)
			} else {
				require(e != nil, "invalid link archive accepted: "+fixture.id)
			}
			checks[fixture.id] = true
		}
		setFeed(descriptors, false)
		return map[string]any{"real_signed_link_cases": checks, "tampered_link_repaired": true}
	})
	step("d1-process-owner", "两个实际安装进程争用同一根，只下载一次并复用校验后的版本", func() map[string]any {
		setFeed(descriptors, false)
		cfg := childConfig{Root: filepath.Join(root, "concurrent"), Feed: server.URL + "/feed", PublicKey: base64.StdEncoding.EncodeToString(key), ID: descriptors[0].ID}
		config := filepath.Join(root, "child-config.json")
		require(writeFixtureJSON(config, cfg) == nil, "child config write")
		executable, e := os.Executable()
		require(e == nil, e)
		mu.Lock()
		before := requests["/"+cfg.ID]
		mu.Unlock()
		results := make(chan components.Installed, 2)
		failures := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() {
				cmd := exec.CommandContext(ctx, executable, "--single-config", config)
				output, e := cmd.Output()
				if e != nil {
					failures <- e
					return
				}
				var installed components.Installed
				if e = json.Unmarshal(output, &installed); e != nil {
					failures <- e
					return
				}
				results <- installed
			}()
		}
		installed := []components.Installed{}
		for i := 0; i < 2; i++ {
			select {
			case e := <-failures:
				panic(e)
			case result := <-results:
				installed = append(installed, result)
			case <-ctx.Done():
				panic(ctx.Err())
			}
		}
		total := installed[0].DownloadedBytes + installed[1].DownloadedBytes
		require(total == descriptors[0].Bytes, "two owners downloaded duplicate content")
		require(installed[0].Reused != installed[1].Reused, "expected exactly one reused installation")
		mu.Lock()
		count := requests["/"+cfg.ID] - before
		mu.Unlock()
		require(count == 1, "multiple download owners")
		return map[string]any{"real_process_count": 2, "source_archive_requests": count, "downloaded_bytes": total, "one_verified_reuse": true}
	})
	step("d1-offline-reuse", "关闭测试源后离线签名缓存及组件复用、空缓存拒绝就绪", func() map[string]any {
		server.Close()
		offline, e := components.New(manager.Root, key, 1, true)
		require(e == nil, e)
		_, e = offline.CachedFeed(server.URL + "/feed")
		require(e == nil, e)
		for _, d := range descriptors {
			installed, e := offline.Prepare(ctx, d, nil)
			require(e == nil && installed.Reused && installed.DownloadedBytes == 0, e)
		}
		blank, e := components.New(filepath.Join(root, "blank-offline"), key, 1, true)
		require(e == nil, e)
		_, e = blank.CachedFeed(server.URL + "/feed")
		require(e != nil, "empty offline cache considered ready")
		client, e := engine.Start(ctx, engine.Config{Executable: filepath.Join(engineInstall.Directory, engineInstall.Metadata.Executable), Library: filepath.Join(engineInstall.Directory, engineInstall.Metadata.Library), ModelDir: modelInstall.Directory, DenyNetwork: true, DataDir: filepath.Join(root, "offline-knowledge")})
		require(e == nil, e)
		defer client.Close()
		dataset, memory := uuid.NewString(), uuid.NewString()
		_, e = client.Index(ctx, engine.Record{Dataset: dataset, Memory: memory, Revision: 1, Text: "offlineReadyToken 可离线检索"})
		require(e == nil, e)
		hits, e := client.Search(ctx, dataset, "offlineReadyToken", 1)
		require(e == nil && len(hits) == 1 && hits[0].Memory == memory, e)
		return map[string]any{"cached_signature_verified": true, "components_reused_zero_download": true, "empty_offline_not_ready": true, "offline_index_search": true, "controlled_source_stopped": true}
	})
	report["status"] = "passed"
	report["source_requests"] = requests
	report["range_requests"] = ranges
	report["progress"] = progress
	report["formal_release_signing"] = "not configured; ephemeral test key only"
}

type childConfig struct{ Root, Feed, PublicKey, ID string }

func writeFixtureJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
func runChild(path string) {
	data, err := os.ReadFile(path)
	require(err == nil, err)
	var cfg childConfig
	require(json.Unmarshal(data, &cfg) == nil, "child config")
	key, err := base64.StdEncoding.DecodeString(cfg.PublicKey)
	require(err == nil, err)
	m, err := components.New(cfg.Root, ed25519.PublicKey(key), 1, true)
	require(err == nil, err)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	feed, err := m.Feed(ctx, cfg.Feed)
	require(err == nil, err)
	for _, d := range feed.Components {
		if d.ID == cfg.ID {
			installed, err := m.Prepare(ctx, d, nil)
			require(err == nil, err)
			require(json.NewEncoder(os.Stdout).Encode(installed) == nil, "child output")
			return
		}
	}
	panic("child component absent")
}
func linkFixture(root, id, platform, target string, declared bool) string {
	payload := []byte("synthetic link payload")
	checksum := sha256.Sum256(payload)
	meta := components.Metadata{ID: id, Version: "1", Platform: platform, ProtocolMin: 1, ProtocolMax: 1, Files: []components.Asset{{Path: "payload.bin", Bytes: int64(len(payload)), SHA256: hex.EncodeToString(checksum[:])}}}
	if declared {
		meta.Links = []components.Link{{Path: "alias.bin", Target: target}}
	}
	manifest, err := json.Marshal(meta)
	require(err == nil, err)
	path := filepath.Join(root, id+".tar.gz")
	file, err := os.Create(path)
	require(err == nil, err)
	compressed := gzip.NewWriter(file)
	writer := tar.NewWriter(compressed)
	for name, data := range map[string][]byte{"payload.bin": payload, "component.json": manifest} {
		require(writer.WriteHeader(&tar.Header{Name: name, Size: int64(len(data)), Mode: 0600}) == nil, "fixture header")
		_, err = io.Copy(writer, bytes.NewReader(data))
		require(err == nil, err)
	}
	require(writer.WriteHeader(&tar.Header{Name: "alias.bin", Typeflag: tar.TypeSymlink, Linkname: target, Mode: 0777}) == nil, "fixture link")
	require(writer.Close() == nil, "fixture tar")
	require(compressed.Close() == nil, "fixture gzip")
	require(file.Close() == nil, "fixture close")
	return path
}
