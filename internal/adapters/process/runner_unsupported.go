//go:build !linux

package process

import (
	"context"
	"syscall"

	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

// Runner is an unsupported implementation of ports.ProcessRunner for non-Linux OS.
type Runner struct{}

// NewRunner creates a new Runner.
func NewRunner() *Runner {
	return &Runner{}
}

// Run always returns ports.ErrUnsupported on non-Linux platforms.
func (r *Runner) Run(ctx context.Context, spec ports.CommandSpec, onStart func(domain.ProcessIdentity) error, onRecord func(ports.StreamRecord) error) (ports.ProcessResult, error) {
	return ports.ProcessResult{}, ports.ErrUnsupported
}

// SignalProcess returns ports.ErrUnsupported on non-Linux platforms.
func SignalProcess(identity domain.ProcessIdentity, sig syscall.Signal) error {
	return ports.ErrUnsupported
}
