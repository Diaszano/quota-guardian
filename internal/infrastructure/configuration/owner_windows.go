//go:build windows

package configuration

import "os"

func ownedByCurrentUser(os.FileInfo) bool { return true }
