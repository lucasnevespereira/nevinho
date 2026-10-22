package cmd

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lucasnevespereira/nevinho/chatgpt"
	"github.com/lucasnevespereira/nevinho/config"
)

// Login connects a user's eligible ChatGPT plan to this self-hosted install.
// The browser may be on another machine; pasting its final redirect URL works
// when the loopback callback cannot reach the process on a remote VM.
func Login(configDir string, args []string) {
	newAccount := false
	switch strings.Join(args, " ") {
	case "", "chatgpt":
	case "--new", "chatgpt --new":
		newAccount = true
	default:
		fmt.Fprintln(os.Stderr, "usage: nevinho login [chatgpt] [--new]")
		os.Exit(2)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot start ChatGPT callback: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()
	redirect := "http://" + listener.Addr().String() + "/auth/callback"
	store := chatgpt.NewStore(configDir)
	attempt, err := store.Begin(redirect, newAccount)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ChatGPT login: %v\n", err)
		os.Exit(1)
	}
	callbacks := make(chan string, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		select {
		case callbacks <- "http://" + r.Host + r.URL.RequestURI():
		default:
		}
		fmt.Fprintln(w, "Return to nevinho to finish signing in.")
	})
	server.Handler = mux
	go server.Serve(listener)
	defer server.Close()

	fmt.Println("Continue with ChatGPT. Open this link in your browser:")
	fmt.Println(attempt.AuthorizeURL())
	fmt.Println("Waiting for browser sign-in. On a remote VM, paste the final redirect URL here and press Enter.")
	manual := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		manual <- strings.TrimSpace(line)
	}()
	var callback string
	select {
	case callback = <-callbacks:
	case callback = <-manual:
	case <-time.After(5 * time.Minute):
		fmt.Fprintln(os.Stderr, "ChatGPT login timed out")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	email, err := attempt.Complete(ctx, callback)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ChatGPT login: %v\n", err)
		os.Exit(1)
	}
	models, err := store.FetchModels(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Connected as %s, but model discovery failed: %v\n", email, err)
		os.Exit(1)
	}
	if len(models) == 0 {
		fmt.Fprintln(os.Stderr, "Connected, but this account has no available models")
		os.Exit(1)
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	model := chatgpt.SelectModel(models, cfg.Model)
	if err := cfg.Set("MODEL", model); err != nil {
		fmt.Fprintf(os.Stderr, "save model: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Connected as %s. Model: %s. Run nevinho to start.\n", email, model)
}
