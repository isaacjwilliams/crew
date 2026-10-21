package main

// Settings files crew rewrites or merges keep their keys in order, so what crew
// writes reads like the file it came from, as jq's output did. A parsed value is
// an *object, a []any, or a json.RawMessage for anything else, kept as written.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

type object struct {
	keys []string
	vals map[string]any
}

func newObject() *object { return &object{vals: map[string]any{}} }

func (o *object) get(k string) (any, bool) {
	v, ok := o.vals[k]
	return v, ok
}

// set adds k at the end, or replaces its value where it is.
func (o *object) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, key := range o.keys {
		if key == k {
			o.keys = append(o.keys[:i:i], o.keys[i+1:]...)
			break
		}
	}
}

// child is the object at k, made if it's missing or isn't an object.
func (o *object) child(k string) *object {
	if v, ok := o.vals[k].(*object); ok {
		return v
	}
	c := newObject()
	o.set(k, c)
	return c
}

func parseJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, fmt.Errorf("more than one JSON value")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) > 0 && raw[0] == '{':
		inner := json.NewDecoder(bytes.NewReader(raw))
		inner.UseNumber()
		inner.Token()
		o := newObject()
		for inner.More() {
			t, err := inner.Token()
			if err != nil {
				return nil, err
			}
			v, err := decodeValue(inner)
			if err != nil {
				return nil, err
			}
			o.set(t.(string), v)
		}
		return o, nil
	case len(raw) > 0 && raw[0] == '[':
		inner := json.NewDecoder(bytes.NewReader(raw))
		inner.UseNumber()
		inner.Token()
		arr := []any{}
		for inner.More() {
			v, err := decodeValue(inner)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		return arr, nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, err
	}
	return json.RawMessage(compact.Bytes()), nil
}

// encodeJSON writes v compactly, or indented by two spaces as jq does, with a
// final newline.
func encodeJSON(v any, indent bool) string {
	var b strings.Builder
	writeJSON(&b, v, indent, "")
	if indent {
		b.WriteString("\n")
	}
	return b.String()
}

func writeJSON(b *strings.Builder, v any, indent bool, prefix string) {
	nl, inner, colon := "", "", ":"
	if indent {
		nl, inner, colon = "\n", prefix+"  ", ": "
	}
	switch v := v.(type) {
	case *object:
		if len(v.keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{")
		for i, k := range v.keys {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(nl + inner + jsonString(k) + colon)
			writeJSON(b, v.vals[k], indent, inner)
		}
		b.WriteString(nl + prefix + "}")
	case []any:
		if len(v) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[")
		for i, e := range v {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(nl + inner)
			writeJSON(b, e, indent, inner)
		}
		b.WriteString(nl + prefix + "]")
	case json.RawMessage:
		b.Write(v)
	case string:
		b.WriteString(jsonString(v))
	}
}

// jsonString is s as a JSON string, without the HTML escaping encoding/json does.
func jsonString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// addOnly merges extra into base without taking anything away: objects merge,
// arrays gain the entries they lack, and any other value base has wins.
func addOnly(base, extra any) any {
	switch b := base.(type) {
	case *object:
		e, ok := extra.(*object)
		if !ok {
			return base
		}
		out := newObject()
		for _, k := range b.keys {
			out.set(k, b.vals[k])
		}
		for _, k := range e.keys {
			if bv, ok := b.vals[k]; ok {
				out.set(k, addOnly(bv, e.vals[k]))
			} else {
				out.set(k, e.vals[k])
			}
		}
		return out
	case []any:
		e, ok := extra.([]any)
		if !ok {
			return base
		}
		out := append([]any{}, b...)
		for _, x := range e {
			if !containsJSON(b, x) {
				out = append(out, x)
			}
		}
		return out
	}
	return base
}

func containsJSON(list []any, x any) bool {
	for _, y := range list {
		if equalJSON(x, y) {
			return true
		}
	}
	return false
}

func equalJSON(a, b any) bool {
	var x, y any
	json.Unmarshal([]byte(encodeJSON(a, false)), &x)
	json.Unmarshal([]byte(encodeJSON(b, false)), &y)
	return reflect.DeepEqual(x, y)
}
