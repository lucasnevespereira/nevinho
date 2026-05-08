package cmd

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

// Start starts nevinho as a systemd service on Linux. On other platforms it
// runs the bot in the foreground via Serve.
func Start(configDir, version, selfDoc string) {
	if runtime.GOOS != "linux" {
		Serve(configDir, version, selfDoc)
		return
	}

	// Pre flight, verify config exists before installing a service that
	// would otherwise crash loop on the first start.
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

// Stop stops the running nevinho systemd service.
func Stop() {
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

// Logs streams or prints recent log lines from journalctl.
func Logs(args []string) {
	if runtime.GOOS != "linux" {
		fmt.Println("Logs are available on Linux with systemd.")
		return
	}

	jArgs := []string{"-u", "nevinho", "--no-pager", "-o", "cat"}

	full := false
	last := ""
	for i, a := range args {
		switch a {
		case "--full":
			full = true
		case "--last":
			if i+1 < len(args) {
				last = args[i+1]
			}
		}
	}

	if last != "" {
		jArgs = append(jArgs, "-n", last)
	} else {
		jArgs = append(jArgs, "-f")
	}

	if full {
		jArgs = append(jArgs, "--no-hostname")
	}

	cmd := exec.Command("journalctl", jArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
}

// Status reports whether nevinho is currently running.
func Status(version string) {
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

// Restart restarts the running unit. No op when not running.
func Restart() {
	if runtime.GOOS != "linux" || !isRunning() {
		return
	}
	systemctl("restart", "nevinho")
}

// InstallSystemdUnit (re)writes the systemd unit file and reloads the daemon.
// Used by Start on first install and by Upgrade to refresh after binary swap.
// No op on platforms without systemd.
func InstallSystemdUnit() {
	if runtime.GOOS != "linux" {
		return
	}
	installService()
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

	// StartLimitIntervalSec + StartLimitBurst stop a hard restart loop.
	// After 5 failures in 10 minutes systemd holds the unit. The user clears
	// it with: systemctl reset-failed nevinho && systemctl start nevinho.
	// Without this, a bad token or Discord rate limit makes nevinho thrash
	// the gateway, which extends the rate limit and floods the journal.
	unit := fmt.Sprintf(`[Unit]
Description=nevinho
After=network.target
StartLimitIntervalSec=600
StartLimitBurst=5

[Service]
Type=simple
ExecStart=%s serve
Restart=on-failure
RestartSec=10
TimeoutStopSec=30
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
