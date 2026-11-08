// Explicit isolated offline replay acceptance. This checker is not distributed
// with the App and never starts an Agent/model or reads a default memory root.
package main

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/recoveryfiles"
	"github.com/zwhu/memlane/internal/store"
)

func marker(path string, out any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 65536 {
		return errors.New("ordinary bounded marker required")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return err
	}
	if len(data) > 65536 {
		return errors.New("marker exceeds limit")
	}
	return json.Unmarshal(data, out)
}

// Validate every input before Store.Open or production Restore can write.
// This checker accepts completed replay only, never a new restore request.
func validateReplay(ctx context.Context, root, backup, operation string) error {
	for _, id := range []string{backup, operation} {
		parsed, e := uuid.Parse(id)
		if e != nil || parsed.String() != id {
			return errors.New("canonical completed replay IDs required")
		}
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || filepath.Base(root) != "data" || filepath.Base(filepath.Dir(root)) != "client" || !strings.HasPrefix(filepath.Base(filepath.Dir(filepath.Dir(root))), "memlane-p3-code-app-") {
		return errors.New("marked synthetic client/data root required")
	}
	fixture := filepath.Dir(filepath.Dir(root))
	for _, p := range []string{fixture, filepath.Dir(root), root} {
		info, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("replay root directories must not be links")
		}
	}
	resolved, e := filepath.EvalSymlinks(root)
	if e != nil {
		return e
	}
	resolvedFixture, e := filepath.EvalSymlinks(fixture)
	if e != nil {
		return e
	}
	temp, e := filepath.EvalSymlinks(os.TempDir())
	if e != nil {
		return e
	}
	if filepath.Dir(resolvedFixture) != filepath.Clean(temp) || resolved != filepath.Join(resolvedFixture, "client/data") {
		return errors.New("replay root escaped marked temporary fixture")
	}
	var info struct {
		Synthetic   bool `json:"synthetic_only"`
		UserData    bool `json:"user_data_copied"`
		AgentConfig bool `json:"agent_configs_copied"`
	}
	if e = marker(filepath.Join(fixture, "source-info.json"), &info); e != nil {
		return e
	}
	if !info.Synthetic || info.UserData || info.AgentConfig {
		return errors.New("synthetic source marker required")
	}
	var config struct {
		Root string `json:"root"`
	}
	if e = marker(filepath.Join(fixture, "runtime-config.json"), &config); e != nil {
		return e
	}
	bound, e := filepath.EvalSymlinks(config.Root)
	if e != nil {
		return e
	}
	if bound != filepath.Dir(resolved) {
		return errors.New("runtime config differs from replay fixture")
	}
	if _, e = os.Lstat(filepath.Join(root, ".restore-pending.json")); e == nil {
		return errors.New("completed replay refuses pending restore")
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	var state records.ServiceState
	if e = marker(filepath.Join(root, "service-state.json"), &state); e != nil {
		return e
	}
	if !state.Stopped {
		return errors.New("completed replay requires stopped service")
	}
	list, e := (recoveryfiles.RestoreManager{Root: root}).List(ctx)
	if e != nil {
		return e
	}
	var completed *records.RestoreReceipt
	for i := range list {
		if list[i].State != "restored_stopped" {
			return errors.New("completed replay refuses incomplete catalogue")
		}
		if list[i].ID == operation {
			completed = &list[i]
		}
	}
	if completed == nil || completed.Backup != backup {
		return errors.New("completed replay receipt required before opening SQL")
	}
	source, e := (recoveryfiles.Manager{Root: root}).Verify(ctx, backup)
	if e != nil {
		return e
	}
	if completed.Vault != source.Receipt.Vault || completed.SourceSHA != source.Receipt.SHA256 {
		return errors.New("completed replay receipt differs from source backup")
	}
	path := filepath.Join(root, "memlane.sqlite")
	file, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !file.Mode().IsRegular() || file.Mode()&os.ModeSymlink != 0 {
		return errors.New("ordinary current database required")
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	header := make([]byte, 100)
	if _, e = io.ReadFull(f, header); e != nil {
		return e
	}
	if string(header[:16]) != "SQLite format 3\x00" || int(binary.BigEndian.Uint32(header[60:64])) != store.SchemaVersion {
		return errors.New("current database must already match shipped schema; migration refused")
	}
	return ctx.Err()
}

func tables(db *sql.DB) (map[string][][]any, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := map[string][][]any{}
	for _, name := range names {
		quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
		rows, err = db.Query(`SELECT * FROM ` + quoted + ` ORDER BY 1,2`)
		if err != nil {
			return nil, err
		}
		columns, e := rows.Columns()
		if e != nil {
			rows.Close()
			return nil, e
		}
		values := [][]any{}
		for rows.Next() {
			row := make([]any, len(columns))
			pointers := make([]any, len(row))
			for i := range row {
				pointers[i] = &row[i]
			}
			if err = rows.Scan(pointers...); err != nil {
				rows.Close()
				return nil, err
			}
			values = append(values, row)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		out[name] = values
	}
	return out, nil
}

func main() {
	root := flag.String("root", "", "explicit marked synthetic service data root")
	backup := flag.String("backup", "", "completed source backup ID")
	operation := flag.String("operation", "", "completed restore ID")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := validateReplay(ctx, *root, *backup, *operation); err != nil {
		panic(err)
	}
	path := filepath.Join(*root, "memlane.sqlite")
	db, err := store.Open(path)
	if err != nil {
		panic(err)
	}
	before, err := tables(db.DB)
	if err != nil {
		db.Close()
		panic(err)
	}
	if err = db.Close(); err != nil {
		panic(err)
	}
	receipt, err := (recoveryfiles.RestoreManager{Root: *root}).Restore(ctx, *backup, *operation, store.PrepareRestoredSnapshot)
	if err != nil {
		panic(err)
	}
	db, err = store.Open(path)
	if err != nil {
		panic(err)
	}
	after, err := tables(db.DB)
	if err != nil {
		db.Close()
		panic(err)
	}
	if err = db.Close(); err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(before, after) {
		panic("completed restore replay changed current tables")
	}
	result := map[string]any{"status": "passed", "synthetic_only": true, "actual_app": false, "actual_model": false, "completed_restore_replayed": receipt.ID, "current_tables_unchanged": true, "table_count": len(after), "service_must_remain_stopped": true}
	data, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(data))
}
