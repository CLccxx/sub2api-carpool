//go:build integration

package carpool

import "net/http"

// ConfigureTestTransport is compiled only into the test binary. The production
// client has no option for bypassing its public-address/TLS policy.
func ConfigureTestTransport(store *ControlStore, transport http.RoundTripper) {
	store.client.http.Transport = transport
}
