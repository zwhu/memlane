"""Provider registry and CLI contract for user profile exporters."""
from dataclasses import dataclass
from pathlib import Path

from project_policy import PolicyError

from ..constants import LIMIT, valid_body
from ..errors import ProfileAgentFailure, failure_detail, failure_reason


@dataclass(frozen=True)
class CommandSpec:
    """CLI invocation details produced by a provider's command builder."""

    args: list[str]
    env: dict[str, str]
    output_file: Path | None = None


class ProfileProvider:
    """Base contract for non-interactive or manual-only App profile sources."""

    kind: str = ''
    display_name: str = ''
    can_generate: bool = True

    def build_command(
        self,
        executable: str,
        row: dict,
        work: Path,
        prompt: str,
        env: dict[str, str],
    ) -> CommandSpec:
        """Build isolated read-only CLI invocation and environment for this App."""
        raise PolicyError('profile_automatic_unavailable', 409)

    def parse_output(self, stdout: bytes, output_file: Path | None) -> str:
        """Validate and extract the exported profile document from output file or stdout."""
        if output_file is not None:
            if not output_file.is_file() or output_file.stat().st_size > LIMIT:
                raise PolicyError('profile_output_unconfirmed', 503)
            return valid_body(output_file.read_text())
        return valid_body(stdout.decode('utf-8'))

    def parse_failure(self, returncode: int, stdout: bytes, stderr: bytes) -> ProfileAgentFailure:
        """Convert non-zero CLI exit status into a sanitized ProfileAgentFailure."""
        return ProfileAgentFailure(returncode, failure_reason(stderr), failure_detail(stderr))


PROVIDERS: dict[str, ProfileProvider] = {}


def register_provider(provider: ProfileProvider) -> ProfileProvider:
    """Register one host profile provider instance by its kind identifier."""
    PROVIDERS[provider.kind] = provider
    return provider


def get_provider(kind: str) -> ProfileProvider | None:
    """Return the registered provider for an Agent kind, if any."""
    return PROVIDERS.get(kind)


from .antigravity import AntigravityProvider  # noqa: E402
from .claude_code import ClaudeCodeProvider  # noqa: E402
from .codex import CodexProvider, TraeXProvider  # noqa: E402
from .manual_only import ManualOnlyProvider  # noqa: E402
from .pi import PiProvider  # noqa: E402

for _provider in (
    CodexProvider(),
    PiProvider(),
    TraeXProvider(),
    ClaudeCodeProvider(),
    AntigravityProvider(),
    ManualOnlyProvider('cursor', 'Cursor'),
    ManualOnlyProvider('trae_ide', 'TRAE IDE'),
    ManualOnlyProvider('opencode', 'OpenCode'),
):
    register_provider(_provider)

NAMES = {kind: provider.display_name for kind, provider in PROVIDERS.items()}
GENERATE_KINDS = {kind for kind, provider in PROVIDERS.items() if provider.can_generate}
