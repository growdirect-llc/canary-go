// internal/pagination/pagination.go
package pagination

import (
	"net/http"
	"strconv"
)

const defaultLimit = 50
const maxLimit = 200

type Params struct {
	Limit  int
	Offset int
}

func FromRequest(r *http.Request) Params {
	limit := defaultLimit
	offset := 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= maxLimit {
			limit = v
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}
	return Params{Limit: limit, Offset: offset}
}
