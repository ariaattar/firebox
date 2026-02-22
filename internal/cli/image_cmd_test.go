package cli

import (
	"path/filepath"
	"testing"
	"time"
)

func TestImageInstanceName(t *testing.T) {
	got := imageInstanceName("Dev.Image_1")
	want := "firebox-img-dev-image_1"
	if got != want {
		t.Fatalf("imageInstanceName() = %q, want %q", got, want)
	}
}

func TestImageCatalogRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "images.json")
	in := imageCatalog{
		Images: map[string]imageRecord{
			"dev": {
				Name:         "dev",
				InstanceName: "firebox-img-dev",
				YAMLFile:     "/tmp/dev.yaml",
				BuiltAt:      time.Unix(1700000000, 0).UTC(),
			},
		},
	}

	if err := saveImageCatalog(path, in); err != nil {
		t.Fatalf("saveImageCatalog() error = %v", err)
	}
	out, err := loadImageCatalog(path)
	if err != nil {
		t.Fatalf("loadImageCatalog() error = %v", err)
	}

	rec, ok := out.Images["dev"]
	if !ok {
		t.Fatalf("catalog missing image %q", "dev")
	}
	if rec.InstanceName != "firebox-img-dev" {
		t.Fatalf("InstanceName = %q, want %q", rec.InstanceName, "firebox-img-dev")
	}
	if rec.YAMLFile != "/tmp/dev.yaml" {
		t.Fatalf("YAMLFile = %q, want %q", rec.YAMLFile, "/tmp/dev.yaml")
	}
}
