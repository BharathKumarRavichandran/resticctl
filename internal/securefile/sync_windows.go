//go:build windows

package securefile

// replace uses MOVEFILE_WRITE_THROUGH on Windows, where directories cannot be
// opened and flushed with the same portable semantics as Unix directory fsync.
func syncParent(string) error { return nil }
