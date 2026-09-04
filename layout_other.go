// Copyright (c) the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause licence that can be
// found in the LICENSE file.

//go:build !darwin

package hotkey

// charFor has no keyboard layout service to ask here, so it answers nothing and
// every caller falls back to the ANSI name.
//
// Not a guess: a wrong character is worse than none. A report saying "Equal" is
// a name a person can look up; one saying "=" on a keyboard where that key
// prints something else is the defect this exists to fix.
func charFor(Key) string { return "" }
