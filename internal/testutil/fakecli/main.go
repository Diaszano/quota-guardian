package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	var (
		mode       string
		sessionID  string
		sleepDelay time.Duration
		floodBytes int
	)

	flag.StringVar(&mode, "mode", "success", "operation mode")
	flag.StringVar(&sessionID, "session-id", "test-session", "session identifier")
	flag.DurationVar(&sleepDelay, "sleep", 10*time.Second, "sleep duration for delayed modes")
	flag.IntVar(&floodBytes, "flood-bytes", 2*1024*1024, "number of bytes to flood")
	flag.Parse()

	switch mode {
	case "success":
		fmt.Fprintf(os.Stdout, `{"event":"result","status":"success","session_id":"%s"}`+"\n", sessionID)
		os.Exit(0)

	case "quota-reset":
		fmt.Fprintln(os.Stderr, `{"event":"error","code":"quota_exhausted","reset_at":"2026-10-11T20:00:00Z"}`)
		os.Exit(1)

	case "quota-unknown":
		fmt.Fprintln(os.Stderr, `{"event":"error","code":"quota_exhausted"}`)
		os.Exit(1)

	case "interrupted":
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		fmt.Fprintln(os.Stdout, `{"event":"started","status":"waiting_signal"}`)
		<-sigChan
		fmt.Fprintln(os.Stderr, `{"event":"interrupted"}`)
		os.Exit(130)

	case "invalid-id":
		fmt.Fprintln(os.Stderr, `{"event":"error","code":"invalid_session","message":"invalid session id"}`)
		os.Exit(1)

	case "no-resume":
		fmt.Fprintln(os.Stderr, `{"event":"error","code":"resume_unsupported","message":"cannot resume session"}`)
		os.Exit(1)

	case "repeated-quota":
		fmt.Fprintln(os.Stderr, `{"event":"error","code":"quota_repeated","message":"repeated quota exhaustion"}`)
		os.Exit(1)

	case "auth":
		fmt.Fprintln(os.Stderr, `{"event":"error","code":"auth_failed","message":"authentication failed"}`)
		os.Exit(1)

	case "soft-denial":
		// Soft denial exits 0 despite permission denied error
		fmt.Fprintln(os.Stderr, `permission denied: operation not allowed`)
		fmt.Fprintf(os.Stdout, `{"event":"result","status":"permission_denied","session_id":"%s"}`+"\n", sessionID)
		os.Exit(0)

	case "blocked-child":
		// Spawns a background worker in the same process group, then parent exits 0 immediately.
		cmd := exec.Command(os.Args[0], "--mode", "sleep-worker", "--sleep", "30s")
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to spawn blocked child: %v\n", err)
			os.Exit(1)
		}
		// Print child pid so parent output can be observed if needed
		fmt.Fprintf(os.Stdout, `{"event":"parent_exiting","child_pid":%d}`+"\n", cmd.Process.Pid)
		os.Exit(0)

	case "sleep-worker":
		time.Sleep(sleepDelay)
		os.Exit(0)

	case "flood-output":
		// Flood stderr with massive data (chunks of 64 KiB), while writing final result to stdout
		chunk := strings.Repeat("E", 64*1024)
		totalWritten := 0
		for totalWritten < floodBytes {
			toWrite := len(chunk)
			if totalWritten+toWrite > floodBytes {
				toWrite = floodBytes - totalWritten
			}
			if _, err := os.Stderr.WriteString(chunk[:toWrite]); err != nil {
				break
			}
			totalWritten += toWrite
		}
		fmt.Fprintf(os.Stdout, `{"event":"result","status":"completed","session_id":"%s"}`+"\n", sessionID)
		os.Exit(0)

	case "wrong-id":
		fmt.Fprintln(os.Stdout, `{"event":"result","session_id":"wrong-session-999"}`)
		os.Exit(0)

	case "echo-args":
		for i, arg := range flag.Args() {
			fmt.Fprintf(os.Stdout, "ARG[%d]=%s\n", i, arg)
		}
		os.Exit(0)

	case "spawn-grandchild":
		// Spawns a child process which in turn spawns a grandchild process, all sleeping.
		cmd := exec.Command(os.Args[0], "--mode", "spawn-grandchild-child", "--sleep", "30s")
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to start child: %v\n", err)
			os.Exit(1)
		}
		// Wait for signal or child
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		_ = cmd.Process.Kill()
		os.Exit(143)

	case "spawn-grandchild-child":
		cmd := exec.Command(os.Args[0], "--mode", "sleep-worker", "--sleep", "30s")
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to start grandchild: %v\n", err)
			os.Exit(1)
		}
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		_ = cmd.Process.Kill()
		os.Exit(143)

	default:
		fmt.Fprintf(os.Stderr, "unknown mode: %s\n", mode)
		os.Exit(2)
	}
}
