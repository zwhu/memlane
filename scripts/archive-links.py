"""Restore only explicitly manifested relative links within an isolated runtime."""
from pathlib import Path


def restore_links(directory, manifest):
    root = Path(directory).resolve()
    seen = set()
    for link in manifest.get("links", []):
        rel, target = Path(link["path"]), Path(link["target"])
        assert not rel.is_absolute() and ".." not in rel.parts and str(rel) not in seen
        seen.add(str(rel))
        assert not target.is_absolute() and "\\" not in link["target"]
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        lexical = Path(__import__("os").path.abspath(path.parent / target))
        assert lexical.is_relative_to(root)
        assert not path.exists() and not path.is_symlink()
        path.symlink_to(link["target"])
    for link in manifest.get("links", []):
        resolved = (root / link["path"]).resolve(strict=True)
        assert resolved.is_relative_to(root)
