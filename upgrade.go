package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

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

	fmt.Printf("Updated to %s.\n", latest)
	restartService()
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
