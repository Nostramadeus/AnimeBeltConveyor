//go:build !windows

package player

const available = false

// windowTitles is not implemented outside Windows (see BACKLOG player-linux-mac).
func windowTitles() []string { return nil }
