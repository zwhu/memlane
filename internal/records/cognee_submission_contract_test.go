package records

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCogneeMaterialGoJSONAcceptedByPythonBoundary(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable for cross-language contract")
	}
	text := "Synthetic exact material for the Go to Python boundary."
	sum := sha256.Sum256([]byte(text))
	id := "11111111-1111-4111-8111-111111111111"
	material := CogneeSubmissionMaterial{Library: id, Operation: id, Instance: id, Dataset: id, Project: id, Memory: id, Revision: 1, Origin: "knowledge", Text: text, Digest: hex.EncodeToString(sum[:]), Processing: ProcessingSnapshot{Revision: ProcessingRevision{ID: id, Digest: hex.EncodeToString(sum[:])}, DependencyDigest: hex.EncodeToString(sum[:])}}
	raw, err := json.Marshal(material)
	if err != nil {
		t.Fatal(err)
	}
	module, err := filepath.Abs(filepath.Join("..", "..", "engine", "python"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-c", `import json,sys
sys.path.insert(0,sys.argv[1])
import cognee_submission
m=json.load(sys.stdin)
cognee_submission.validate({'material':m,'gateway':{'endpoint':'http://127.0.0.1:12345/v1','token':'synthetic-no-network'}},m['instance'])
assert cognee_submission.document_id(m)
`, module)
	command.Stdin = bytes.NewReader(raw)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("actual Go material JSON rejected at Python boundary: %v\n%s", err, output)
	}
}
