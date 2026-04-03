package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const serviceFile = "/etc/systemd/system/nevinho.service"

func startService(configDir string) {
	if runtime.GOOS != "linux" {
		run(configDir)
		return
	}

	if _, err := os.Stat(serviceFile); os.IsNotExist(err) {
		installService()
	}

	systemctl("start", "nevinho")
	fmt.Println("nevinho started.")
	fmt.Println("  nevinho logs     show live logs")
	fmt.Println("  nevinho stop     stop the bot")
}

func stopService() {
	if runtime.GOOS != "linux" {
		fmt.Println("On macOS, stop with Ctrl+C.")
		return
	}
	systemctl("stop", "nevinho")
	fmt.Println("nevinho stopped.")
}

func showLogs() {
	if runtime.GOOS != "linux" {
		fmt.Println("Logs are available on Linux with systemd.")
		return
	}
	cmd := exec.Command("journalctl", "-u", "nevinho", "-f", "--no-pager")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
}

func installService() {
	binPath, err := os.Executable()
	if err != nil {
		log.Fatalf("failed to find binary path: %v", err)
	}
	binPath, err = filepath.EvalSymlinks(binPath)
	if err != nil {
		log.Fatalf("failed to resolve binary path: %v", err)
	}

	unit := fmt.Sprintf(`[Unit]
Description=nevinho
After=network.target

[Service]
Type=simple
ExecStart=%s --run
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, binPath)

	if err := os.WriteFile(serviceFile, []byte(unit), 0644); err != nil {
		log.Fatalf("failed to write service file (try running as root): %v", err)
	}

	systemctl("daemon-reload")
	systemctl("enable", "nevinho")
}

const repo = "lucasnevespereira/nevinho"

func upgrade() {
	latest, err := fetchLatestVersion()
	if err != nil {
		log.Fatalf("failed to check latest version: %v", err)
	}

	if latest == version {
		fmt.Printf("Already on latest version (%s).\n", version)
		return
	}

	fmt.Printf("Upgrading nevinho %s -> %s...\n", version, latest)

	binPath, err := os.Executable()
	if err != nil {
		log.Fatalf("failed to find binary path: %v", err)
	}
	binPath, err = filepath.EvalSymlinks(binPath)
	if err != nil {
		log.Fatalf("failed to resolve binary path: %v", err)
	}

	binary := fmt.Sprintf("nevinho-%s-%s", runtime.GOOS, runtime.GOARCH)
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, latest, binary)

	resp, err := http.Get(url)
	if err != nil {
		log.Fatalf("download failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("download failed: %s", resp.Status)
	}

	tmp := binPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		log.Fatalf("failed to write update: %v", err)
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		log.Fatalf("download interrupted: %v", err)
	}
	f.Close()

	if err := os.Rename(tmp, binPath); err != nil {
		os.Remove(tmp)
		log.Fatalf("failed to replace binary: %v", err)
	}

	fmt.Printf("Updated to %s. Restart with 'nevinho start'.\n", latest)
}

func fetchLatestVersion() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return release.TagName, nil
}

func systemctl(args ...string) {
	cmd := exec.Command("systemctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("systemctl %v failed: %v", args, err)
	}
}
