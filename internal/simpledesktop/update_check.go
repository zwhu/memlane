package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
)

const (
	AppVersion            = "0.1.0-beta.2"
	defaultReleasesAPIURL = "https://api.github.com/repos/zwhu/memlane/releases?per_page=10"
	releasesPagePrefix    = "https://github.com/zwhu/memlane/releases/"
	releasesIndexURL      = "https://github.com/zwhu/memlane/releases"
)

type updateCheckState struct {
	Checking      bool
	Checked       bool
	HasUpdate     bool
	LatestVersion string
	ReleaseURL    string
	ReleaseName   string
	Error         string
	CheckedAt     time.Time
	apiURL        string
}

type githubReleaseRecord struct {
	TagName    string `json:"tag_name"`
	Name       string `json:"name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

type semverParts struct {
	major, minor, patch int
	pre                 []string
}

func parseSemver(raw string) (semverParts, bool) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if plus := strings.IndexByte(s, '+'); plus >= 0 {
		s = s[:plus]
	}
	if s == "" {
		return semverParts{}, false
	}
	var prePart string
	core := s
	if dash := strings.IndexByte(s, '-'); dash >= 0 {
		core = s[:dash]
		prePart = s[dash+1:]
		if prePart == "" {
			return semverParts{}, false
		}
	}
	nums := strings.Split(core, ".")
	if len(nums) != 3 {
		return semverParts{}, false
	}
	major, err1 := strconv.Atoi(nums[0])
	minor, err2 := strconv.Atoi(nums[1])
	patch, err3 := strconv.Atoi(nums[2])
	if err1 != nil || err2 != nil || err3 != nil || major < 0 || minor < 0 || patch < 0 {
		return semverParts{}, false
	}
	var pre []string
	if prePart != "" {
		pre = strings.Split(prePart, ".")
		for _, id := range pre {
			if id == "" {
				return semverParts{}, false
			}
		}
	}
	return semverParts{major: major, minor: minor, patch: patch, pre: pre}, true
}

func isDigitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// compareSemver compares two semantic versions (positive if a > b, negative if a < b, 0 if equal).
func compareSemver(a, b string) int {
	pa, okA := parseSemver(a)
	pb, okB := parseSemver(b)
	if !okA && !okB {
		return 0
	}
	if !okA {
		return -1
	}
	if !okB {
		return 1
	}
	if pa.major != pb.major {
		if pa.major > pb.major {
			return 1
		}
		return -1
	}
	if pa.minor != pb.minor {
		if pa.minor > pb.minor {
			return 1
		}
		return -1
	}
	if pa.patch != pb.patch {
		if pa.patch > pb.patch {
			return 1
		}
		return -1
	}
	if len(pa.pre) == 0 && len(pb.pre) == 0 {
		return 0
	}
	if len(pa.pre) == 0 {
		return 1
	}
	if len(pb.pre) == 0 {
		return -1
	}
	limit := len(pa.pre)
	if len(pb.pre) < limit {
		limit = len(pb.pre)
	}
	for i := 0; i < limit; i++ {
		ia, ib := pa.pre[i], pb.pre[i]
		if ia == ib {
			continue
		}
		numA, numB := isDigitsOnly(ia), isDigitsOnly(ib)
		switch {
		case numA && numB:
			va, _ := strconv.Atoi(ia)
			vb, _ := strconv.Atoi(ib)
			if va > vb {
				return 1
			}
			if va < vb {
				return -1
			}
		case numA != numB:
			// Per SemVer 2.0.0, numeric prerelease identifiers sort lower than non-numeric.
			if numA {
				return -1
			}
			return 1
		default:
			if ia > ib {
				return 1
			}
			return -1
		}
	}
	if len(pa.pre) > len(pb.pre) {
		return 1
	}
	if len(pa.pre) < len(pb.pre) {
		return -1
	}
	return 0
}

func isSafeReleaseURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if u.Scheme != "https" || u.Host != "github.com" || u.User != nil {
		return false
	}
	return strings.HasPrefix(u.String(), releasesPagePrefix)
}

func fetchLatestRelease(ctx context.Context, client *http.Client, apiURL, currentVersion string) (latestVersion, releaseURL, releaseName string, hasUpdate bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", "", "", false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Memlane/"+AppVersion)
	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", "", false, fmt.Errorf("检查更新失败（HTTP %d）", resp.StatusCode)
	}
	var records []githubReleaseRecord
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&records); err != nil {
		return "", "", "", false, err
	}
	var best *githubReleaseRecord
	for i := range records {
		rec := &records[i]
		if rec.Draft {
			continue
		}
		if _, ok := parseSemver(rec.TagName); !ok {
			continue
		}
		if !isSafeReleaseURL(rec.HTMLURL) {
			continue
		}
		if best == nil || compareSemver(rec.TagName, best.TagName) > 0 {
			best = rec
		}
	}
	if best == nil {
		return currentVersion, releasesIndexURL, "", false, nil
	}
	return best.TagName, best.HTMLURL, best.Name, compareSemver(best.TagName, currentVersion) > 0, nil
}

func formatDisplayVersion(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "v" + AppVersion
	}
	if strings.HasPrefix(s, "v") || strings.HasPrefix(s, "V") {
		return "v" + s[1:]
	}
	return "v" + s
}

// CheckForUpdates manually queries GitHub Releases (including beta prereleases) without modifying installed binaries.
func (a *App) CheckForUpdates() {
	if a.updateCheck.Checking || a.Update == nil {
		return
	}
	apiURL := a.updateCheck.apiURL
	if apiURL == "" {
		apiURL = defaultReleasesAPIURL
	}
	a.updateCheck.Checking = true
	a.updateCheck.Error = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = bootstrap.ProxyFunc(ctx)
		client := &http.Client{Timeout: 10 * time.Second, Transport: transport}
		latest, relURL, relName, hasUpdate, err := fetchLatestRelease(ctx, client, apiURL, AppVersion)
		a.Update(func() {
			a.updateCheck.Checking = false
			a.updateCheck.Checked = true
			a.updateCheck.CheckedAt = time.Now()
			if err != nil {
				a.updateCheck.Error = "检查更新失败，请稍后重试或前往发布页查看"
				a.updateCheck.HasUpdate = false
				return
			}
			a.updateCheck.Error = ""
			a.updateCheck.HasUpdate = hasUpdate
			a.updateCheck.LatestVersion = latest
			a.updateCheck.ReleaseURL = relURL
			a.updateCheck.ReleaseName = relName
		})
	}()
}

// OpenUpdateDownloadPage opens the validated GitHub Releases page in the system browser.
func (a *App) OpenUpdateDownloadPage() {
	target := a.updateCheck.ReleaseURL
	if !isSafeReleaseURL(target) {
		target = releasesIndexURL
	}
	if a.OpenExternalURL != nil {
		if err := a.OpenExternalURL(target); err != nil {
			a.updateCheck.Error = "无法打开系统浏览器，请手动访问 GitHub Releases 页面"
		}
	}
}

func (a *App) versionUpdateSettings(c *ui.Context) {
	u := &a.updateCheck
	v4Group(c, "版本与更新", "仅在点击「检查更新」时向 GitHub Releases 查询新版信息（含公测预发布版本）", func() {
		if u.Checking {
			v4Spinner(c)
		} else if u.Checked && u.Error == "" {
			if u.HasUpdate {
				v4Badge(c, "发现新版本", "accent")
			} else {
				v4Badge(c, "已是最新", "ok")
			}
		}
		v4SmallButton(c, "检查更新", v4IconRefresh, a.CheckForUpdates).Disabled(u.Checking)
		if u.HasUpdate {
			v4SmallPrimaryButton(c, "前往下载页面", a.OpenUpdateDownloadPage)
		} else {
			v4SmallButton(c, "查看发布页", nil, a.OpenUpdateDownloadPage)
		}
	}, func() {
		desc := "点击「检查更新」可比对 GitHub Releases 上的最新公测或正式版本"
		valCol := tok.Text2
		if u.Checking {
			desc = "正在向 GitHub Releases 查询最新版本…"
		} else if u.HasUpdate {
			desc = "发现可用新版本 " + formatDisplayVersion(u.LatestVersion) + "，可点击「前往下载页面」下载安装包"
			valCol = tok.Accent
		} else if u.Checked && u.Error == "" {
			desc = "当前已是最新版本（" + u.CheckedAt.Format("15:04:05") + " 已检查）"
			valCol = tok.OK
		}
		v4Row(c, v4RowOptions{
			Key:        "settings-version-current",
			Title:      "当前版本",
			Desc:       desc,
			Value:      formatDisplayVersion(AppVersion),
			ValueColor: valCol,
			Error:      u.Error,
		})
	})
}
