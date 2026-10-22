package main

import "testing"

// setHome points the home directory at dir. os.UserHomeDir reads USERPROFILE on
// Windows, so a test that sets only HOME would act on the real home folder there.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}
