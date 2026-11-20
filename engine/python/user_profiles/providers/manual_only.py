"""Display-only provider adapters for hosts restricted to manual profile import."""
from . import ProfileProvider


class ManualOnlyProvider(ProfileProvider):
    """Host source without non-interactive automatic profile export support."""

    can_generate = False

    def __init__(self, kind: str, display_name: str):
        self.kind = kind
        self.display_name = display_name
