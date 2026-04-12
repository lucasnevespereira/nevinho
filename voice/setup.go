package voice

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
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
		fmt.Print("  Installing ffmpeg... ")
		if err := installPkg("ffmpeg"); err != nil {
			return fmt.Errorf("ffmpeg install failed: %w", err)
		}
		fmt.Println("done")
	}

	// Build whisper binary from source
	binaryPath := filepath.Join(whisperDir, "whisper-cli")
	if _, err := os.Stat(binaryPath); err != nil {
		if err := buildWhisper(whisperDir, binaryPath); err != nil {
			return fmt.Errorf("build whisper: %w", err)
		}
	} else {
		fmt.Println("  Whisper: already installed")
	}

	// Download model
	modelPath := filepath.Join(whisperDir, "ggml-tiny.bin")
	if _, err := os.Stat(modelPath); err != nil {
		fmt.Print("  Downloading model (~75MB)... ")
		if err := downloadFile(modelURL, modelPath); err != nil {
			return fmt.Errorf("download model: %w", err)
		}
		fmt.Println("done")
	} else {
		fmt.Println("  Model: already installed")
	}

	fmt.Println("  Voice messages enabled.")
	return nil
}

func buildWhisper(whisperDir, dest string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is required to build whisper")
	}
	if _, err := exec.LookPath("gcc"); err != nil {
		if _, err := exec.LookPath("cc"); err != nil {
			return fmt.Errorf("a C compiler is required: sudo apt install build-essential")
		}
	}
	if _, err := exec.LookPath("cmake"); err != nil {
		fmt.Print("  Installing cmake... ")
		if err := installPkg("cmake"); err != nil {
			return fmt.Errorf("cmake is required: %w", err)
		}
		fmt.Println("done")
	}

	srcDir := filepath.Join(whisperDir, "whisper.cpp")

	fmt.Print("  Cloning whisper.cpp... ")
	if _, err := os.Stat(srcDir); err != nil {
		if err := runQuiet("git", "clone", "--depth", "1", whisperRepoURL, srcDir); err != nil {
			return fmt.Errorf("git clone failed: %w", err)
		}
	}
	fmt.Println("done")

	buildDir := filepath.Join(srcDir, "build")
	os.MkdirAll(buildDir, 0755)

	fmt.Print("  Compiling whisper... ")
	if err := runQuietIn(buildDir, "cmake", "..", "-DCMAKE_BUILD_TYPE=Release"); err != nil {
		return fmt.Errorf("cmake configure failed: %w", err)
	}
	if err := runQuietIn(buildDir, "cmake", "--build", ".", "--config", "Release", "-j"); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}
	fmt.Println("done")

	builtBinary := filepath.Join(buildDir, "bin", "whisper-cli")
	if _, err := os.Stat(builtBinary); err != nil {
		return fmt.Errorf("binary not found after build")
	}

	data, err := os.ReadFile(builtBinary)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dest, data, 0755); err != nil {
		return err
	}

	os.RemoveAll(srcDir)
	return nil
}

// runQuiet runs a command with stdout/stderr suppressed, showing dots as progress.
func runQuiet(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	stop := dots()
	err := cmd.Run()
	stop()
	return err
}

// runQuietIn runs a command in a directory with output suppressed, showing dots.
func runQuietIn(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	stop := dots()
	err := cmd.Run()
	stop()
	return err
}

// dots prints a dot every second until stop is called.
func dots() func() {
	var once sync.Once
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Print(".")
			}
		}
	}()
	return func() {
		once.Do(func() { close(done) })
	}
}

// installPkg installs a package using the system package manager.
func installPkg(pkg string) error {
	switch runtime.GOOS {
	case "linux":
		for _, pm := range [][]string{
			{"apt-get", "install", "-y"},
			{"dnf", "install", "-y"},
			{"apk", "add"},
		} {
			if _, err := exec.LookPath(pm[0]); err == nil {
				args := append([]string{pm[0]}, pm[1:]...)
				args = append(args, pkg)
				cmd := exec.Command("sudo", args...)
				cmd.Stdout = io.Discard
				cmd.Stderr = io.Discard
				stop := dots()
				err := cmd.Run()
				stop()
				if err == nil {
					return nil
				}
			}
		}
		return fmt.Errorf("could not install %s automatically", pkg)
	case "darwin":
		if _, err := exec.LookPath("brew"); err == nil {
			cmd := exec.Command("brew", "install", pkg)
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			stop := dots()
			err := cmd.Run()
			stop()
			return err
		}
		return fmt.Errorf("install Homebrew first: https://brew.sh")
	default:
		return fmt.Errorf("install %s manually", pkg)
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

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	if resp.ContentLength > 0 {
		return copyWithProgress(f, resp.Body, resp.ContentLength)
	}
	_, err = io.Copy(f, resp.Body)
	return err
}

func copyWithProgress(dst io.Writer, src io.Reader, total int64) error {
	buf := make([]byte, 32*1024)
	var written int64
	lastPct := -1

	for {
		n, err := src.Read(buf)
		if n > 0 {
			nw, werr := dst.Write(buf[:n])
			if werr != nil {
				return werr
			}
			written += int64(nw)
			pct := int(float64(written) / float64(total) * 100)
			if pct/25 != lastPct/25 {
				fmt.Printf(" %d%%", pct)
				lastPct = pct
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

// IsAvailable checks if whisper is set up.
func IsAvailable(whisperDir string) bool {
	binary := filepath.Join(whisperDir, "whisper-cli")
	model := filepath.Join(whisperDir, "ggml-tiny.bin")
	_, errBin := os.Stat(binary)
	_, errModel := os.Stat(model)
	return errBin == nil && errModel == nil
}
