package main

// A small JSON object that keeps its keys in order, so the manifest can be
// rewritten without reordering it: Claude Desktop shows the settings in the
// order the manifest lists them, and `-check` compares the text.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// object is a JSON object with ordered keys. Values are *object, []any,
// string, json.Number, bool or nil.
type object struct {
	keys []string
	vals map[string]any
}

// newObject builds an object from alternating keys and values.
func newObject(kv ...any) *object {
	o := &object{vals: map[string]any{}}
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

func (o *object) get(key string) any { return o.vals[key] }

func (o *object) set(key string, v any) *object {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
	return o
}

// path follows nested objects, returning nil if any step is missing.
func (o *object) path(keys ...string) any {
	var v any = o
	for _, k := range keys {
		obj, ok := v.(*object)
		if !ok || obj == nil {
			return nil
		}
		v = obj.get(k)
	}
	return v
}

func parseJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON value")
	}
	return v, nil
}

func parseValue(dec *json.Decoder) (any, error) {
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
				k, err := dec.Token()
				if err != nil {
					return nil, err
				}
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				o.set(k.(string), v)
			}
			_, err := dec.Token()
			return o, err
		case '[':
			a := []any{}
			for dec.More() {
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			_, err := dec.Token()
			return a, err
		}
		return nil, fmt.Errorf("unexpected %v", t)
	}
	return tok, nil
}

// stringify writes v indented by two spaces, like JSON.stringify(v, null, 2).
func stringify(v any) string {
	var b strings.Builder
	writeValue(&b, v, "")
	return b.String()
}

func writeValue(b *strings.Builder, v any, indent string) {
	inner := indent + "  "
	switch x := v.(type) {
	case *object:
		if len(x.keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, k := range x.keys {
			b.WriteString(inner)
			b.WriteString(quote(k))
			b.WriteString(": ")
			writeValue(b, x.vals[k], inner)
			if i < len(x.keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + "}")
	case []any:
		if len(x) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, e := range x {
			b.WriteString(inner)
			writeValue(b, e, inner)
			if i < len(x)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + "]")
	case []string:
		a := make([]any, len(x))
		for i, s := range x {
			a[i] = s
		}
		writeValue(b, a, indent)
	case string:
		b.WriteString(quote(x))
	case json.Number:
		b.WriteString(x.String())
	default:
		out, err := json.Marshal(x)
		if err != nil {
			panic(err)
		}
		b.Write(out)
	}
}

// quote encodes a string without escaping <, > and &, as JSON.stringify does.
func quote(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		panic(err)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}
