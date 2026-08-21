package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"
)

func main() {
	releaseDir := flag.String("release-dir", "", "candidate release directory")
	flag.Parse()
	if flag.NArg() != 0 || release.ValidateReleaseDirectory(*releaseDir) != nil {
		fmt.Fprintln(os.Stderr, "release validation failed")
		os.Exit(1)
	}
	fmt.Println("release validation passed")
}
