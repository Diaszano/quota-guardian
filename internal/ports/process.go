package ports

import (
	"context"
	"io"

	"github.com/Diaszano/quota-guardian/internal/domain"
)

// StreamKind identifies whether a stream record originates from stdout or stderr.
type StreamKind string

const (
	StreamStdout StreamKind = "stdout"
	StreamStderr StreamKind = "stderr"
)

// SettlementConfidence values describe certainty regarding process and descendant cleanup.
const (
	SettlementConfirmed = "confirmed"
	SettlementUncertain = "uncertain"
)

// StreamRecord represents a bounded chunk read from process stdout or stderr.
type StreamRecord struct {
	Kind StreamKind
	Data []byte
}

// CommandSpec specifies execution parameters for a supervised process.
type CommandSpec struct {
	Path  string
	Args  []string
	Dir   string
	Env   []string
	Stdin io.Reader
}

// ProcessResult encapsulates exit status and settlement evidence.
type ProcessResult struct {
	ExitCode             int
	Reaped               bool
	Settled              bool // True if parent and descendants settled cleanly
	DescendantsReaped    bool
	SettlementConfidence string
}

// ProcessRunner coordinates bounded, cancellable execution of external processes.
type ProcessRunner interface {
	Run(ctx context.Context, spec CommandSpec, onStart func(domain.ProcessIdentity) error, onRecord func(StreamRecord) error) (ProcessResult, error)
}
