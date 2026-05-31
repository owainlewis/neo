package agent

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"

	"github.com/owainlewis/neo/internal/llm"
)

// maxImageBytes caps the size of an attached image. Anthropic rejects images
// larger than a few MB; we fail early with a clear message rather than sending
// a doomed request.
const maxImageBytes = 5 * 1024 * 1024

// supportedImageMedia lists the media types vision models accept. Anything
// else is rejected before we encode it.
var supportedImageMedia = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// imageBlock reads an image file and returns it as a base64 image content
// block. The media type is sniffed from the file's contents (not its
// extension), so a mislabeled file still gets the right type or is rejected.
func imageBlock(path string) (llm.ContentBlock, error) {
	info, err := os.Stat(path)
	if err != nil {
		return llm.ContentBlock{}, err
	}
	if info.IsDir() {
		return llm.ContentBlock{}, fmt.Errorf("is a directory")
	}
	if info.Size() > maxImageBytes {
		return llm.ContentBlock{}, fmt.Errorf("larger than %dMB", maxImageBytes/(1024*1024))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return llm.ContentBlock{}, err
	}
	media := http.DetectContentType(data)
	if !supportedImageMedia[media] {
		return llm.ContentBlock{}, fmt.Errorf("unsupported type %q", media)
	}
	return llm.ContentBlock{
		Type: "image",
		Source: &llm.ImageSource{
			Type:      "base64",
			MediaType: media,
			Data:      base64.StdEncoding.EncodeToString(data),
		},
	}, nil
}
