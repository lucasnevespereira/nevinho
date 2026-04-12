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
	modelURL       = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-tiny.bin"
	whisperRepoURL = "https://github.com/ggerganov/whisper.cpp.git"
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

	// Build whisper binary from source
	binaryPath := filepath.Join(whisperDir, "whisper-cli")
	if _, err := os.Stat(binaryPath); err != nil {
		fmt.Println("Building whisper from source...")
		if err := buildWhisper(whisperDir, binaryPath); err != nil {
			return fmt.Errorf("build whisper: %w", err)
		}
		fmt.Println("Whisper built.")
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

func buildWhisper(whisperDir, dest string) error {
	// Check for git and a C compiler
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is required to build whisper")
	}
	if _, err := exec.LookPath("gcc"); err != nil {
		if _, err := exec.LookPath("cc"); err != nil {
			return fmt.Errorf("a C compiler (gcc/cc) is required. Install with: sudo apt install build-essential")
		}
	}
	if _, err := exec.LookPath("cmake"); err != nil {
		fmt.Println("Installing cmake...")
		if err := installCmake(); err != nil {
			return fmt.Errorf("cmake is required: %w", err)
		}
	}

	srcDir := filepath.Join(whisperDir, "whisper.cpp")

	// Clone if not already there
	if _, err := os.Stat(srcDir); err != nil {
		fmt.Println("  Cloning whisper.cpp...")
		cmd := exec.Command("git", "clone", "--depth", "1", whisperRepoURL, srcDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git clone failed: %w", err)
		}
	}

	// Build with cmake
	buildDir := filepath.Join(srcDir, "build")
	os.MkdirAll(buildDir, 0755)

	fmt.Println("  Configuring...")
	cmake := exec.Command("cmake", "..", "-DCMAKE_BUILD_TYPE=Release")
	cmake.Dir = buildDir
	cmake.Stdout = os.Stdout
	cmake.Stderr = os.Stderr
	if err := cmake.Run(); err != nil {
		return fmt.Errorf("cmake configure failed: %w", err)
	}

	fmt.Println("  Compiling...")
	make := exec.Command("cmake", "--build", ".", "--config", "Release", "-j")
	make.Dir = buildDir
	make.Stdout = os.Stdout
	make.Stderr = os.Stderr
	if err := make.Run(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	// Find and copy the binary
	builtBinary := filepath.Join(buildDir, "bin", "whisper-cli")
	if _, err := os.Stat(builtBinary); err != nil {
		return fmt.Errorf("binary not found at %s after build", builtBinary)
	}

	data, err := os.ReadFile(builtBinary)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dest, data, 0755); err != nil {
		return err
	}

	// Clean up source to save disk space
	os.RemoveAll(srcDir)
	return nil
}

func installCmake() error {
	switch runtime.GOOS {
	case "linux":
		for _, cmd := range [][]string{
			{"apt-get", "install", "-y", "cmake"},
			{"dnf", "install", "-y", "cmake"},
			{"apk", "add", "cmake"},
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
		return fmt.Errorf("install cmake manually: sudo apt install cmake")
	case "darwin":
		if _, err := exec.LookPath("brew"); err == nil {
			cmd := exec.Command("brew", "install", "cmake")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			return cmd.Run()
		}
		return fmt.Errorf("install cmake via Homebrew: brew install cmake")
	default:
		return fmt.Errorf("install cmake manually for your platform")
	}
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
