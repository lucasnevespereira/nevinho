package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Uninstall stops nevinho, removes the systemd unit, the binary, and the
// config directory. Aimed at full teardown so a subsequent install.sh run
// starts from a clean slate.
//
//	nevinho uninstall          confirms before removing each piece
//	nevinho uninstall --yes    skip the prompt
//	nevinho uninstall --keep-config  preserve ~/.nevinho
func Uninstall(configDir string, args []string) {
	yes := false
	keepConfig := false
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		case "--keep-config":
			keepConfig = true
		case "help", "-h", "--help":
			printUninstallUsage(os.Stdout)
			return
		}
	}

	binPath, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(binPath); err == nil {
		binPath = resolved
	}

	fmt.Println("This will remove:")
	if runtime.GOOS == "linux" {
		fmt.Println("  • systemd service /etc/systemd/system/nevinho.service")
	}
	fmt.Printf("  • binary           %s\n", binPath)
	if keepConfig {
		fmt.Printf("  • (keeping)        %s\n", configDir)
	} else {
		fmt.Printf("  • config dir       %s (tokens, memory, summaries)\n", configDir)
	}
	fmt.Println()

	if !yes && !confirm("Proceed?") {
		fmt.Println("Aborted.")
		return
	}

	if runtime.GOOS == "linux" {
		_ = exec.Command("systemctl", "stop", "nevinho").Run()
		_ = exec.Command("systemctl", "disable", "nevinho").Run()
		_ = exec.Command("systemctl", "reset-failed", "nevinho").Run()
		if err := os.Remove(serviceFile); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "remove unit file: %v\n", err)
		}
		_ = exec.Command("systemctl", "daemon-reload").Run()
		fmt.Println("Removed systemd service.")
	}

	if !keepConfig {
		if err := os.RemoveAll(configDir); err != nil {
			fmt.Fprintf(os.Stderr, "remove config dir: %v\n", err)
		} else {
			fmt.Printf("Removed %s\n", configDir)
		}
	}

	if binPath != "" {
		// Removing the running binary works on POSIX. The kernel keeps the
		// file open until this process exits, then frees the inode.
		if err := os.Remove(binPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "remove binary: %v\n", err)
		} else {
			fmt.Printf("Removed %s\n", binPath)
		}
	}

	fmt.Println()
	fmt.Println("Done. Reinstall with:")
	fmt.Println("  curl -sSL https://raw.githubusercontent.com/lucasnevespereira/nevinho/main/install.sh | bash")
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func printUninstallUsage(w *os.File) {
	fmt.Fprintln(w, "Usage: nevinho uninstall [--yes] [--keep-config]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Removes the systemd service, the binary, and ~/.nevinho.")
	fmt.Fprintln(w, "Pass --yes to skip the confirmation prompt.")
	fmt.Fprintln(w, "Pass --keep-config to preserve ~/.nevinho (tokens, memory).")
}
