package voice

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const transcribeTimeout = 30 * time.Second

// Transcribe converts audio to text using local whisper.cpp.
// The audio file is converted to WAV via ffmpeg, then transcribed.
func Transcribe(whisperDir string, audioData []byte, filename string) (string, error) {
	binary := filepath.Join(whisperDir, "whisper-cli")
	model := filepath.Join(whisperDir, "ggml-tiny.bin")

	if _, err := os.Stat(binary); err != nil {
		return "", fmt.Errorf("whisper not installed (run nevinho setup)")
	}
	if _, err := os.Stat(model); err != nil {
		return "", fmt.Errorf("whisper model not found (run nevinho setup)")
	}

	// Write audio to temp file
	tmpDir, err := os.MkdirTemp("", "nevinho-voice-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	audioPath := filepath.Join(tmpDir, filename)
	if err := os.WriteFile(audioPath, audioData, 0644); err != nil {
		return "", fmt.Errorf("write audio: %w", err)
	}

	// Convert to 16kHz mono WAV for whisper.cpp
	wavPath := filepath.Join(tmpDir, "audio.wav")
	ffmpeg := exec.Command("ffmpeg", "-i", audioPath, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", wavPath, "-y")
	ffmpeg.Stderr = nil
	if err := ffmpeg.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg conversion failed: %w", err)
	}

	// Run whisper
	ctx, cancel := context.WithTimeout(context.Background(), transcribeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "-m", model, "-f", wavPath, "--no-timestamps", "-t", "4")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("transcription timed out")
		}
		return "", fmt.Errorf("whisper failed: %w\n%s", err, string(output))
	}

	text := strings.TrimSpace(string(output))
	// whisper.cpp outputs some log lines, the transcription is the last part
	text = cleanWhisperOutput(text)
	return text, nil
}

// cleanWhisperOutput strips whisper.cpp log lines, keeping only transcription text.
func cleanWhisperOutput(output string) string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip whisper.cpp system info and log lines
		if strings.HasPrefix(line, "whisper_") ||
			strings.HasPrefix(line, "main:") ||
			strings.HasPrefix(line, "system_info:") ||
			strings.HasPrefix(line, "output_") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, " ")
}

