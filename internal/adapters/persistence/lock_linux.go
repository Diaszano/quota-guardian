//go:build linux

package persistence

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/Diaszano/quota-guardian/internal/ports"
	"golang.org/x/sys/unix"
)

type daemonLock struct {
	file *os.File
	once sync.Once
	err  error
}

func AcquireLock(stateDir string) (io.Closer, error) {
	info, err := os.Lstat(stateDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, ports.ErrUnsupported
	}
	path := filepath.Join(stateDir, "daemon.lock")
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, err
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ports.ErrBusy
		}
		return nil, err
	}
	return &daemonLock{file: file}, nil
}

func (l *daemonLock) Close() error {
	l.once.Do(func() {
		if err := unix.Flock(int(l.file.Fd()), unix.LOCK_UN); err != nil {
			l.err = err
		}
		if err := l.file.Close(); l.err == nil {
			l.err = err
		}
	})
	return l.err
}
