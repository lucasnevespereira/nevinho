package voice

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

const (
	modelURL = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-tiny.bin"
	// whisper.cpp release binary base URL
	whisperVersion = "v1.7.5"
)

// Setup downloads the whisper binary and model to the given directory.
func Setup(whisperDir string) error {
	if err := os.MkdirAll(whisperDir, 0755); err != nil {
		return fmt.Errorf("create whisper dir: %w", err)
	}

	// Install ffmpeg if missing
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		fmt.Println("Installing ffmpeg...")
		if err := installFFmpeg(); err != nil {
			return fmt.Errorf("ffmpeg install failed: %w", err)
		}
		fmt.Println("ffmpeg installed.")
	}

	// Download whisper binary
	binaryPath := filepath.Join(whisperDir, "whisper-cli")
	if _, err := os.Stat(binaryPath); err != nil {
		fmt.Println("Downloading whisper binary...")
		if err := downloadWhisperBinary(binaryPath); err != nil {
			return fmt.Errorf("download whisper: %w", err)
		}
		fmt.Println("Binary downloaded.")
	} else {
		fmt.Println("Whisper binary already installed.")
	}

	// Download model
	modelPath := filepath.Join(whisperDir, "ggml-tiny.bin")
	if _, err := os.Stat(modelPath); err != nil {
		fmt.Println("Downloading whisper model (~75MB)...")
		if err := downloadFile(modelURL, modelPath); err != nil {
			return fmt.Errorf("download model: %w", err)
		}
		fmt.Println("Model downloaded.")
	} else {
		fmt.Println("Whisper model already installed.")
	}

	fmt.Println("Voice messages enabled.")
	return nil
}

func downloadWhisperBinary(dest string) error {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Map Go arch names to whisper.cpp release names
	var platform string
	switch {
	case goos == "linux" && goarch == "amd64":
		platform = "linux-x86_64"
	case goos == "linux" && goarch == "arm64":
		platform = "linux-aarch64"
	case goos == "darwin" && goarch == "amd64":
		platform = "darwin-x86_64"
	case goos == "darwin" && goarch == "arm64":
		platform = "darwin-arm64"
	default:
		return fmt.Errorf("unsupported platform: %s/%s. Build whisper.cpp manually and place binary at %s", goos, goarch, dest)
	}

	// Try downloading from whisper.cpp releases
	url := fmt.Sprintf("https://github.com/ggerganov/whisper.cpp/releases/download/%s/whisper-cli-%s", whisperVersion, platform)
	if err := downloadFile(url, dest); err != nil {
		// Fallback: try building from source
		return fmt.Errorf("could not download binary from %s: %w\nYou can build manually: git clone https://github.com/ggerganov/whisper.cpp && cd whisper.cpp && make && cp build/bin/whisper-cli %s", url, err, dest)
	}
	return os.Chmod(dest, 0755)
}

func downloadFile(url, dest string) error {
	client := &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	// Get file size for progress
	size := resp.ContentLength

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	if size > 0 {
		return downloadWithProgress(f, resp.Body, size)
	}
	_, err = io.Copy(f, resp.Body)
	return err
}

func downloadWithProgress(dst *os.File, src io.Reader, total int64) error {
	buf := make([]byte, 32*1024)
	var written int64
	lastPercent := -1

	for {
		n, err := src.Read(buf)
		if n > 0 {
			nw, werr := dst.Write(buf[:n])
			if werr != nil {
				return werr
			}
			written += int64(nw)

			percent := int(float64(written) / float64(total) * 100)
			if percent != lastPercent && percent%10 == 0 {
				fmt.Printf("  %d%%\n", percent)
				lastPercent = percent
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Remove cleans up the whisper installation.
func Remove(whisperDir string) error {
	return os.RemoveAll(whisperDir)
}

func installFFmpeg() error {
	switch runtime.GOOS {
	case "linux":
		// Try apt (Debian/Ubuntu), then dnf (Fedora/RHEL), then apk (Alpine)
		for _, cmd := range [][]string{
			{"apt-get", "install", "-y", "ffmpeg"},
			{"dnf", "install", "-y", "ffmpeg"},
			{"apk", "add", "ffmpeg"},
		} {
			if _, err := exec.LookPath(cmd[0]); err == nil {
				c := exec.Command("sudo", cmd...)
				c.Stdout = os.Stdout
				c.Stderr = os.Stderr
				if err := c.Run(); err == nil {
					return nil
				}
			}
		}
		return fmt.Errorf("could not install ffmpeg automatically. Install manually:\n  Ubuntu/Debian: sudo apt install ffmpeg\n  Fedora: sudo dnf install ffmpeg\n  Alpine: apk add ffmpeg")
	case "darwin":
		if _, err := exec.LookPath("brew"); err == nil {
			cmd := exec.Command("brew", "install", "ffmpeg")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			return cmd.Run()
		}
		return fmt.Errorf("install Homebrew first (https://brew.sh), then run: brew install ffmpeg")
	default:
		return fmt.Errorf("install ffmpeg manually for your platform")
	}
}
