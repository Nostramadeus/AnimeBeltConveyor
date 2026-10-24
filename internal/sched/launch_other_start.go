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

func (l osLauncher) OpenPlayback(playerPath, file string, start int, options SubtitleOptions) (<-chan struct{}, string, bool, bool, string, error) {
	if playerPath == "" || !options.Requested && !smplayerName(playerPath) {
		done, player, skipped, err := l.OpenAt(playerPath, file, start)
		return done, player, skipped, false, "", err
	}
	command := playbackArgs(playerPath, file, start, options)
	cmd := exec.Command(playerPath, command.args...)
	if err := cmd.Start(); err != nil {
		command.cleanup()
		return nil, PlayerName(playerPath), false, false, command.note, err
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); command.cleanup(); close(done) }()
	return done, PlayerName(playerPath), command.skipped, command.subtitles, command.note, nil
}
