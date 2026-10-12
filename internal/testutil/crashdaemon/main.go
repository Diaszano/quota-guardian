package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/Diaszano/quota-guardian/internal/adapters/persistence"
	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

func main() {
	if len(os.Args) != 4 {
		os.Exit(2)
	}
	ctx := context.Background()
	store, err := persistence.Open(ctx, os.Args[1])
	if err != nil {
		os.Exit(3)
	}
	session, err := store.Get(ctx, os.Args[2])
	if err != nil {
		os.Exit(4)
	}
	now := time.Now().UTC()
	attempt, claimed, err := store.ClaimStart(ctx, ports.ClaimRequest{
		SessionID: session.ID, ExpectedRevision: session.Revision, OwnerEpoch: "crashed-daemon",
		Now: now, LeaseDuration: time.Hour,
	})
	if err != nil || !claimed {
		os.Exit(5)
	}
	childPID := 0
	switch os.Args[3] {
	case "claimed":
	case "launching", "spawn", "identity", "exit-before-commit":
		if err := store.MarkLaunching(ctx, attempt); err != nil {
			os.Exit(6)
		}
		if os.Args[3] == "spawn" || os.Args[3] == "identity" || os.Args[3] == "exit-before-commit" {
			binary, duration := "/bin/sleep", "60"
			if os.Args[3] == "exit-before-commit" {
				binary, duration = "/bin/sleep", "0.01"
			}
			child := exec.Command(binary, duration)
			child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := child.Start(); err != nil {
				os.Exit(7)
			}
			childPID = child.Process.Pid
			if os.Args[3] != "spawn" {
				if err := store.RecordIdentity(ctx, attempt, domain.ProcessIdentity{PID: childPID, BootID: "old-boot", StartTicks: 1}); err != nil {
					os.Exit(8)
				}
			}
			if os.Args[3] == "exit-before-commit" {
				_ = child.Wait()
				childPID = 0
			}
		}
	default:
		os.Exit(9)
	}
	_, _ = fmt.Fprintf(os.Stdout, "READY %s %s\n", strconv.Itoa(os.Getpid()), strconv.Itoa(childPID))
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	_ = store.Close()
}
