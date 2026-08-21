//go:build linux

package release

// TaskCReplacePreIntentStagingSyncForTest is a test-only bridge for the narrow
// unexported activationSyncStagingBeforeIntent seam. Its production default must
// delegate to root.activationSyncSealedRelease before transaction.json is created.
func TaskCReplacePreIntentStagingSyncForTest(replacement func(string) error) func() {
	previous := activationSyncStagingBeforeIntent
	activationSyncStagingBeforeIntent = func(_ *activationLockedRoot, stagingName string) error {
		return replacement(stagingName)
	}
	return func() {
		activationSyncStagingBeforeIntent = previous
	}
}
