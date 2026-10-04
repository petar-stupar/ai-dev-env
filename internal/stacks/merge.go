package stacks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// The ordered JSON model: *jsonObject, []any, string, json.Number, bool, nil.

// jsonObject is a JSON object that remembers the order keys were first seen.
type jsonObject struct {
	keys []string
	vals map[string]any
}

func newObject() *jsonObject { return &jsonObject{vals: map[string]any{}} }

func (o *jsonObject) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

// MergeJSON deep-merges JSON documents in order, keeping key order: objects
// merge recursively, arrays concatenate with duplicates removed, and a scalar
// that differs between documents is an error. Output is indented with two spaces.
//
// Details: keys appear in first-seen order across the documents; array
// elements are compared by canonical JSON text (object keys sorted, compact),
// so duplicates inside one document are dropped too; a null never overrides
// a value, and a value replaces an earlier null; a type mismatch (object vs
// array vs scalar) is an error like a scalar conflict. No documents yields {}.
// HTML characters are not escaped. The output ends with a newline.
func MergeJSON(docs ...[]byte) ([]byte, error) {
	var acc any = newObject()
	for i, d := range docs {
		v, err := decodeOrdered(d)
		if err != nil {
			return nil, fmt.Errorf("document %d: %w", i+1, err)
		}
		if i == 0 {
			acc = v
			continue
		}
		acc, err = mergeValues(acc, v, "")
		if err != nil {
			return nil, fmt.Errorf("document %d: %w", i+1, err)
		}
	}
	var buf bytes.Buffer
	if err := encodeOrdered(&buf, acc, ""); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

func decodeOrdered(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("empty document")
		}
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data after JSON value")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := newObject()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k := kt.(string)
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				if old, ok := o.vals[k]; ok {
					// A repeated key within one object merges like a later document.
					if v, err = mergeValues(old, v, k); err != nil {
						return nil, err
					}
				}
				o.set(k, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return o, nil
		case '[':
			a := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return mergeArrays(nil, a), nil // drop duplicates within the document
		}
		return nil, fmt.Errorf("unexpected delimiter %v", t)
	default:
		return tok, nil // string, json.Number, bool, nil
	}
}

func mergeValues(dst, src any, path string) (any, error) {
	if src == nil {
		return dst, nil
	}
	if dst == nil {
		return src, nil
	}
	switch d := dst.(type) {
	case *jsonObject:
		s, ok := src.(*jsonObject)
		if !ok {
			return nil, conflict(path, dst, src)
		}
		for _, k := range s.keys {
			sv := s.vals[k]
			if dv, ok := d.vals[k]; ok {
				mv, err := mergeValues(dv, sv, joinPath(path, k))
				if err != nil {
					return nil, err
				}
				d.vals[k] = mv
			} else {
				d.set(k, sv)
			}
		}
		return d, nil
	case []any:
		s, ok := src.([]any)
		if !ok {
			return nil, conflict(path, dst, src)
		}
		return mergeArrays(d, s), nil
	default:
		if _, ok := src.(*jsonObject); ok {
			return nil, conflict(path, dst, src)
		}
		if _, ok := src.([]any); ok {
			return nil, conflict(path, dst, src)
		}
		if canonical(dst) != canonical(src) {
			return nil, conflict(path, dst, src)
		}
		return dst, nil
	}
}

func mergeArrays(dst, src []any) []any {
	seen := map[string]bool{}
	out := make([]any, 0, len(dst)+len(src))
	for _, v := range append(append([]any(nil), dst...), src...) {
		c := canonical(v)
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, v)
	}
	return out
}

func conflict(path string, a, b any) error {
	if path == "" {
		path = "(root)"
	}
	return fmt.Errorf("conflicting values at %s: %s vs %s", path, canonical(a), canonical(b))
}

// joinPath builds a readable key path such as permissions.allow or ["a.b"].
func joinPath(path, key string) string {
	seg := key
	if key == "" || strings.ContainsAny(key, ".[]\" ") {
		seg = "[" + strconv.Quote(key) + "]"
		return path + seg
	}
	if path == "" {
		return seg
	}
	return path + "." + seg
}

// canonical renders v compactly with object keys sorted.
func canonical(v any) string {
	var b bytes.Buffer
	writeCanonical(&b, v)
	return b.String()
}

func writeCanonical(b *bytes.Buffer, v any) {
	switch t := v.(type) {
	case *jsonObject:
		keys := append([]string(nil), t.keys...)
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(encodeString(k))
			b.WriteByte(':')
			writeCanonical(b, t.vals[k])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			writeCanonical(b, e)
		}
		b.WriteByte(']')
	default:
		b.Write(encodeScalar(v))
	}
}

func encodeString(s string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // strings always encode
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

func encodeScalar(v any) []byte {
	switch t := v.(type) {
	case nil:
		return []byte("null")
	case bool:
		return []byte(strconv.FormatBool(t))
	case json.Number:
		return []byte(t.String())
	case string:
		return encodeString(t)
	}
	panic(fmt.Sprintf("stacks: unexpected JSON value %T", v))
}

func encodeOrdered(b *bytes.Buffer, v any, indent string) error {
	const step = "  "
	switch t := v.(type) {
	case *jsonObject:
		if len(t.keys) == 0 {
			b.WriteString("{}")
			return nil
		}
		b.WriteString("{\n")
		for i, k := range t.keys {
			b.WriteString(indent + step)
			b.Write(encodeString(k))
			b.WriteString(": ")
			if err := encodeOrdered(b, t.vals[k], indent+step); err != nil {
				return err
			}
			if i < len(t.keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + "}")
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return nil
		}
		b.WriteString("[\n")
		for i, e := range t {
			b.WriteString(indent + step)
			if err := encodeOrdered(b, e, indent+step); err != nil {
				return err
			}
			if i < len(t)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + "]")
	default:
		b.Write(encodeScalar(v))
	}
	return nil
}
