//go:build linux

package sysinfo

import "syscall"

func uname() (sysname, release string, err error) {
	var uts syscall.Utsname

	if err := syscall.Uname(&uts); err != nil {
		return "", "", err
	}

	return utsField(uts.Sysname[:]), utsField(uts.Release[:]), nil
}

// The Utsname fields are int8 on amd64 and uint8 on arm64.
func utsField[T int8 | uint8](field []T) string {
	text := make([]byte, 0, len(field))

	for _, c := range field {
		if c == 0 {
			break
		}

		text = append(text, byte(c))
	}

	return string(text)
}
