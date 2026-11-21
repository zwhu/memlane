package simpledesktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestCompareSemverHandlesBetaPrereleasesAndMultiDigitParts(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.1.0-beta.2", "0.1.0-beta.1", 1},
		{"v0.1.0-beta.10", "v0.1.0-beta.2", 1},
		{"0.1.0", "0.1.0-beta.9", 1},
		{"0.1.0-beta.1", "0.1.0", -1},
		{"v0.1.0-beta.1", "0.1.0-beta.1", 0},
		{"v0.2.0-beta.1", "v0.1.9", 1},
	}
	for _, tc := range cases {
		if got := compareSemver(tc.a, tc.b); got != tc.want {
			t.Fatalf("compareSemver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestFetchLatestReleaseFiltersDraftsAndUnsafeURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]githubReleaseRecord{
			{
				TagName:    "v0.9.0",
				Name:       "Draft higher version",
				HTMLURL:    "https://github.com/zwhu/memlane/releases/tag/v0.9.0",
				Draft:      true,
				Prerelease: false,
			},
			{
				TagName:    "v0.5.0",
				Name:       "Spoofed external URL",
				HTMLURL:    "https://evil.example.com/memlane/releases/tag/v0.5.0",
				Draft:      false,
				Prerelease: false,
			},
			{
				TagName:    "v0.1.0-beta.2",
				Name:       "Beta 2",
				HTMLURL:    "https://github.com/zwhu/memlane/releases/tag/v0.1.0-beta.2",
				Draft:      false,
				Prerelease: true,
			},
			{
				TagName:    "v0.1.0-beta.1",
				Name:       "Beta 1",
				HTMLURL:    "https://github.com/zwhu/memlane/releases/tag/v0.1.0-beta.1",
				Draft:      false,
				Prerelease: true,
			},
		})
	}))
	defer srv.Close()

	latest, relURL, _, hasUpdate, err := fetchLatestRelease(context.Background(), srv.Client(), srv.URL, AppVersion)
	if err != nil {
		t.Fatalf("fetchLatestRelease failed: %v", err)
	}
	if !hasUpdate || latest != "v0.1.0-beta.2" || relURL != "https://github.com/zwhu/memlane/releases/tag/v0.1.0-beta.2" {
		t.Fatalf("unexpected release result: latest=%q url=%q hasUpdate=%v", latest, relURL, hasUpdate)
	}

	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream down", http.StatusInternalServerError)
	}))
	defer errSrv.Close()
	if _, _, _, _, err := fetchLatestRelease(context.Background(), errSrv.Client(), errSrv.URL, AppVersion); err == nil {
		t.Fatal("expected error for HTTP 500 response")
	}
}

func TestVersionUpdateSettingsUIAndOpenDownloadPage(t *testing.T) {
	a := New(Config{}, nil)
	var openedURL string
	a.OpenExternalURL = func(raw string) error {
		openedURL = raw
		return nil
	}
	a.updateCheck = updateCheckState{
		Checked:       true,
		HasUpdate:     true,
		LatestVersion: "v0.1.0-beta.2",
		ReleaseURL:    "https://github.com/zwhu/memlane/releases/tag/v0.1.0-beta.2",
		CheckedAt:     time.Now(),
	}

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Themed(c, homeTheme).Fill().Children(func() {
			ui.Scroll(c).Fill().Children(func() {
				a.versionUpdateSettings(c)
			})
		})
	}, 1200, 820)

	if !tt.HasText("版本与更新") || !tt.HasText("v0.1.0-beta.1") || !tt.HasText("发现新版本") || !tt.HasText("前往下载页面") {
		t.Fatalf("missing version update settings UI text: %v", tt.Texts())
	}

	clickProjectUI(t, tt, "前往下载页面")
	if openedURL != "https://github.com/zwhu/memlane/releases/tag/v0.1.0-beta.2" {
		t.Fatalf("expected validated GitHub release URL opened, got %q", openedURL)
	}

	// Spoofed ReleaseURL falls back safely to releasesIndexURL.
	openedURL = ""
	a.updateCheck.ReleaseURL = "https://phishing.example.invalid/download"
	a.OpenUpdateDownloadPage()
	if openedURL != releasesIndexURL {
		t.Fatalf("expected unsafe URL fallback to %q, got %q", releasesIndexURL, openedURL)
	}
}
