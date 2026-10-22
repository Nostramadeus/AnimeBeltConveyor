// Package player reports which media-player windows are open, by window title.
// The watched-time accumulator lives in sched; this package only looks.
package player

import "strings"

// Playing is one visible player window.
type Playing struct {
	Title string `json:"title"`
}

// Available reports whether window detection works on this OS.
const Available = available

// Current returns the visible top-level windows whose title contains any of
// filters (case-insensitive). Empty filters return nothing. On systems without
// window detection it returns nil.
func Current(filters []string) []Playing {
	var lower []string
	for _, f := range filters {
		if f = strings.ToLower(strings.TrimSpace(f)); f != "" {
			lower = append(lower, f)
		}
	}
	if len(lower) == 0 {
		return nil
	}
	var out []Playing
	for _, t := range windowTitles() {
		lt := strings.ToLower(t)
		for _, f := range lower {
			if strings.Contains(lt, f) {
				out = append(out, Playing{Title: t})
				break
			}
		}
	}
	return out
}
