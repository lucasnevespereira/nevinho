package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestImageMediaType(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		filename    string
		want        string
	}{
		{"png by content type", "image/png", "shot.png", "image/png"},
		{"jpeg by content type", "image/jpeg", "x.jpg", "image/jpeg"},
		{"gif by content type", "image/gif", "x.gif", "image/gif"},
		{"webp by content type", "image/webp", "x.webp", "image/webp"},
		{"png by extension when content empty", "", "screenshot.PNG", "image/png"},
		{"jpeg by extension jpg", "", "photo.jpg", "image/jpeg"},
		{"jpeg by extension jpeg", "", "photo.jpeg", "image/jpeg"},
		{"unsupported type", "image/heic", "live.heic", ""},
		{"text file", "text/plain", "notes.txt", ""},
		{"audio not image", "audio/ogg", "v.ogg", ""},
		{"empty", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			att := &discordgo.MessageAttachment{
				ContentType: c.contentType,
				Filename:    c.filename,
			}
			if got := imageMediaType(att); got != c.want {
				t.Errorf("imageMediaType(%q, %q) = %q, want %q", c.contentType, c.filename, got, c.want)
			}
		})
	}
}
