package bootstrap

import (
	"strings"
	"testing"
)

func TestMirrorMappingsPreserveDigestsAndDistinguishOfficialSources(t *testing.T) {
	if got := NormalizeDownloadSource(""); got != DownloadSourceChina {
		t.Fatalf("expected empty source to default to china, got %q", got)
	}
	if got := NormalizeDownloadSource("official"); got != DownloadSourceOfficial {
		t.Fatalf("expected official source, got %q", got)
	}

	uvOfficial := "https://github.com/astral-sh/uv/releases/download/0.12.3/uv-aarch64-apple-darwin.tar.gz"
	uvChina := ToolCandidates("uv", uvOfficial, pinnedUV0123Arm64OfficialSHA256, DownloadSourceChina)
	if len(uvChina) != 5 || uvChina[0].Format != "wheel" || !strings.Contains(uvChina[0].URL, "pypi.tuna.tsinghua.edu.cn") {
		t.Fatalf("unexpected uv china candidates: %+v", uvChina)
	}
	if uvChina[0].SHA256 != pinnedUV0123Arm64WheelSHA256 {
		t.Fatalf("unexpected uv wheel digest: %q", uvChina[0].SHA256)
	}

	pyOfficial := "https://github.com/astral-sh/python-build-standalone/releases/download/20260211/cpython-3.12.12%2B20260211-aarch64-apple-darwin-install_only_stripped.tar.gz"
	const pySHA = "22625deaf5757e7c266cf1a096c9151a06b598b1e14632a2ec9993d58ec5fe84"
	pyChina := ToolCandidates("python", pyOfficial, pySHA, DownloadSourceChina)
	if !strings.HasPrefix(pyChina[0].URL, "https://registry.npmmirror.com/-/binary/python-build-standalone/20260211/cpython-3.12.12%2B20260211-") {
		t.Fatalf("unexpected python china mirror URL: %q", pyChina[0].URL)
	}
	if pyChina[0].SHA256 != pySHA {
		t.Fatalf("python mirror must preserve SHA256")
	}

	sslOfficial := "https://ghcr.io/v2/homebrew/core/openssl/3/blobs/sha256:" + pinnedOpenSSL359Arm64BlobSHA256
	sslChina := NativeCandidates(sslOfficial, pinnedOpenSSL359Arm64BlobSHA256, DownloadSourceChina)
	if len(sslChina) != 3 || sslChina[0].URL != "https://mirrors.ustc.edu.cn/homebrew-bottles"+strings.TrimPrefix(sslOfficial, "https://ghcr.io") || sslChina[1].URL != "https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles/openssl%403-3.5.9.arm64_sequoia.bottle.tar.gz" {
		t.Fatalf("unexpected openssl china candidates: %+v", sslChina)
	}

	srcOfficial := "https://codeload.github.com/topoteretes/cognee/tar.gz/ec6f40859e5f2df9e7862dde27d580ac3268ed43"
	const srcSHA = "6e11047ef690efedac06653c67a11bea0c0e0a8c6af0c89dad634574f4bc01bc"
	srcChina := SourceCandidates(srcOfficial, srcSHA, DownloadSourceChina)
	if len(srcChina) != 3 || srcChina[0].URL != "https://gh-proxy.org/"+srcOfficial || srcChina[1].URL != "https://gh-proxy.com/"+srcOfficial {
		t.Fatalf("unexpected source china candidates: %+v", srcChina)
	}

	mf := ModelFile{
		Name:   "model_optimized.onnx",
		SHA256: "1294ea4b6331115a353d81f96b85e8c8d7fdcc284453d5b2fab5b016230aad38",
		URL:    "https://huggingface.co/Qdrant/bge-small-zh-v1.5/resolve/46fbe35fd4374a00fee7de77dfddaeb6dd6a2c59/model_optimized.onnx",
	}
	mfChina := ModelFileCandidates("Qdrant/bge-small-zh-v1.5", mf, DownloadSourceChina)
	if len(mfChina) != 2 || mfChina[0].URL != "https://modelscope.cn/models/Qdrant/bge-small-zh-v1.5/resolve/master/model_optimized.onnx" || mfChina[0].SHA256 != mf.SHA256 {
		t.Fatalf("unexpected model china candidate: %+v", mfChina)
	}
	for _, tc := range []struct {
		mirrors             []ArtifactCandidate
		url, digest, format string
		wheels              int
	}{
		{uvChina, uvOfficial, pinnedUV0123Arm64OfficialSHA256, "tar.gz", 2},
		{pyChina, pyOfficial, pySHA, "tar.gz", 0},
		{sslChina, sslOfficial, pinnedOpenSSL359Arm64BlobSHA256, "tar.gz", 0},
		{srcChina, srcOfficial, srcSHA, "tar.gz", 0},
		{mfChina, mf.URL, mf.SHA256, "raw", 0},
	} {
		if last := tc.mirrors[len(tc.mirrors)-1]; last != (ArtifactCandidate{URL: tc.url, SHA256: tc.digest, Format: tc.format}) {
			t.Fatalf("unexpected official fallback: %+v", last)
		}
		seen := map[string]bool{}
		for i, candidate := range tc.mirrors {
			digest := tc.digest
			if i < tc.wheels {
				digest = pinnedUV0123Arm64WheelSHA256
			}
			if seen[candidate.URL] || candidate.SHA256 != digest {
				t.Fatalf("duplicate candidate or changed digest: %+v", candidate)
			}
			seen[candidate.URL] = true
		}
	}

	mfOff := ModelFileCandidates("Qdrant/bge-small-zh-v1.5", mf, DownloadSourceOfficial)
	if mfOff[0].URL != mf.URL {
		t.Fatalf("expected official URL untouched, got %q", mfOff[0].URL)
	}
}
