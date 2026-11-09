package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRequestSequenceFollowsConcurrentWireOrder(t *testing.T) {
	var wire bytes.Buffer
	client := &Client{stdin: writeBuffer{&wire}, requestIDScheme: "sequence-uuid-v1"}
	var pending sync.WaitGroup
	for range 40 {
		pending.Add(1)
		go func() {
			defer pending.Done()
			for range 300 {
				if err := client.write(map[string]any{"protocol": 1, "op": "health", "id": uuid.NewString()}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	pending.Wait()
	decoder := json.NewDecoder(&wire)
	for number := 1; number <= 12000; number++ {
		var request struct {
			ID string `json:"id"`
		}
		if err := decoder.Decode(&request); err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(request.ID, ":")
		if len(parts) != 3 || parts[0] != "seq" || parts[1] != strconv.Itoa(number) {
			t.Fatalf("wire sequence reordered at %d: %s", number, request.ID)
		}
		if _, err := uuid.Parse(parts[2]); err != nil {
			t.Fatal(err)
		}
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatal("unexpected extra request", err)
	}
}

type writeBuffer struct{ *bytes.Buffer }

func (writeBuffer) Close() error { return nil }

func TestRequestSequenceReadyAgainstStartupCancellation(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("requires a Python protocol process fixture")
	}
	root := t.TempDir()
	worker := filepath.Join(root, "protocol-worker")
	// Real subprocess protocol fixture: no model, graph or successful business
	// operation is fabricated. Race detection covers ready versus Close writes.
	script := fmt.Sprintf("#!%s\n", python) + `import json,sys
def emit(value):
 print(json.dumps(value),flush=True)
emit({"protocol":1,"event":"hello","worker_version":"0.0.1"})
emit({"protocol":1,"event":"ready","worker_version":"0.0.1","ready":True,"generation":"fixture","request_id_scheme":"sequence-uuid-v1"})
for line in sys.stdin:
 request=json.loads(line)
 emit({"protocol":1,"id":request["id"],"ok":True,"result":{"worker_version":"0.0.1","ready":True,"generation":"fixture"}})
 if request["op"]=="shutdown": break
`
	if err := os.WriteFile(worker, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for number := range 30 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(10+number%5*5)*time.Millisecond)
		client, _ := Start(ctx, Config{Executable: worker, DataDir: filepath.Join(root, fmt.Sprint(number)), ModelDir: root, Library: filepath.Join(root, "not-used")})
		cancel()
		if client != nil {
			client.Close()
		}
	}
}
