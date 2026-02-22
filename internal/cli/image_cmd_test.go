package cli

import (
	"path/filepath"
	"testing"
	"time"

	"firebox/internal/config"
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

func TestSetActiveImagePreservesRuntimePolicy(t *testing.T) {
	root := t.TempDir()
	paths := config.Paths{
		Runtime: filepath.Join(root, "runtime.json"),
	}

	initial := config.RuntimeConfig{
		InstanceName: "firebox-host",
		ImageName:    "default",
		Policy: config.RuntimePolicyConfig{
			NetworkAllow: []string{"github.com"},
		},
	}
	if err := config.SaveRuntimeConfig(paths.Runtime, initial); err != nil {
		t.Fatalf("SaveRuntimeConfig() error = %v", err)
	}

	rec := imageRecord{Name: "dev", InstanceName: "firebox-img-dev"}
	if err := setActiveImage(paths, rec); err != nil {
		t.Fatalf("setActiveImage() error = %v", err)
	}

	got, err := config.LoadRuntimeConfig(paths.Runtime)
	if err != nil {
		t.Fatalf("LoadRuntimeConfig() error = %v", err)
	}
	if got.InstanceName != rec.InstanceName {
		t.Fatalf("InstanceName = %q, want %q", got.InstanceName, rec.InstanceName)
	}
	if got.ImageName != rec.Name {
		t.Fatalf("ImageName = %q, want %q", got.ImageName, rec.Name)
	}
	if len(got.Policy.NetworkAllow) != 1 || got.Policy.NetworkAllow[0] != "github.com" {
		t.Fatalf("Policy.NetworkAllow = %#v, want [github.com]", got.Policy.NetworkAllow)
	}
}
