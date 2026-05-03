package utils 

import "net/http"

// HeadersToMap: Converts the http.Header to a flat map[string]string.
func HeadersToMap(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, vals := range h {
		if len(vals) > 0 {
			m[k] = vals[0]
		}
	}
	return m
}