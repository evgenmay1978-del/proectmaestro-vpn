//go:build !linux

package runtimefence

import "errors"

func ReadLeaseClock() (string, int64, error) {
	return "", 0, errors.New("managed runtime lease clock requires Linux BOOTTIME")
}

func readBoottime() (int64, error) {
	return 0, errors.New("managed runtime lease clock requires Linux BOOTTIME")
}
