package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// checkSchema validates one schemas/<name>.json file: it must be well-formed
// JSON and a valid JSON Schema (draft 2020-12).
func checkSchema(file string, data []byte) Errors {
	var probe any
	if err := json.Unmarshal(data, &probe); err != nil {
		line := 1
		var se *json.SyntaxError
		if errors.As(err, &se) {
			line = 1 + bytes.Count(data[:min(int(se.Offset), len(data))], []byte("\n"))
		}
		return Errors{{File: file, Line: line, Msg: "invalid JSON: " + err.Error()}}
	}
	// JSON is YAML, so the YAML indexer maps schema locations to lines.
	src, errs := decode(file, data, 0, &probe)
	if errs != nil {
		return errs
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return Errors{{File: file, Line: 1, Msg: err.Error()}}
	}
	c := jsonschema.NewCompiler()
	const url = "mem:///schema.json"
	if err := c.AddResource(url, doc); err != nil {
		return Errors{{File: file, Line: 1, Msg: err.Error()}}
	}
	if _, err := c.Compile(url); err != nil {
		path, msg := schemaProblem(err)
		var out Errors
		src.report(&out).add(path, msg)
		return out
	}
	return nil
}

// schemaLeaf matches the last line of a compile error, which names the
// deepest failing location: "- at '/properties/a/minimum': got string, want number".
var schemaLeaf = regexp.MustCompile(`- at '([^']*)': ([^\n]*)\s*$`)

// schemaProblem extracts the location (as a dotted path) and a short message
// from a schema compile error.
func schemaProblem(err error) (path, msg string) {
	if m := schemaLeaf.FindStringSubmatch(err.Error()); m != nil {
		return strings.ReplaceAll(strings.TrimPrefix(m[1], "/"), "/", "."), "invalid JSON Schema: " + m[2]
	}
	return "", "invalid JSON Schema: " + err.Error()
}
