//go:build cgo

package main

/*
#cgo LDFLAGS: -lhdTransEngine -Wl,-rpath,$ORIGIN
#include <stdlib.h>
#include <stddef.h>

// C API of libhdTransEngine (hdTrans, include/core/hdtrans.h). Strings are UTF-8.
// Calls are thread-safe, not reentrant. The library loads once per process.
int hdTrans_Initialize(const char* dirs);
void hdTrans_Terminate(void);
int hdTrans_Translate(const char* text, size_t n, char** out, size_t* outn);
int hdTrans_TranslateContext(const char* context, size_t cn, const char* text, size_t n,
                             char** out, size_t* outn);
void hdTrans_FreeMem(void* p);
const char* hdTrans_Version(void);
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

// translator wraps the process-wide hdTrans engine. The library is loaded and
// initialized exactly once; requests then call into it concurrently. The semaphore
// bounds how many cgo calls run at the same time, so a burst of requests cannot
// occupy every OS thread.
type translator struct {
	sem chan struct{}
}

var (
	engineOnce sync.Once
	engineErr  error
)

// newTranslator initializes the engine (dictionaries come from hdtrans-data/ next to
// the library) and returns a translator that allows workers concurrent calls.
func newTranslator(workers int) (*translator, error) {
	engineOnce.Do(func() {
		if rc := C.hdTrans_Initialize(nil); rc != 0 {
			engineErr = fmt.Errorf("hdTrans_Initialize failed (code %d): check hdtrans-data/dicts.hdsnap next to libhdTransEngine.so", rc)
		}
	})
	if engineErr != nil {
		return nil, engineErr
	}
	if workers < 1 {
		workers = 1
	}
	return &translator{sem: make(chan struct{}, workers)}, nil
}

// Version reports the engine version string.
func (t *translator) Version() string {
	return C.GoString(C.hdTrans_Version())
}

// Translate translates text (one or more "\n"-separated lines). When contextText is
// non-empty it is passed as the preceding lines of the same document.
func (t *translator) Translate(contextText, text string) (string, error) {
	if t == nil {
		return "", errTranslatorUnavailable
	}
	t.sem <- struct{}{}
	defer func() { <-t.sem }()

	var out *C.char
	var outn C.size_t
	textBytes := []byte(text)
	textPtr := (*C.char)(unsafe.Pointer(unsafe.SliceData(textBytes)))

	var rc C.int
	if contextText == "" {
		rc = C.hdTrans_Translate(textPtr, C.size_t(len(textBytes)), &out, &outn)
	} else {
		ctxBytes := []byte(contextText)
		rc = C.hdTrans_TranslateContext(
			(*C.char)(unsafe.Pointer(unsafe.SliceData(ctxBytes))), C.size_t(len(ctxBytes)),
			textPtr, C.size_t(len(textBytes)),
			&out, &outn)
	}
	if rc != 0 {
		return "", errTranslateFailed
	}
	defer C.hdTrans_FreeMem(unsafe.Pointer(out))
	if out == nil {
		return "", errors.New("translation returned no buffer")
	}
	return C.GoStringN(out, C.int(outn)), nil
}

// closeTranslator releases the engine. Call only after all requests have finished.
func closeTranslator() {
	C.hdTrans_Terminate()
}
