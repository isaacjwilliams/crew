package main

// The settings files crew rewrites or merges keep their keys in order, so what
// crew writes reads like the file it came from: encoding/json's maps would sort
// them. A parsed value is an *object, a []any, a string, a json.Number, a bool,
// or nil.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("more than one JSON value")
	}
	return v, nil
}

// decodeValue reads the next value from dec, token by token.
func decodeValue(dec *json.Decoder) (any, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t {
	case json.Delim('{'):
		o := newObject()
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			o.set(k.(string), v) // inside an object, the decoder only returns string keys here
		}
		_, err := dec.Token() // }
		return o, err
	case json.Delim('['):
		arr := []any{}
		for dec.More() {
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		_, err := dec.Token() // ]
		return arr, err
	}
	return t, nil
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
	case string:
		b.WriteString(jsonString(v))
	case json.Number:
		b.WriteString(v.String())
	case bool:
		fmt.Fprint(b, v)
	case nil:
		b.WriteString("null")
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

// equalJSON compares two values as JSON does: an object's keys in any order, and
// numbers by value (1 is 1.0).
func equalJSON(a, b any) bool {
	switch x := a.(type) {
	case *object:
		y, ok := b.(*object)
		if !ok || len(x.keys) != len(y.keys) {
			return false
		}
		for _, k := range x.keys {
			if yv, ok := y.get(k); !ok || !equalJSON(x.vals[k], yv) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !equalJSON(x[i], y[i]) {
				return false
			}
		}
		return true
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		f, err1 := x.Float64()
		g, err2 := y.Float64()
		return err1 == nil && err2 == nil && f == g
	}
	return a == b
}
