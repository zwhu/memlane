// Runs functional checks of the Go client against an actual packaged worker.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/memory"
	"github.com/zwhu/memlane/internal/store"
)

func require(ok bool, message string) {
	if !ok {
		panic(message)
	}
}

func main() {
	worker := flag.String("worker", "", "worker executable")
	library := flag.String("library", "", "dynamic library")
	model := flag.String("model", "", "model directory")
	data := flag.String("data", "", "synthetic data directory")
	version := flag.String("worker-version", "0.0.1", "expected packaged worker version")
	healthRequests := flag.Int("health-requests", 0, "real requests before functional checks, including crossing the old lifetime limit")
	denyNetwork := flag.Bool("deny-network", false, "deny network access to the actual worker")
	flag.Parse()
	report := map[string]any{"status": "failed"}
	defer func() {
		if e := recover(); e != nil {
			report["error"] = fmt.Sprint(e)
		}
		json.NewEncoder(os.Stdout).Encode(report)
		if report["status"] != "passed" {
			os.Exit(1)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg := engine.Config{Executable: *worker, DataDir: filepath.Join(*data, "index"), ModelDir: *model, Library: *library, ExpectedVersion: *version, DenyNetwork: *denyNetwork}
	client, err := engine.Start(ctx, cfg)
	require(err == nil, fmt.Sprint(err))
	defer client.Close()
	for number := 0; number < *healthRequests; number++ {
		var health struct {
			Ready      bool   `json:"ready"`
			Generation string `json:"generation"`
		}
		err = client.Call(ctx, "health", nil, &health)
		require(err == nil && health.Ready && health.Generation == client.Generation, fmt.Sprint(number, health, err))
	}
	report["health_requests_before_functional_checks"] = *healthRequests
	db, err := store.Open(filepath.Join(*data, "business.sqlite"))
	require(err == nil, fmt.Sprint(err))
	defer db.Close()
	svc := memory.Service{Store: db, Engine: client}
	dataset, id := uuid.NewString(), uuid.NewString()
	v, err := db.Apply(ctx, store.Mutation{OperationID: "first", Kind: "save", Dataset: dataset, Memory: id, Text: "goWorkerClientToken 原文验证"})
	require(err == nil, fmt.Sprint(err))
	err = svc.IndexCurrent(ctx, id)
	require(err == nil, fmt.Sprint(err))
	hits, err := svc.Search(ctx, []string{dataset}, "goWorkerClientToken", 3)
	require(err == nil && len(hits) == 1 && hits[0].Version.Revision == v.Revision, fmt.Sprint(hits, err))
	report["real_store_worker_roundtrip"] = true
	lateID := uuid.NewString()
	long := strings.Repeat("天地玄黄宇宙洪荒日月盈昃辰宿列张", 1500)
	_, err = db.Apply(ctx, store.Mutation{OperationID: "timeout", Kind: "save", Dataset: dataset, Memory: lateID, Text: long})
	require(err == nil, fmt.Sprint(err))
	short, stop := context.WithTimeout(ctx, 10*time.Millisecond)
	started := time.Now()
	err = svc.IndexCurrent(short, lateID)
	stop()
	require(err != nil, "timeout returned successful index")
	report["client_timeout_ms"] = float64(time.Since(started).Microseconds()) / 1000
	var indexed int64
	err = db.DB.QueryRow(`SELECT indexed_revision FROM memories WHERE id=?`, lateID).Scan(&indexed)
	require(err == nil && indexed == 0, "timed-out original marked indexed")
	pid := client.PID()
	client.Close()
	require(syscall.Kill(pid, 0) == syscall.ESRCH, "closed worker still exists")
	_, err = svc.Search(ctx, []string{dataset}, "goWorkerClientToken", 3)
	require(err != nil, "closed worker returned empty success")
	report["cancel_original_not_indexed"] = true
	report["process_stopped"] = true
	report["closed_worker_returns_error"] = true
	client, err = engine.Start(ctx, cfg)
	require(err == nil, fmt.Sprint(err))
	defer client.Close()
	svc.Engine = client
	hits, err = svc.Search(ctx, []string{dataset}, "goWorkerClientToken", 3)
	require(err == nil && len(hits) == 1 && hits[0].Version.Memory == id, fmt.Sprint(hits, err))
	_, err = svc.Search(ctx, []string{dataset}, "timeout", 3)
	require(err == nil, fmt.Sprint(err))
	bad := cfg
	bad.DataDir = filepath.Join(*data, "incompatible")
	bad.ExpectedVersion = "99.0.0"
	_, err = engine.Start(ctx, bad)
	require(err != nil, "incompatible worker accepted")
	report["restart_valid_bindings"] = true
	report["incompatible_version_rejected"] = true
	// Confirm that failure tasks remain separate from the worker process.
	var state string
	err = db.DB.QueryRow(`SELECT state FROM jobs WHERE memory=?`, lateID).Scan(&state)
	require(err == nil && state == "failed", fmt.Sprint(state, err))
	report["failed_job_persisted"] = true
	report["generation"] = client.Generation
	report["status"] = "passed"
	_ = sql.ErrNoRows
	_ = errors.Is
}
