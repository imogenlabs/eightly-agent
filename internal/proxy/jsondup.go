// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
)

// errDuplicateKey is a JSON object that names one key twice. Go's decoder
// (the daemon's as well as ours) matches field names without regard to case
// and keeps the last value, so {"Privileged":true,"privileged":null} is one
// field set twice. Our types and the daemon's need not treat the second value
// alike (a null leaves a bool as it was, but clears a pointer), so the two
// readings can differ. A body that says one thing twice is refused.
var errDuplicateKey = errors.New("duplicate key")

// checkDuplicateKeys walks body and returns an error when any object, at any
// depth, holds two keys that Go's decoder would treat as the same field.
func checkDuplicateKeys(body []byte) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := walkValue(dec); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing data after the JSON value")
	}
	return nil
}

func walkValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch tok {
	case json.Delim('{'):
		seen := map[string]struct{}{}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return err
			}
			key, _ := k.(string)
			fold := foldKey(key)
			if _, dup := seen[fold]; dup {
				return errors.Join(errDuplicateKey, errors.New(strings.ToValidUTF8(key, "?")))
			}
			seen[fold] = struct{}{}
			if err := walkValue(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token() // '}'
		return err
	case json.Delim('['):
		for dec.More() {
			if err := walkValue(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token() // ']'
		return err
	}
	return nil
}

// foldKey maps every key that strings.EqualFold treats as equal, which is
// how encoding/json matches field names, to the same string: each rune is
// replaced by the smallest rune in its case-folding orbit (so the Kelvin sign
// and "k" fold alike, as they do for EqualFold).
func foldKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		low := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < low {
				low = f
			}
		}
		b.WriteRune(low)
	}
	return b.String()
}
