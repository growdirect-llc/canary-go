package transaction

import "encoding/json"

// jsonMarshalImpl is the underlying encoding for jsonbBytes; isolated
// here to keep the encoding/json import out of store.go's main
// surface.
func jsonMarshalImpl(v any) ([]byte, error) {
	return json.Marshal(v)
}
