//go:build !darwin

package main

// runApp has nothing to run off macOS; Register has already reported
// ErrUnsupported by the time anything reaches here.
func runApp() {}
