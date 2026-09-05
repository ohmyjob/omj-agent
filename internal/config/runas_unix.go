//go:build unix

package config

import (
	"fmt"
	"os/user"
	"strconv"
)

// LookupUser answers with the numeric uid so that the allowlist recognises
// root even under another name.
func LookupUser(name string) (int, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, err
	}

	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, fmt.Errorf("uid of %s: %w", name, err)
	}

	return uid, nil
}
