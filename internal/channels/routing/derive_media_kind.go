package routing

import (
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels/media"
)

// MediaKindFromFlags maps (hasVoice, hasMedia) → resolver media kind.
// Voice wins over non-voice media. Used by channels after they classify
// their native media list.
func MediaKindFromFlags(hasVoice, hasMedia bool) string {
	switch {
	case hasVoice:
		return MediaKindVoice
	case hasMedia:
		return MediaKindMedia
	default:
		return MediaKindText
	}
}

// DeriveMediaKindFromMediaInfoTypes scans the channel-native MediaInfo.Type
// strings (media.TypeAudio, media.TypeVoice, media.TypeImage, …) and returns
// the resolver media kind. Unknown type strings count as "media" so an
// undocumented channel type doesn't silently become "text".
func DeriveMediaKindFromMediaInfoTypes(types []string) string {
	hasVoice, hasMedia := false, false
	for _, t := range types {
		switch t {
		case media.TypeAudio, media.TypeVoice:
			hasVoice = true
		case media.TypeImage, media.TypeVideo, media.TypeDocument, media.TypeAnimation:
			hasMedia = true
		default:
			if t != "" {
				hasMedia = true
			}
		}
	}
	return MediaKindFromFlags(hasVoice, hasMedia)
}

// DeriveMediaKindFromBusMedia inspects []bus.MediaFile (post-conversion) and
// returns the resolver media kind based on the MimeType prefix. Used by
// BaseChannel.HandleMessage which only has bus.MediaFile available.
//
// Treats audio/* MIME types as voice; any other non-empty MimeType as media.
// Items with an empty MimeType but a Path also count as media (channels that
// pass raw file paths without classifying — Zalo OA, Slack, etc.).
func DeriveMediaKindFromBusMedia(files []bus.MediaFile) string {
	hasVoice, hasMedia := false, false
	for _, f := range files {
		if f.MimeType != "" {
			if strings.HasPrefix(f.MimeType, "audio/") {
				hasVoice = true
				continue
			}
			hasMedia = true
			continue
		}
		if f.Path != "" {
			hasMedia = true
		}
	}
	return MediaKindFromFlags(hasVoice, hasMedia)
}
