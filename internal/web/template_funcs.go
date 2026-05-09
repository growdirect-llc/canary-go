package web

import (
	"fmt"
	"html/template"
)

// componentFuncs is the FuncMap registered when loading template sets that
// reference components (under templates/components/). It exposes a small
// fixed vocabulary that components and their consumers may rely on.
//
// Keep this map minimal. Adding a func here grows the public API of every
// component template and makes future template-engine swaps harder.
var componentFuncs = template.FuncMap{
	// dict builds a map[string]any from a flat sequence of key/value pairs.
	// Used by component callers to pass named params:
	//
	//   {{template "components/form-field" (dict "name" "email" "label" "Email")}}
	//
	// Keys must be strings. Returns an error to the template (rendered as
	// the empty string with the err propagated by Execute) if the args are
	// malformed — fail loud, not silent.
	"dict": func(args ...any) (map[string]any, error) {
		if len(args)%2 != 0 {
			return nil, fmt.Errorf("dict requires even number of args, got %d", len(args))
		}
		m := make(map[string]any, len(args)/2)
		for i := 0; i < len(args); i += 2 {
			key, ok := args[i].(string)
			if !ok {
				return nil, fmt.Errorf("dict key at position %d must be string, got %T", i, args[i])
			}
			m[key] = args[i+1]
		}
		return m, nil
	},
}
