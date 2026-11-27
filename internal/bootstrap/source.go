package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/zwhu/memlane/core/setup"
	"github.com/zwhu/memlane/kit/aiinstaller"
)

type SourceSpec = setup.SourceSpec

type SourceDriver struct {
	Spec           SourceSpec
	DownloadSource string
}

func (d SourceDriver) matches(r Requirement) bool {
	if r.ID != "cognee-source" || r.Version != d.Spec.Commit || r.Digest != d.Spec.SHA256 {
		return false
	}
	if d.DownloadSource == "" {
		return r.Source == d.Spec.URL
	}
	return r.Source == d.Spec.URL || r.Source == DisplaySourceURL(r.ID, d.Spec.URL, d.Spec.SHA256, d.DownloadSource)
}
func (d SourceDriver) Probe(ctx context.Context, r Requirement) (Observation, error) {
	o := Observation{Status: Missing, Path: r.Destination, Version: d.Spec.Commit, Fingerprint: d.Spec.SHA256}
	if err := ctx.Err(); err != nil {
		return o, err
	}
	if !d.matches(r) {
		return o, errors.New("source requirement differs from pinned manifest")
	}
	if _, err := os.Lstat(r.Destination); errors.Is(err, os.ErrNotExist) {
		return o, nil
	} else if err != nil {
		return o, err
	}
	err := VerifySource(filepath.Join(r.Destination, "service.tar.gz"), filepath.Join(r.Destination, "source"), d.Spec.SHA256, d.Spec.Packages)
	if err != nil {
		o.Status, o.Reason = Incompatible, err.Error()
	} else {
		o.Status = Available
	}
	return o, nil
}

func (d SourceDriver) Install(ctx context.Context, r Requirement) error {
	progressComponent(ctx, r.ID, false)
	if !d.matches(r) {
		return errors.New("source requirement differs from pinned manifest")
	}
	// State ownership must be established before network use. Never replace an
	// existing user source tree, even if a user approved installing another copy.
	expected, _ := json.Marshal(d.Spec)
	if err := aiinstaller.ClaimComponentRoot(r.Destination, ".memlane-source.json", expected); err != nil {
		return err
	}
	archive := filepath.Join(r.Destination, "service.tar.gz")
	if d.DownloadSource != "" {
		candidates := SourceCandidates(d.Spec.URL, r.Digest, d.DownloadSource)
		if _, err := fetchArtifactCandidates(ctx, candidates, func(ArtifactCandidate) string { return archive }); err != nil {
			return err
		}
	} else if err := fetchArchive(ctx, r.Source, r.Digest, archive); err != nil {
		return err
	}
	source := filepath.Join(r.Destination, "source")
	if _, err := os.Lstat(source); err == nil {
		progressStage(ctx, "verifying")
		return VerifySource(archive, source, r.Digest, d.Spec.Packages)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	staging, err := os.MkdirTemp(r.Destination, ".source-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	progressStage(ctx, "extracting")
	if err := extractPackages(ctx, archive, staging, d.Spec.Packages); err != nil {
		return err
	}
	progressStage(ctx, "verifying")
	if err := VerifySource(archive, staging, r.Digest, d.Spec.Packages); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(staging, source)
}

var (
	artifactUserAgent             = setup.MemlaneBrand().UserAgent
	artifactResponseHeaderTimeout = 30 * time.Second
	artifactReadStallTimeout      = 30 * time.Second
	artifactTotalTimeout          = 30 * time.Minute
	artifactHTTPClientFactory     = newArtifactHTTPClient
)

func artifactDownloader() aiinstaller.Downloader {
	downloader := setup.NewDownloader()
	downloader.Options.UserAgent = artifactUserAgent
	downloader.Options.ResponseHeaderTimeout = artifactResponseHeaderTimeout
	downloader.Options.ReadStallTimeout = artifactReadStallTimeout
	downloader.Options.TotalTimeout = artifactTotalTimeout
	return downloader
}
func newArtifactHTTPClient(ctx context.Context) *http.Client {
	return artifactDownloader().NewHTTPClient(ctx)
}
func fetchArtifactCandidates(ctx context.Context, candidates []ArtifactCandidate, destinationFor func(ArtifactCandidate) string) (ArtifactCandidate, error) {
	d := artifactDownloader()
	d.Options.HTTPClientFactory = artifactHTTPClientFactory
	return d.FetchCandidates(ctx, candidates, destinationFor)
}
func fetchArchive(ctx context.Context, rawURL, digest, destination string) error {
	d := artifactDownloader()
	d.Options.HTTPClientFactory = artifactHTTPClientFactory
	return d.FetchArchive(ctx, rawURL, digest, destination)
}
func fetchArchiveAttempt(ctx context.Context, rawURL, digest, destination string) error {
	d := artifactDownloader()
	d.Options.HTTPClientFactory = artifactHTTPClientFactory
	return d.FetchArchiveAttempt(ctx, rawURL, digest, destination)
}

var (
	summarizeArtifactError = aiinstaller.SummarizeArtifactError
	extractPackages        = aiinstaller.ExtractTarGzPackages
)
