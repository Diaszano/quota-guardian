//go:build !linux

package ipc

import (
	"context"
	"errors"
	"net"
	"net/http"
)

func Listen(context.Context, string, http.Handler) (net.Listener, error) {
	return nil, errors.New("unsupported")
}
func authorizedUID(int) bool { return false }
