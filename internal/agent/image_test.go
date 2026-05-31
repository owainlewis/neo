package agent

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// a tiny valid PNG header so http.DetectContentType returns image/png.
var pngBytes = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

func TestImageBlock(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "a.png")
	if err := os.WriteFile(png, pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	blk, err := imageBlock(png)
	if err != nil {
		t.Fatalf("imageBlock: %v", err)
	}
	if blk.Type != "image" {
		t.Errorf("Type = %q, want image", blk.Type)
	}
	if blk.Source == nil || blk.Source.MediaType != "image/png" {
		t.Fatalf("Source = %+v, want media image/png", blk.Source)
	}
	if got := blk.Source.Data; got != base64.StdEncoding.EncodeToString(pngBytes) {
		t.Errorf("data not round-tripped: %q", got)
	}
}

func TestImageBlockRejectsNonImage(t *testing.T) {
	dir := t.TempDir()
	txt := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(txt, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := imageBlock(txt); err == nil {
		t.Fatal("expected error for non-image file")
	}
}

func TestImageBlockMissingFile(t *testing.T) {
	if _, err := imageBlock(filepath.Join(t.TempDir(), "nope.png")); err == nil {
		t.Fatal("expected error for missing file")
	}
}
