//go:build android

package main

/*
#include "bridge.h"
*/
import "C"

import "unsafe"

//export MaestroXhttpStart
func MaestroXhttpStart(id C.int64_t, data *C.char, length C.int) C.int {
	if data == nil || length <= 0 || length > maxPayloadBytes {
		return C.int(statusInvalidInput)
	}
	raw := C.GoBytes(unsafe.Pointer(data), length)
	return C.int(liveEngine.Start(int64(id), raw, func(sessionID int64, fd int) bool {
		return C.MaestroXhttpProtect(C.int64_t(sessionID), C.int(fd)) == 1
	}))
}

//export MaestroXhttpStop
func MaestroXhttpStop(id C.int64_t) C.int {
	return C.int(liveEngine.Stop(int64(id)))
}
