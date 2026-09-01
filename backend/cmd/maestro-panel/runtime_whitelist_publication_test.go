package main
import "testing"
func TestRuntimeWhiteListPublicationDefaultsOff(t *testing.T) { if runtimeWhiteListPublicationSource() != nil { t.Fatal("publication source enabled by default") } }
