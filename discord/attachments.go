package discord

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/logger"
	"github.com/lucasnevespereira/nevinho/voice"
)

var audioExtensions = map[string]bool{
	".ogg": true, ".mp3": true, ".wav": true, ".m4a": true, ".webm": true,
}

func isAudioAttachment(att *discordgo.MessageAttachment) bool {
	if strings.HasPrefix(att.ContentType, "audio/") {
		return true
	}
	ext := strings.ToLower(filepath.Ext(att.Filename))
	return audioExtensions[ext]
}

var imageMediaTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

var imageExtMediaTypes = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// imageMediaType returns the canonical media type for a Discord attachment, or
// empty string if it is not a supported image. Falls back on the filename
// extension when ContentType is missing or generic.
func imageMediaType(att *discordgo.MessageAttachment) string {
	if imageMediaTypes[att.ContentType] {
		return att.ContentType
	}
	ext := strings.ToLower(filepath.Ext(att.Filename))
	return imageExtMediaTypes[ext]
}

func (b *Bot) downloadImage(att *discordgo.MessageAttachment, mediaType string) (llm.Image, error) {
	return b.downloadImageURL(att.URL, mediaType)
}

// downloadImageURL fetches an image from any URL. mediaType may be empty,
// in which case it is inferred from the response Content-Type header or
// the URL's file extension. Used for both Discord attachments and the
// images embedded in link previews (m.Embeds).
func (b *Bot) downloadImageURL(url, mediaType string) (llm.Image, error) {
	resp, err := http.Get(url)
	if err != nil {
		return llm.Image{}, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if mediaType == "" {
		if ct := resp.Header.Get("Content-Type"); imageMediaTypes[ct] {
			mediaType = ct
		}
	}
	if mediaType == "" {
		clean := url
		if i := strings.IndexByte(clean, '?'); i >= 0 {
			clean = clean[:i]
		}
		ext := strings.ToLower(filepath.Ext(clean))
		mediaType = imageExtMediaTypes[ext]
	}
	if mediaType == "" {
		return llm.Image{}, fmt.Errorf("unsupported image type")
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return llm.Image{}, fmt.Errorf("read: %w", err)
	}
	if len(data) > maxImageBytes {
		return llm.Image{}, fmt.Errorf("image exceeds %d bytes", maxImageBytes)
	}
	return llm.Image{MediaType: mediaType, Data: data}, nil
}

func (b *Bot) transcribeAttachment(s *discordgo.Session, channelID string, att *discordgo.MessageAttachment) string {
	whisperDir := filepath.Join(b.cfg.Dir(), "whisper")
	if !voice.IsAvailable(whisperDir) {
		s.ChannelMessageSend(channelID, "Voice messages not enabled. Run `nevinho setup` to enable.")
		return ""
	}

	resp, err := http.Get(att.URL)
	if err != nil {
		logger.Err(fmt.Errorf("download voice: %w", err))
		s.ChannelMessageSend(channelID, "Failed to download voice message.")
		return ""
	}
	defer resp.Body.Close()

	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Err(fmt.Errorf("read voice: %w", err))
		s.ChannelMessageSend(channelID, "Failed to read voice message.")
		return ""
	}

	logger.Info("transcribing voice message...")
	text, err := voice.Transcribe(whisperDir, audio, att.Filename)
	if err != nil {
		logger.Err(fmt.Errorf("transcribe: %w", err))
		s.ChannelMessageSend(channelID, fmt.Sprintf("Transcription failed: %v", err))
		return ""
	}

	return text
}
