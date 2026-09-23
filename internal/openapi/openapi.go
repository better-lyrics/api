// Package openapi owns the hand-written OpenAPI document for the public API.
package openapi

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"

	"go.yaml.in/yaml/v3"
)

//go:embed openapi.yaml
var Spec []byte

var specJSON = mustJSON(Spec)

func mustJSON(src []byte) []byte {
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		panic("openapi: parse openapi.yaml: " + err.Error())
	}
	var buf bytes.Buffer
	if err := writeJSON(&buf, &root); err != nil {
		panic("openapi: encode openapi.yaml as JSON: " + err.Error())
	}
	return buf.Bytes()
}

// writeJSON walks the YAML node tree instead of decoding into a map, so the JSON
// keeps the document's key order and generated docs follow it.
func writeJSON(buf *bytes.Buffer, n *yaml.Node) error {
	switch n.Kind {
	case yaml.DocumentNode:
		return writeJSON(buf, n.Content[0])
	case yaml.AliasNode:
		return writeJSON(buf, n.Alias)
	case yaml.MappingNode:
		buf.WriteByte('{')
		for i := 0; i < len(n.Content); i += 2 {
			if i > 0 {
				buf.WriteByte(',')
			}
			key, err := json.Marshal(n.Content[i].Value)
			if err != nil {
				return err
			}
			buf.Write(key)
			buf.WriteByte(':')
			if err := writeJSON(buf, n.Content[i+1]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case yaml.SequenceNode:
		buf.WriteByte('[')
		for i, item := range n.Content {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeJSON(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case yaml.ScalarNode:
		var v any
		if err := n.Decode(&v); err != nil {
			return err
		}
		out, err := json.Marshal(v)
		if err != nil {
			return err
		}
		buf.Write(out)
	default:
		return fmt.Errorf("unsupported YAML node kind %d at line %d", n.Kind, n.Line)
	}
	return nil
}

// Handler serves the document as JSON.
func Handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write(specJSON)
}
