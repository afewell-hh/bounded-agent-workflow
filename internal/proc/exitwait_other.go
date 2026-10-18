//go:build !darwin || !arm64

package proc

// proveExited has no implementation outside darwin/arm64, the one platform
// whose siginfo layout and waitid call are pinned. It fails closed, so a
// registration error is returned unchanged.
func proveExited(pid int) bool {
	return false
}
