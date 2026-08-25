//go:build darwin

package main

import "github.com/go-macos/objc"

// runApp enters the AppKit run loop, which is what delivers hot-key presses.
// Accessory policy: no Dock icon, no menu bar. The hot key is global anyway.
func runApp() { objc.RunApp(1) }
