//go:build !windows

// Native windows are a Windows-only feature (WebView2). Other platforms use
// the Edge/Chrome --app window fallback or the default browser.
package main

// runNativeWindow always reports failure off Windows so the caller falls back.
func runNativeWindow(_, _ string) bool { return false }
