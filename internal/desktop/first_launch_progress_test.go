package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDownloadByteEventsKeepUIFreshWithoutRewritingDurableSetup(t *testing.T) {
	a := New("test", nil)
	root := t.TempDir()
	a.firstLaunch.Controller = &RuntimeController{Config: RuntimeConfig{Root: root}}
	a.firstLaunch.Open = true
	a.firstLaunch.State.Stage = "preparing"
	a.SetRuntimeStatus(RuntimeStatus{Phase: "downloading", Message: "正在准备 cognee-service", Downloaded: 1, Total: 1000})
	file := filepath.Join(root, "first-launch.json")
	sentinel := time.Unix(100, 0)
	if err := os.Chtimes(file, sentinel, sentinel); err != nil {
		t.Fatal(err)
	}
	for n := int64(2); n <= 1000; n++ {
		a.SetRuntimeStatus(RuntimeStatus{Phase: "downloading", Message: "正在准备 cognee-service", Downloaded: n, Total: 1000})
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(sentinel) {
		t.Fatal("byte progress rewrote the durable setup file on the UI thread")
	}
	if a.runtimeStatus.Downloaded != 1000 {
		t.Fatal("live UI lost the latest byte count")
	}
	a.SetRuntimeStatus(RuntimeStatus{Phase: "cancelled", Message: "本轮准备已取消"})
	var saved FirstLaunchState
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Stage != "cancelled" {
		t.Fatal("terminal cancellation was not durable", saved.Stage)
	}
}
