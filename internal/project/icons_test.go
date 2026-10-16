package project

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestProjectEmojiValidation(t *testing.T) {
	for _, emoji := range []string{"", "🇫🇷", "👩🏽‍💻", "1️⃣"} {
		if err := validateIdentity(emoji, ""); err != nil {
			t.Errorf("valid emoji %q: %v", emoji, err)
		}
	}
	for _, emoji := range []string{"admin", "<b>", "x y", "x\ny", "\t", "\x00", "\u200b", string([]byte{0xff}), strings.Repeat("🚀", 2)} {
		if err := validateIdentity(emoji, ""); err == nil {
			t.Errorf("accepted invalid emoji %q", emoji)
		}
	}
}

func TestIconImageNormalizationAndBoundaries(t *testing.T) {
	var jpegBytes bytes.Buffer
	if err := jpeg.Encode(&jpegBytes, image.NewRGBA(image.Rect(0, 0, 128, 64)), nil); err != nil {
		t.Fatal(err)
	}
	input := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpegBytes.Bytes())
	normalized, err := NormalizeIconImage(input)
	if err != nil || !strings.HasPrefix(normalized, "data:image/png;base64,") {
		t.Fatalf("normalize = %q, %v", normalized, err)
	}
	raw, err := IconBytes(normalized)
	if err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width != 128 || config.Height != 64 {
		t.Fatalf("config = %+v, %v", config, err)
	}
	if again, err := NormalizeIconImage(normalized); err != nil || again != normalized {
		t.Fatalf("not canonical: %v", err)
	}
	var oversized bytes.Buffer
	if err := png.Encode(&oversized, image.NewRGBA(image.Rect(0, 0, 129, 1))); err != nil {
		t.Fatal(err)
	}
	invalid := []string{
		"https://example.com/icon.png", "data:image/svg+xml;base64,PHN2Zy8+", "data:image/png;base64,%%%",
		"data:image/png;base64," + base64.StdEncoding.EncodeToString(jpegBytes.Bytes()),
		"data:image/png;base64," + base64.StdEncoding.EncodeToString(oversized.Bytes()),
		"data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, MaxIconBytes+1)),
		strings.Repeat("x", MaxIconDataURLBytes+1),
	}
	for _, value := range invalid {
		if _, err := NormalizeIconImage(value); err == nil {
			t.Errorf("accepted invalid image, length %d", len(value))
		}
	}
}

func TestServiceNormalizesIdentityAndPreservesOmittedUpdates(t *testing.T) {
	repo := &fakeProjects{}
	service := NewService(repo, nil)
	var source bytes.Buffer
	if err := jpeg.Encode(&source, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil); err != nil {
		t.Fatal(err)
	}
	icon := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(source.Bytes())
	if _, err := service.CreateProject(t.Context(), CreateProject{Slug: "icons", Name: "Icons", Platform: PlatformGo, IconEmoji: "👩🏽‍💻", IconImage: icon}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(repo.created[0].IconImage, "data:image/png;base64,") || repo.created[0].IconEmoji != "👩🏽‍💻" {
		t.Fatalf("not normalized: %+v", repo.created[0])
	}
	if _, err := service.UpdateProject(t.Context(), "icons", UpdateProject{Name: "Icons", Platform: PlatformGo}); err != nil {
		t.Fatal(err)
	}
	if repo.updated[0].IconEmoji != nil || repo.updated[0].IconImage != nil {
		t.Fatal("omission was not preserved")
	}
	empty := ""
	if _, err := service.UpdateProject(t.Context(), "icons", UpdateProject{Name: "Icons", Platform: PlatformGo, IconEmoji: &empty, IconImage: &empty}); err != nil {
		t.Fatal(err)
	}
	if repo.updated[1].IconEmoji == nil || repo.updated[1].IconImage == nil || *repo.updated[1].IconImage != "" {
		t.Fatal("clear was not preserved")
	}
	icon = "invalid"
	if _, err := service.UpdateProject(t.Context(), "icons", UpdateProject{Name: "Icons", Platform: PlatformGo, IconImage: &icon}); err == nil || len(repo.updated) != 2 {
		t.Fatal("invalid image reached persistence")
	}
}
