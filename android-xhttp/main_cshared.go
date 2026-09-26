//go:build !xhttpcli

package main

// The C-shared (JNI) build has no executable entry point; the exports live in
// native_android.go. The child-process build replaces this with main_cli.go.
func main() {}
