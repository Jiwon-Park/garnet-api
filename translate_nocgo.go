//go:build !cgo

package main

import "errors"

// Without cgo the hdTrans engine cannot be linked, so /translate answers 503.
// The key-value routes are unaffected.
type translator struct{}

func newTranslator(int) (*translator, error) {
	return nil, errors.New("built without cgo: translation disabled")
}

func (t *translator) Version() string { return "" }

func (t *translator) Translate(string, string) (string, error) {
	return "", errTranslatorUnavailable
}

func closeTranslator() {}
