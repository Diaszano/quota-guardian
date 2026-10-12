//go:build linux

package ipc

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

func authorizedUID(uid int) bool { return uid == os.Getuid() }

type peerConn struct {
	net.Conn
	uid uint32
}

func (c peerConn) RemoteAddr() net.Addr { return peerAddr(c.uid) }

type peerAddr uint32

func (a peerAddr) Network() string { return "unix" }
func (a peerAddr) String() string  { return fmt.Sprintf("uid:%d", uint32(a)) }

type peerListener struct{ net.Listener }

func (l peerListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		c.Close()
		return nil, fmt.Errorf("unexpected listener connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		c.Close()
		return nil, err
	}
	var cred *unix.Ucred
	var sockErr error
	if err = raw.Control(func(fd uintptr) { cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil {
		c.Close()
		return nil, err
	}
	if sockErr != nil {
		c.Close()
		return nil, sockErr
	}
	return peerConn{Conn: c, uid: cred.Uid}, nil
}

type limitedListener struct {
	net.Listener
	mu     sync.Mutex
	active int
	max    int
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		c, e := l.Listener.Accept()
		if e != nil {
			return nil, e
		}
		l.mu.Lock()
		if l.active < l.max {
			l.active++
			l.mu.Unlock()
			return &countedConn{Conn: c, done: func() { l.mu.Lock(); l.active--; l.mu.Unlock() }}, nil
		}
		l.mu.Unlock()
		_ = c.Close()
	}
}

type countedConn struct {
	net.Conn
	once sync.Once
	done func()
}

func (c *countedConn) Close() error { e := c.Conn.Close(); c.once.Do(c.done); return e }

func Listen(ctx context.Context, runtimeDir string, handler http.Handler) (net.Listener, error) {
	if !filepath.IsAbs(runtimeDir) {
		return nil, fmt.Errorf("runtime directory must be absolute")
	}
	info, err := os.Lstat(runtimeDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("unsafe runtime directory")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return nil, fmt.Errorf("runtime directory owner mismatch")
	}
	dir := filepath.Join(runtimeDir, "quota-guardian")
	if err = os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err = os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("unsafe socket directory")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return nil, fmt.Errorf("socket directory owner mismatch")
	}
	path := filepath.Join(dir, "control.sock")
	if _, err = os.Lstat(path); err == nil {
		return nil, fmt.Errorf("control socket already exists")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		l.Close()
		os.Remove(path)
		return nil, err
	}
	go func() { <-ctx.Done(); _ = l.Close(); _ = os.Remove(path) }()
	wrapped := peerListener{Listener: l}
	return &limitedListener{Listener: wrapped, max: 64}, nil
}
