package bootstrap

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type progressTestTransport func(*http.Request) (*http.Response, error)

func (fn progressTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type progressTestReader func([]byte) (int, error)

func (fn progressTestReader) Read(b []byte) (int, error) { return fn(b) }

func TestInstallProgressThrottlingAndMirrorReset(t *testing.T) {
	t.Run("throttle and completion", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var events []InstallProgress
			ctx := WithInstallProgress(t.Context(), func(p InstallProgress) { events = append(events, p) })
			progressComponent(ctx, "embedding", true)
			progressStage(ctx, "probing")
			progressAttempt(ctx, "downloading", "https://first.example/file", 1, 1)
			tracker := progressTracker(ctx)
			tracker.setLength(-1)
			reader := installProgressReader{strings.NewReader("abcdef"), tracker}
			b := make([]byte, 1)
			_, _ = reader.Read(b)
			time.Sleep(DefaultProgressThrottle - time.Millisecond)
			_, _ = reader.Read(b)
			if len(events) != 3 || !events[2].Indeterminate || events[2].BytesTotal != 0 {
				t.Fatalf("early ticks or incorrect unknown length: %+v", events)
			}
			time.Sleep(time.Millisecond)
			_, _ = reader.Read(b)
			_, _ = reader.Read(b)
			if len(events) != 4 || events[3].BytesDone != 3 || events[3].BytesPerSec <= 0 {
				t.Fatalf("expected one timed tick: %+v", events)
			}
			_, _ = io.Copy(io.Discard, reader)
			tracker.finishTransfer(6, false)
			progressStage(ctx, "verifying")
			if len(events) != 6 || events[4].BytesDone != 6 || events[5].Stage != "verifying" {
				t.Fatalf("completion or transition was throttled: %+v", events)
			}
		})
	})

	t.Run("mirror retry and cached batch", func(t *testing.T) {
		root := t.TempDir()
		journal := filepath.Join(root, "installation.json")
		if err := os.WriteFile(journal, []byte("retained journal"), 0600); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(1234, 0)
		if err := os.Chtimes(journal, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(root, "artifact")
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(testArtifact)))
		if err := os.WriteFile(target, []byte(testArtifact), 0600); err != nil {
			t.Fatal(err)
		}
		var events []InstallProgress
		ctx := WithInstallProgressInterval(t.Context(), 0, func(p InstallProgress) {
			events = append(events, p)
			info, err := os.Stat(journal)
			if err != nil || !info.ModTime().Equal(stamp) {
				t.Fatalf("download tick modified the journal: %v, %v", info, err)
			}
		})
		progressBatch(ctx, 2*int64(len(testArtifact)))
		if err := fetchArchive(ctx, "https://cached.example/file", digest, target); err != nil {
			t.Fatal(err)
		}
		cached := events[len(events)-1]
		if cached.BytesDone != int64(len(testArtifact)) || cached.NetworkBytesDownloaded != 0 {
			t.Fatalf("cache hit progress = %+v", cached)
		}
		old := artifactHTTPClientFactory
		t.Cleanup(func() { artifactHTTPClientFactory = old })
		artifactHTTPClientFactory = func(context.Context) *http.Client {
			return &http.Client{Transport: progressTestTransport(func(req *http.Request) (*http.Response, error) {
				if ua := req.UserAgent(); ua != "Memlane/0.1.0-beta.3" {
					t.Fatalf("unexpected User-Agent: %q", ua)
				}
				r := strings.NewReader(testArtifact)
				response := &http.Response{StatusCode: http.StatusOK, ContentLength: -1, Body: io.NopCloser(r)}
				if req.URL.Host == "first.example" {
					response.Body = io.NopCloser(progressTestReader(func(b []byte) (int, error) {
						if r.Len() == len(testArtifact) {
							return r.Read(b[:3])
						}
						return 0, io.ErrUnexpectedEOF
					}))
				}
				return response, nil
			})}
		}
		target = filepath.Join(root, "second-artifact")
		candidates := []ArtifactCandidate{
			{URL: "https://first.example/file", SHA256: digest},
			{URL: "https://second.example/file", SHA256: digest},
		}
		if _, err := fetchArtifactCandidates(ctx, candidates, func(ArtifactCandidate) string { return target }); err != nil {
			t.Fatal(err)
		}
		base := int64(len(testArtifact))
		var partial, reset bool
		for _, p := range events {
			if p.Mirror == "first.example" && p.FallbackAttempt == 1 && p.BytesDone == base+3 && p.BytesPerSec > 0 {
				partial = p.NetworkBytesDownloaded == 3
			}
			if p.Mirror == "second.example" {
				reset = p.BytesDone == base && p.BytesPerSec == 0 && p.FallbackAttempt == 2 &&
					p.FallbackTotal == 2 && p.Indeterminate && p.NetworkBytesDownloaded == 3
				break
			}
		}
		last := events[len(events)-1]
		if !partial || !reset || last.BytesDone != base*2 || last.BytesTotal != base*2 || last.NetworkBytesDownloaded != base+3 {
			t.Fatalf("incorrect mirror reset or batch totals: %+v", events)
		}
		progressComponent(ctx, "next component", true)
		progressAttempt(ctx, "downloading", "https://next.example/file", 1, 1)
		if err := fetchArchive(ctx, "https://next.example/file", digest, filepath.Join(root, "third-artifact")); err != nil {
			t.Fatal(err)
		}
		var previous int64
		for _, p := range events {
			if p.NetworkBytesDownloaded < previous {
				t.Fatalf("network bytes decreased from %d to %d", previous, p.NetworkBytesDownloaded)
			}
			previous = p.NetworkBytesDownloaded
		}
		if previous != 2*base+3 {
			t.Fatalf("network bytes after component change = %d, want %d", previous, 2*base+3)
		}
	})
}

func TestInstallProgressSyncElapsedStopsWithCommand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var events []InstallProgress
		ctx := WithInstallProgress(t.Context(), func(p InstallProgress) { events = append(events, p) })
		progressComponent(ctx, "runtime", true)
		stop := progressSync(ctx, "uv pip sync", "https://pypi.org/simple/", 2, 3)
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		stop()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if len(events) != 2 {
			t.Fatalf("expected initial and one elapsed update: %+v", events)
		}
		p := events[1]
		if p.Stage != "syncing" || p.Detail != "uv pip sync" || p.Mirror != "pypi.org" || p.FallbackAttempt != 2 ||
			p.FallbackTotal != 3 || !p.Indeterminate || p.Elapsed != time.Second || p.ElapsedMs != 1000 {
			t.Fatalf("unexpected sync update: %+v", p)
		}
	})
}

func runtimeSyncTestDriver(t *testing.T, syncScript string) RuntimeDriver {
	t.Helper()
	uv := fakePython(t, `
case "$4 $5" in
	"venv --no-project") exit 0 ;;
	"pip sync") `+syncScript+` ;;
	*) exit 42 ;;
esac`)
	return RuntimeDriver{
		Lock:   []byte("# empty test lock\n"),
		Python: func(context.Context) (string, error) { return "/usr/bin/python3", nil },
		UV:     func(context.Context) (string, error) { return uv, nil },
	}
}

func TestRuntimeInstallProgressRecordsPipSyncAcrossRetries(t *testing.T) {
	driver := runtimeSyncTestDriver(t, `
		if [ ! -f first-sync ]; then
			touch first-sync
			exit 19
		fi
		exit 0`)
	driver.DownloadSource = DownloadSourceChina
	req := Requirement{ID: "runtime", Digest: driver.Digest(), Destination: filepath.Join(workerTestRoot(t), "runtime")}
	var events []InstallProgress
	ctx := WithInstallProgress(t.Context(), func(p InstallProgress) { events = append(events, p) })
	if err := driver.Install(ctx, req); err != nil {
		t.Fatal(err)
	}
	progressComponent(ctx, "next-component", true)
	progressStage(ctx, "ready")
	var venv, firstSync, retry bool
	for _, p := range events {
		switch p.Detail {
		case "uv venv":
			if p.RuntimeSyncRan {
				t.Fatalf("venv creation marked as pip sync: %+v", p)
			}
			venv = true
		case "uv pip sync":
			if !p.RuntimeSyncRan || p.NetworkBytesDownloaded != 0 {
				t.Fatalf("pip sync progress = %+v", p)
			}
			firstSync = firstSync || p.FallbackAttempt == 1
			retry = retry || p.FallbackAttempt == 2
		}
	}
	last := events[len(events)-1]
	if !venv || !firstSync || !retry || last.Component != "next-component" || !last.RuntimeSyncRan {
		t.Fatalf("expected sync tracking to survive retry and component change: %+v", events)
	}
}
