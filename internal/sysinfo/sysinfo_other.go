//go:build !linux

package sysinfo

import "errors"

var errUnsupportedPlatform = errors.New("uname is not available on this platform")

// Other platforms are additive work (PRD §16.8); until then the kernel and OS versions stay empty.
func uname() (sysname, release string, err error) {
	return "", "", errUnsupportedPlatform
}
