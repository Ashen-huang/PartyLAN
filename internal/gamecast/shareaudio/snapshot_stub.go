//go:build !windows

package shareaudio

func collectSnapshot() (Snapshot, error) {
	return Snapshot{}, ErrSnapshotUnavailable
}
