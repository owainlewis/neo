package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractImagePaths(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(png, []byte("\x89PNG\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withSpace := filepath.Join(dir, "my screenshot.png")
	if err := os.WriteFile(withSpace, []byte("\x89PNG\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("bare path is extracted", func(t *testing.T) {
		text, paths := extractImagePaths("what is this " + png)
		if text != "what is this " {
			t.Errorf("text = %q, want %q", text, "what is this ")
		}
		if len(paths) != 1 || paths[0] != png {
			t.Errorf("paths = %v, want [%s]", paths, png)
		}
	})

	t.Run("file:// url is extracted", func(t *testing.T) {
		_, paths := extractImagePaths("file://" + png)
		if len(paths) != 1 || paths[0] != png {
			t.Errorf("paths = %v, want [%s]", paths, png)
		}
	})

	t.Run("backslash-escaped spaces survive", func(t *testing.T) {
		escaped := filepath.Join(dir, `my\ screenshot.png`)
		_, paths := extractImagePaths("look " + escaped)
		if len(paths) != 1 || paths[0] != withSpace {
			t.Errorf("paths = %v, want [%s]", paths, withSpace)
		}
	})

	t.Run("non-image text is left alone", func(t *testing.T) {
		text, paths := extractImagePaths("just a normal message")
		if text != "just a normal message" {
			t.Errorf("text = %q", text)
		}
		if len(paths) != 0 {
			t.Errorf("paths = %v, want none", paths)
		}
	})

	t.Run("multiline text without images is unchanged", func(t *testing.T) {
		input := "Review this table:\n\n| name | value |\n| --- | --- |\n| alpha | 1 |\n\n```go\n\treturn nil\n```"
		text, paths := extractImagePaths(input)
		if text != input {
			t.Errorf("text changed:\n got: %q\nwant: %q", text, input)
		}
		if len(paths) != 0 {
			t.Errorf("paths = %v, want none", paths)
		}
	})

	t.Run("extracting image preserves surrounding multiline layout", func(t *testing.T) {
		input := "Compare these sections:\n\nfirst\n  " + png + "\n\n\tsecond"
		want := "Compare these sections:\n\nfirst\n  \n\n\tsecond"
		text, paths := extractImagePaths(input)
		if text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
		if len(paths) != 1 || paths[0] != png {
			t.Errorf("paths = %v, want [%s]", paths, png)
		}
	})

	t.Run("image first preserves following newline and indentation", func(t *testing.T) {
		input := png + "\n\treturn nil"
		want := "\n\treturn nil"
		text, paths := extractImagePaths(input)
		if text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
		if len(paths) != 1 || paths[0] != png {
			t.Errorf("paths = %v, want [%s]", paths, png)
		}
	})

	t.Run("image last preserves preceding newline", func(t *testing.T) {
		input := "compare this\n" + png
		want := "compare this\n"
		text, paths := extractImagePaths(input)
		if text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
		if len(paths) != 1 || paths[0] != png {
			t.Errorf("paths = %v, want [%s]", paths, png)
		}
	})

	t.Run("missing file is not extracted", func(t *testing.T) {
		ghost := filepath.Join(dir, "nope.png")
		text, paths := extractImagePaths("see " + ghost)
		if len(paths) != 0 {
			t.Errorf("paths = %v, want none", paths)
		}
		if text == "" {
			t.Errorf("text should retain the unmatched token")
		}
	})

	t.Run("non-image file with extension is not extracted", func(t *testing.T) {
		txt := filepath.Join(dir, "notes.txt")
		if err := os.WriteFile(txt, []byte("hi"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, paths := extractImagePaths(txt)
		if len(paths) != 0 {
			t.Errorf("paths = %v, want none", paths)
		}
	})
}
