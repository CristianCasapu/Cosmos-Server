package guides

import (
	"strings"
	"testing"
)

func TestEveryGuideHasAValidHeader(t *testing.T) {
	if len(Kinds()) == 0 || !Known("s3") {
		t.Fatalf("the guides: %v", Kinds())
	}
	for _, kind := range Kinds() {
		guide := known[kind]
		if guide.Title == "" || len(guide.Images) == 0 && guide.Provides == "" {
			t.Errorf("%s: a guide needs a title and something that matches it: %+v", kind, guide)
		}
	}
}

func TestMatching(t *testing.T) {
	cases := []struct {
		what, setting, image, kind, source string
	}{
		{"RustFS by its recipe", "", "rustfs/rustfs:1.0.5", "s3", FromRecipe},
		{"RustFS pinned by digest", "", "rustfs/rustfs@sha256:" + strings.Repeat("ab", 32), "s3", FromRecipe},
		{"RustFS with the registry written out", "", "docker.io/rustfs/rustfs:latest", "s3", FromRecipe},
		{"MinIO by its image", "", "minio/minio:RELEASE.2025-01-01T00-00-00Z", "s3", FromImage},
		{"MinIO from Quay", "", "quay.io/minio/minio", "s3", FromImage},
		{"Garage", "", "dxflrs/garage:v2.1.0", "s3", FromImage},
		{"SeaweedFS", "", "chrislusf/seaweedfs:latest", "s3", FromImage},
		{"a database is not S3", "", "postgres:18", "", ""},
		{"an image with a similar name is not that image", "", "someone/rustfs-like:1", "", ""},
		{"set by hand on any image", "s3", "example/store:1", "s3", FromSetting},
		{"set by hand without a container", "s3", "", "s3", FromSetting},
		{"switched off by hand", "none", "rustfs/rustfs", "", ""},
		{"an unknown setting is as proposed", "ftp", "minio/minio", "s3", FromImage},
		{"no image, nothing set", "", "", "", ""},
		{"an image ID alone is not matched", "", "b81d16a90ee2", "", ""},
	}
	for _, c := range cases {
		kind, source := For(c.setting, c.image)
		if kind != c.kind || source != c.source {
			t.Errorf("%s: For(%q, %q) = %q, %q; want %q, %q", c.what, c.setting, c.image, kind, source, c.kind, c.source)
		}
	}
}

func TestRegion(t *testing.T) {
	if got := Region("s3", "dxflrs/garage:v2"); got != "garage" {
		t.Errorf("Garage's region: %q", got)
	}
	if got := Region("s3", "rustfs/rustfs"); got != "us-east-1" {
		t.Errorf("RustFS' region: %q", got)
	}
	if got := Region("nothing", "rustfs/rustfs"); got != "" {
		t.Errorf("an unknown guide's region: %q", got)
	}
}

func TestSettings(t *testing.T) {
	for _, good := range []string{"", "none", "s3"} {
		if !ValidSetting(good) {
			t.Errorf("%q refused", good)
		}
	}
	for _, bad := range []string{"S3", "ftp", "s3 ", "<b>", "none;rm"} {
		if ValidSetting(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestHeaders(t *testing.T) {
	if _, ok := parseHeader("no header"); ok {
		t.Error("a file without a header was read")
	}
	if _, ok := parseHeader("---\nkind: s3\ntitle: x\n"); ok {
		t.Error("a header that is not closed was read")
	}
	if _, ok := parseHeader("---\nkind: S3!\ntitle: x\n---\n"); ok {
		t.Error("a kind that is not a plain name was read")
	}
	g, ok := parseHeader("---\nkind: db\ntitle: Connect\nimages: Postgres mariadb\nregions: a=b c\n---\nbody")
	if !ok || g.Images[0] != "postgres" || g.Regions["a"] != "b" || len(g.Regions) != 1 {
		t.Errorf("a good header: %+v %v", g, ok)
	}
}
