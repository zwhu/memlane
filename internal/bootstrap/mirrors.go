package bootstrap

import (
	"github.com/zwhu/memlane/core/setup"
	"github.com/zwhu/memlane/kit/aiinstaller"
)

type ArtifactCandidate = aiinstaller.ArtifactCandidate

const (
	DownloadSourceChina    = aiinstaller.DownloadSourceChina
	DownloadSourceOfficial = aiinstaller.DownloadSourceOfficial
)

const (
	pinnedUV0123Arm64OfficialSHA256 = "546f7f8a6c70ff13a3a9d2bc958db3427298cebf3e0cb756f9177133b7068843"
	pinnedUV0123Arm64WheelSHA256    = "7c99f2524fe4b11d74dec85cef9b4d8b725d142dec04cd237f1c06fbdae1ee54"
	pinnedUV0123Arm64WheelPath      = "/packages/e4/66/ba257c91d69921d773f523c9e4c3ffb04e233694c415517f5ef797403a4f/uv-0.12.3-py3-none-macosx_11_0_arm64.whl"
	pinnedUV0123Arm64WheelEntry     = "uv-0.12.3.data/scripts/uv"

	pinnedOpenSSL359Arm64BlobSHA256 = "82b33ce156b289eaf95657b9ec16e8f49cb49c882cdeaeaf57e3e7d67fa85e25"
	pinnedOpenSSL359Arm64TunaFile   = "openssl%403-3.5.9.arm64_sequoia.bottle.tar.gz"
)

var (
	NormalizeDownloadSource = aiinstaller.NormalizeDownloadSource
	PyPIIndexURLs           = aiinstaller.PyPIIndexURLs
	ToolCandidates          = aiinstaller.ToolCandidates
	NativeCandidates        = aiinstaller.NativeCandidates
	SourceCandidates        = aiinstaller.SourceCandidates
	DisplaySourceURL        = setup.DisplaySourceURL
)

func ModelFileCandidates(repo string, file ModelFile, mode string) []ArtifactCandidate {
	return aiinstaller.ModelFileCandidates(repo, aiinstaller.ModelFileRef{Name: file.Name, SHA256: file.SHA256, URL: file.URL}, mode)
}
