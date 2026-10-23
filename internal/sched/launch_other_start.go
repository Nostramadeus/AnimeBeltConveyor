//go:build !windows

package sched

import "os/exec"

// OpenAt plays file startSec seconds in when a player program is configured
// and ABC knows its start flag (StartArgs). Without a configured player the
// desktop's default handler is used and the start cannot be passed, so the
// file opens like Open.
func (l osLauncher) OpenAt(playerPath, file string, startSec int) (<-chan struct{}, string, bool, error) {
	if startSec > 0 && playerPath != "" {
		if args, ok := startCommand(playerPath, file, startSec); ok {
			cmd := exec.Command(playerPath, args...)
			if err := cmd.Start(); err == nil {
				done := make(chan struct{})
				go func() { cmd.Wait(); close(done) }()
				return done, PlayerName(playerPath), true, nil
			}
		}
	}
	done, err := l.Open(playerPath, file)
	return done, PlayerName(playerPath), false, err
}
