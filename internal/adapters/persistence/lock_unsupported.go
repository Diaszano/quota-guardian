//go:build !linux

package persistence

import (
	"io"

	"github.com/Diaszano/quota-guardian/internal/ports"
)

func AcquireLock(string) (io.Closer, error) { return nil, ports.ErrUnsupported }
