//go:build !darwin

package proc

import "errors"

// watchExit is implemented only for darwin, the one exercised platform.
// Elsewhere Run fails closed rather than supervise without an exit watcher.
func watchExit(pid int) (<-chan struct{}, <-chan struct{}, error) {
	return nil, nil, errors.New("no exit watcher for this platform")
}
