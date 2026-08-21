package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"
)

type reasoned interface{ ReasonCode() string }

func main() {
	releaseDir := flag.String("release-dir", "", "candidate release directory")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "release_validation_failed code=arguments_invalid")
		os.Exit(1)
	}
	if err := release.ValidateReleaseDirectory(*releaseDir); err != nil {
		code := "validation_failed"
		var value reasoned
		if errors.As(err, &value) {
			code = value.ReasonCode()
		}
		fmt.Fprintf(os.Stderr, "release_validation_failed code=%s\n", code)
		os.Exit(1)
	}
	fmt.Println("release_validation_passed")
}
