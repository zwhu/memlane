// This explicit development fixture is never included in the client bundle.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func main() {
	root := flag.String("root", "", "stopped synthetic C2 service root only")
	flag.Parse()
	if !filepath.IsAbs(*root) || filepath.Base(*root) != "data" || filepath.Base(filepath.Dir(*root)) != "client" || !strings.HasPrefix(filepath.Base(filepath.Dir(filepath.Dir(*root))), "memlane-p3-c2-app-") {
		fail(fmt.Errorf("isolated C2 synthetic root required"))
	}
	fixture, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(*root)))
	if err != nil {
		fail(err)
	}
	realRoot, err := filepath.EvalSymlinks(*root)
	if err != nil || realRoot != filepath.Join(fixture, "client", "data") {
		fail(fmt.Errorf("service data escapes synthetic fixture"))
	}
	realDB, err := filepath.EvalSymlinks(filepath.Join(*root, "memlane.sqlite"))
	if err != nil || realDB != filepath.Join(realRoot, "memlane.sqlite") {
		fail(fmt.Errorf("database escapes synthetic fixture"))
	}

	state, err := os.ReadFile(filepath.Join(*root, "service-state.json"))
	if err != nil {
		fail(err)
	}
	var stopped struct{ Stopped bool }
	if json.Unmarshal(state, &stopped) != nil || !stopped.Stopped {
		fail(fmt.Errorf("service must be explicitly stopped"))
	}
	s, err := store.Open(filepath.Join(*root, "memlane.sqlite"))
	if err != nil {
		fail(err)
	}
	defer s.Close()
	ctx := context.Background()
	scopes, err := s.Scopes(ctx)
	if err != nil {
		fail(err)
	}
	project := ""
	for _, v := range scopes {
		if v.Name == "C2 合成项目 A" && v.Kind == "project" {
			project = v.ID
		}
	}
	if project == "" {
		fail(fmt.Errorf("synthetic project missing"))
	}
	page, err := s.List(ctx, records.ListQuery{Datasets: []string{project}, Limit: 2, Kind: "knowledge"})
	if err != nil {
		fail(err)
	}
	if len(page.Items) != 2 {
		fail(fmt.Errorf("two active synthetic sources required"))
	}
	d := records.MergeStart{OperationID: uuid.NewString(), Project: project}
	for _, v := range page.Items {
		d.Inputs = append(d.Inputs, records.MergeTarget{Memory: v.Memory, Revision: v.Revision})
	}
	t, _, err := s.StartMerge(ctx, d)
	if err != nil {
		fail(err)
	}
	if err = s.ClaimMerge(ctx, t.ID, t.Attempt); err != nil {
		fail(err)
	}
	t, err = s.CompleteMerge(ctx, t.ID, t.Attempt, nil, map[string]bool{"ui_failure_fixture": true, "real_agent": false}, "agent_timeout")
	if err != nil {
		fail(err)
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"task": t.ID, "state": t.State, "error_code": t.Error, "ui_failure_fixture": true, "normal_model_replaced": false, "synthetic_only": true})
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
