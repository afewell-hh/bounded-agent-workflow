// Package claude holds the fixed Claude Code 2.1.285 worker profile: the
// strict profile manifest, the frozen launch argv and environment, the
// claude-2.1.285-source-v1 source matrix and the bounded stream-json parser.
// It launches nothing itself and reads no authentication material.
package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

var errStrict = errors.New("invalid json")

// StrictObject decodes exactly one UTF-8 JSON object with no duplicate decoded
// member names at any depth and only trailing whitespace. Numbers stay
// json.Number.
func StrictObject(data []byte) (map[string]any, bool) {
	v, ok := StrictValue(data)
	if !ok {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

// StrictValue is StrictObject for any single JSON value.
func StrictValue(data []byte) (any, bool) {
	if !utf8.Valid(data) {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := strictValue(dec)
	if err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return v, true
}

func strictValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, errStrict
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := map[string]any{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, errStrict
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errStrict
				}
				if _, dup := m[k]; dup {
					return nil, errStrict
				}
				v, err := strictValue(dec)
				if err != nil {
					return nil, errStrict
				}
				m[k] = v
			}
			if _, err := dec.Token(); err != nil {
				return nil, errStrict
			}
			return m, nil
		case '[':
			a := []any{}
			for dec.More() {
				v, err := strictValue(dec)
				if err != nil {
					return nil, errStrict
				}
				a = append(a, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, errStrict
			}
			return a, nil
		}
		return nil, errStrict
	default:
		return t, nil
	}
}

// ExactKeys reports whether m has exactly the given member names.
func ExactKeys(m map[string]any, keys ...string) bool {
	if len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

// IsOne reports whether v is the JSON integer literal 1.
func IsOne(v any) bool { n, ok := v.(json.Number); return ok && string(n) == "1" }

// Canonical encodes a decoded strict value deterministically: object members
// sorted by name, no insignificant whitespace, no HTML escaping, number
// literals unchanged. Object member order in the input does not matter;
// strings, list order and types do.
func Canonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
