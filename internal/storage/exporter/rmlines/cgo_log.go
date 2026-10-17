//go:build cgo && rmlines

package rmlines

// This file holds the only //export: a preamble in a file with exports must
// contain declarations only, so the C shims live in worker_cgo.go.

/*
#include <stddef.h>
*/
import "C"

import (
	"strings"
	"sync"
)

const (
	logInfo  = 0
	logDebug = 1
	logError = 2
)

var (
	logMu  sync.Mutex
	logBuf strings.Builder
)

// rmlGoLog receives the library's log messages. The string is valid only
// during the call; the library may call it from any thread.
//
//export rmlGoLog
func rmlGoLog(level C.int, msg *C.char) {
	if msg == nil || level == logDebug {
		return
	}
	s := strings.TrimSpace(C.GoString(msg))
	logMu.Lock()
	defer logMu.Unlock()
	if logBuf.Len() >= maxLogBytes {
		return
	}
	if logBuf.Len() > 0 {
		logBuf.WriteString("\n")
	}
	if level == logError {
		logBuf.WriteString("error: ")
	}
	if room := maxLogBytes - logBuf.Len(); len(s) > room {
		s = s[:room] + "..."
	}
	logBuf.WriteString(s)
}

// takeLog returns and clears the messages logged since the last call.
func takeLog() string {
	logMu.Lock()
	defer logMu.Unlock()
	s := logBuf.String()
	logBuf.Reset()
	return s
}
