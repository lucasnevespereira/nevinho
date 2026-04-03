package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lucasnevespereira/nevinho/config"
)

const serviceFile = "/etc/systemd/system/nevinho.service"

func startService(configDir string) {
	if runtime.GOOS != "linux" {
		run(configDir)
		return
	}

	// Pre-flight: verify config exists before installing a service that would crash-loop
	cfg, err := config.Load(configDir)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	if cfg.DiscordBotToken == "" || cfg.DiscordOwnerID == "" {
		fmt.Println("Config incomplete. Run 'nevinho setup' first.")
		return
	}

	if isRunning() {
		fmt.Println("nevinho is already running.")
		return
	}

	installService()
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
	if !isRunning() {
		fmt.Println("nevinho is not running.")
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

func serviceStatus() {
	if runtime.GOOS != "linux" {
		fmt.Println("nevinho " + version)
		fmt.Println("Service management is only available on Linux.")
		return
	}

	fmt.Println("nevinho " + version)
	if isRunning() {
		fmt.Println("Status: running")
	} else {
		fmt.Println("Status: stopped")
	}
}

func restartService() {
	if runtime.GOOS != "linux" || !isRunning() {
		return
	}
	systemctl("restart", "nevinho")
}

func isRunning() bool {
	cmd := exec.Command("systemctl", "is-active", "--quiet", "nevinho")
	return cmd.Run() == nil
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

	home, _ := os.UserHomeDir()

	unit := fmt.Sprintf(`[Unit]
Description=nevinho
After=network.target

[Service]
Type=simple
ExecStart=%s --run
Restart=always
RestartSec=5
TimeoutStopSec=5
Environment=HOME=%s

[Install]
WantedBy=multi-user.target
`, binPath, home)

	if err := os.WriteFile(serviceFile, []byte(unit), 0644); err != nil {
		log.Fatalf("failed to write service file (try running as root): %v", err)
	}

	systemctl("daemon-reload")
	systemctl("enable", "nevinho")
}

func systemctl(args ...string) {
	cmd := exec.Command("systemctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("systemctl %s failed: %v", strings.Join(args, " "), err)
	}
}
