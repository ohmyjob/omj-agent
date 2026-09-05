//go:build linux

package discovery

// DefaultPaths names where cron keeps its files on Linux. Both spool
// directories are listed because distributions disagree: Debian and Alpine
// keep user crontabs in crontabs/, Red Hat and Arch in the parent directory.
// Whichever one is not in use is either absent or holds the other as a
// subdirectory, and directories are skipped.
func DefaultPaths() Paths {
	return Paths{
		SystemCrontab: "/etc/crontab",
		CronDir:       "/etc/cron.d",
		SpoolDirs:     []string{"/var/spool/cron/crontabs", "/var/spool/cron"},
	}
}
