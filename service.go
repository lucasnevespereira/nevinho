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

func startService() {
	if runtime.GOOS != "linux" || !hasSystemd() {
		fmt.Println("No systemd found. On macOS or non-systemd Linux, run:")
		fmt.Println("  nohup nevinho start &")
		fmt.Println()
		fmt.Println("Or use 'nevinho start' on a Linux server with systemd.")
		return
	}

	if _, err := os.Stat(serviceFile); os.IsNotExist(err) {
		installService()
	}

	runCmd("systemctl", "start", "nevinho")
	fmt.Println("nevinho started.")
	fmt.Println("  nevinho logs     show live logs")
	fmt.Println("  nevinho stop     stop the bot")
}

func stopService() {
	if !hasSystemd() {
		fmt.Println("No systemd found. Use: pkill nevinho")
		return
	}
	runCmd("systemctl", "stop", "nevinho")
	fmt.Println("nevinho stopped.")
}

func showLogs() {
	if !hasSystemd() {
		fmt.Println("No systemd found. Logs are only available with systemd.")
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
ExecStart=%s start
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, binPath)

	if err := os.WriteFile(serviceFile, []byte(unit), 0644); err != nil {
		log.Fatalf("failed to write service file (try running as root): %v", err)
	}

	runCmd("systemctl", "daemon-reload")
	runCmd("systemctl", "enable", "nevinho")
}

func hasSystemd() bool {
	_, err := exec.LookPath("systemctl")
	return err == nil
}

func runCmd(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("%s failed: %v", name, err)
	}
}
