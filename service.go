package main

import (
	"fmt"
	"log"
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

func systemctl(args ...string) {
	cmd := exec.Command("systemctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("systemctl %v failed: %v", args, err)
	}
}
