package bootstrap

import "testing"

func TestLockedVersionsRequiresPinnedUnambiguousVersions(t *testing.T) {
	v, err := LockedVersions([]byte("# lock\nlancedb==0.39.0 \\\n --hash=sha256:abc\npackage_name==1.2.3\n"))
	if err != nil || v["lancedb"] != "0.39.0" || v["package-name"] != "1.2.3" {
		t.Fatal(v, err)
	}
	for _, input := range []string{"# no packages", "package_name==1\npackage-name==2\n"} {
		if _, err := LockedVersions([]byte(input)); err == nil {
			t.Fatal("invalid lock accepted", input)
		}
	}
}
