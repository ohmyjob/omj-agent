//go:build !linux

package discovery

// Other platforms are additive work (PRD §16.8): until their crontab layout is
// added, a discovery there finds no crontabs and reports whatever systemd it
// can reach, which on those platforms is nothing.
func DefaultPaths() Paths {
	return Paths{}
}
