package service

import "net/http"

// MaxKeysBody exposes the keys reply cap to the external tests.
const MaxKeysBody = maxKeysBody

// UsesNoProxy reports whether c's transport has no proxy function.
func UsesNoProxy(c *Control) bool {
	tr, ok := c.client.Transport.(*http.Transport)
	return ok && tr.Proxy == nil
}
