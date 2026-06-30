package routing

import (
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels/media"
)

func TestMediaKindFromFlags(t *testing.T) {
	cases := []struct {
		hv, hm bool
		want   string
	}{
		{false, false, MediaKindText},
		{false, true, MediaKindMedia},
		{true, false, MediaKindVoice},
		{true, true, MediaKindVoice},
	}
	for _, c := range cases {
		if got := MediaKindFromFlags(c.hv, c.hm); got != c.want {
			t.Errorf("MediaKindFromFlags(%v,%v) = %q, want %q", c.hv, c.hm, got, c.want)
		}
	}
}

func TestDeriveMediaKindFromMediaInfoTypes(t *testing.T) {
	cases := []struct {
		name  string
		types []string
		want  string
	}{
		{"empty", nil, MediaKindText},
		{"single image", []string{media.TypeImage}, MediaKindMedia},
		{"voice only", []string{media.TypeVoice}, MediaKindVoice},
		{"audio only", []string{media.TypeAudio}, MediaKindVoice},
		{"image plus voice → voice wins", []string{media.TypeImage, media.TypeVoice}, MediaKindVoice},
		{"document plus animation", []string{media.TypeDocument, media.TypeAnimation}, MediaKindMedia},
		{"unknown type counts as media", []string{"sticker"}, MediaKindMedia},
		{"empty string ignored", []string{""}, MediaKindText},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DeriveMediaKindFromMediaInfoTypes(c.types); got != c.want {
				t.Errorf("DeriveMediaKindFromMediaInfoTypes(%v) = %q, want %q", c.types, got, c.want)
			}
		})
	}
}

func TestDeriveMediaKindFromBusMedia(t *testing.T) {
	cases := []struct {
		name  string
		files []bus.MediaFile
		want  string
	}{
		{"empty", nil, MediaKindText},
		{"audio mime → voice", []bus.MediaFile{{Path: "x", MimeType: "audio/ogg"}}, MediaKindVoice},
		{"image mime → media", []bus.MediaFile{{Path: "x", MimeType: "image/jpeg"}}, MediaKindMedia},
		{"mixed audio + image → voice wins", []bus.MediaFile{{Path: "a", MimeType: "image/png"}, {Path: "b", MimeType: "audio/mpeg"}}, MediaKindVoice},
		{"path but no mime counts as media", []bus.MediaFile{{Path: "x.mp4"}}, MediaKindMedia},
		{"only empty entries → text", []bus.MediaFile{{}}, MediaKindText},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DeriveMediaKindFromBusMedia(c.files); got != c.want {
				t.Errorf("DeriveMediaKindFromBusMedia(%v) = %q, want %q", c.files, got, c.want)
			}
		})
	}
}
