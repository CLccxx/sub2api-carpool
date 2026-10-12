package carpool

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestControlOriginExactHTTPSAllowlist(t *testing.T) {
	client, err := NewControlClient([]string{"https://Node.Example", "https://second.example:8443"})
	require.NoError(t, err)
	for _, raw := range []string{"https://node.example", "https://NODE.example:443", "https://second.example:8443"} {
		_, err := client.origin(raw)
		require.NoError(t, err, raw)
	}
	for _, raw := range []string{
		"", "http://node.example", "https://node.example/", "https://node.example/path", "https://node.example?x=1", "https://node.example#x",
		"https://user:secret@node.example", "https://node.example:80", "https://evil.example", "https://node.example.evil.test",
		"https://node.example.", "https://127.0.0.1", "https://[::1]", "https://[2001:4860:4860::8888]", "https://node.example:0443",
		"https://node.example?", "https://node.example#", "https://node.example:", " https://node.example", "https://node.example\\@evil.example",
	} {
		_, err := client.origin(raw)
		require.ErrorIs(t, err, ErrControlTarget, raw)
	}
	for _, allowed := range [][]string{nil, {"*"}, {"http://node.example"}, {"https://node.example/path"}, {"https://node.example", "https://node.example:443"}} {
		_, err := NewControlClient(allowed)
		require.Error(t, err)
	}
}

func TestControlPublicAddresses(t *testing.T) {
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "2001:4860:4860::8888"} {
		require.True(t, controlPublicIP(netip.MustParseAddr(raw)), raw)
	}
	for _, raw := range []string{
		"0.0.0.0", "0.1.2.3", "10.0.0.1", "100.64.1.1", "127.0.0.1", "169.254.169.254", "172.16.0.1", "192.168.1.1",
		"192.0.0.8", "192.0.2.1", "192.88.99.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255",
		"::", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "ff02::1", "64:ff9b::a00:1", "2001:db8::1", "2002:a00:1::", "3fff::1", "2001::1",
	} {
		require.False(t, controlPublicIP(netip.MustParseAddr(raw)), raw)
	}
	require.False(t, controlPublicIP(netip.Addr{}))
}

type controlPeerConn struct {
	net.Conn
	peer net.Addr
}

func (c controlPeerConn) RemoteAddr() net.Addr { return c.peer }

func TestControlDialChecksAllDNSAnswersAndActualPeer(t *testing.T) {
	ctx := context.Background()
	var calls int
	dial := func(_ context.Context, _, address string) (net.Conn, error) {
		calls++
		require.Equal(t, "8.8.8.8:443", address)
		a, b := net.Pipe()
		t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
		return controlPeerConn{Conn: a, peer: &net.TCPAddr{IP: net.ParseIP("8.8.8.8"), Port: 443}}, nil
	}
	for _, ips := range [][]netip.Addr{
		nil,
		{netip.MustParseAddr("127.0.0.1")},
		{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("::1")},
		{netip.MustParseAddr("2606:4700:4700::1111"), netip.MustParseAddr("10.0.0.1")},
	} {
		lookup := func(context.Context, string) ([]netip.Addr, error) { return ips, nil }
		_, err := controlDial(ctx, "tcp", "node.example:443", lookup, dial)
		require.ErrorIs(t, err, ErrControlTarget)
	}
	require.Zero(t, calls, "a mixed answer must be rejected before any dial")
	lookup := func(_ context.Context, host string) ([]netip.Addr, error) {
		require.Equal(t, "node.example", host)
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	conn, err := controlDial(ctx, "tcp", "node.example:443", lookup, dial)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Equal(t, 1, calls)
	for _, peer := range []string{"127.0.0.1", "1.1.1.1"} {
		wrong := func(context.Context, string, string) (net.Conn, error) {
			a, b := net.Pipe()
			t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
			return controlPeerConn{Conn: a, peer: &net.TCPAddr{IP: net.ParseIP(peer), Port: 443}}, nil
		}
		_, err := controlDial(ctx, "tcp", "node.example:443", lookup, wrong)
		require.ErrorIs(t, err, ErrControlTarget)
	}
	var lookups int
	rebinding := func(context.Context, string) ([]netip.Addr, error) {
		lookups++
		if lookups == 1 {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
	}
	conn, err = controlDial(ctx, "tcp", "node.example:443", rebinding, dial)
	require.NoError(t, err)
	_ = conn.Close()
	_, err = controlDial(ctx, "tcp", "node.example:443", rebinding, dial)
	require.ErrorIs(t, err, ErrControlTarget)
}

func TestControlProductionTransport(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9999")
	client, err := NewControlClient([]string{"https://node.example"})
	require.NoError(t, err)
	transport, ok := client.http.Transport.(*http.Transport)
	require.True(t, ok)
	require.Nil(t, transport.Proxy)
	require.False(t, transport.TLSClientConfig.InsecureSkipVerify)
	require.NotZero(t, transport.TLSHandshakeTimeout)
	require.NotZero(t, transport.ResponseHeaderTimeout)
	require.True(t, transport.DisableKeepAlives)
	require.Positive(t, client.http.Timeout)
	require.ErrorIs(t, client.http.CheckRedirect(&http.Request{}, nil), http.ErrUseLastResponse)
}

func TestControlInfoBoundedTLSAndSanitizedErrors(t *testing.T) {
	var mode atomic.Int32
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		require.Equal(t, "/api/v1/admin/carpool-control/v1/info", r.URL.Path)
		require.Empty(t, r.URL.RawQuery)
		require.Equal(t, "synthetic-secret", r.Header.Get("x-api-key"))
		switch mode.Load() {
		case 1, 2, 3:
			status := map[int32]int{1: 401, 2: 403, 3: 423}[mode.Load()]
			w.WriteHeader(status)
			_, _ = io.WriteString(w, "untrusted synthetic-secret admin password")
		case 4:
			http.Redirect(w, r, "https://evil.example/secret", http.StatusFound)
		case 5:
			_, _ = io.WriteString(w, strings.Repeat("x", controlResponseLimit+1))
		case 6:
			_, _ = io.WriteString(w, `{"code":0,"data":{"protocol_version":999}}`)
		case 7:
			_, _ = io.WriteString(w, `{"code":0,"data":null}`)
		case 8:
			_, _ = io.WriteString(w, `{"code":0,"data":{}} {}`)
		case 9:
			<-r.Context().Done()
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": ControlInfo{Protocol: 1, MainID: "a", NodeID: "b", CarID: "car", InstanceID: "instance-b", Capabilities: []string{"connection-check"}}})
		}
	}))
	defer server.Close()
	// Only the test replaces dial: production has no localhost/SSRF bypass option.
	host := server.Certificate().DNSNames[0]
	origin := "https://" + host
	client, err := NewControlClient([]string{origin})
	require.NoError(t, err)
	transport, ok := client.http.Transport.(*http.Transport)
	require.True(t, ok)
	serverTransport, ok := server.Client().Transport.(*http.Transport)
	require.True(t, ok)
	transport.TLSClientConfig.RootCAs = serverTransport.TLSClientConfig.RootCAs
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	info, err := client.Info(context.Background(), origin, "synthetic-secret")
	require.NoError(t, err)
	require.Equal(t, "instance-b", info.InstanceID)
	for _, test := range []struct {
		mode int32
		want error
	}{{1, ErrControlAuth}, {2, ErrControlAuth}, {3, ErrControlCompliance}, {4, ErrControlTarget}, {5, ErrControlProtocol}, {6, ErrControlProtocol}, {7, ErrControlProtocol}, {8, ErrControlProtocol}} {
		mode.Store(test.mode)
		_, err = client.Info(context.Background(), origin, "synthetic-secret")
		require.ErrorIs(t, err, test.want)
		require.NotContains(t, err.Error(), "synthetic-secret")
		require.NotContains(t, err.Error(), "admin password")
		require.NotContains(t, err.Error(), "evil.example")
	}
	require.EqualValues(t, 9, requests.Load(), "redirect must not be followed")
	mode.Store(9)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = client.Info(ctx, origin, "synthetic-secret")
	require.ErrorIs(t, err, ErrControlTransport)
	mode.Store(0)
	transport.TLSClientConfig.RootCAs = nil
	_, err = client.Info(context.Background(), origin, "synthetic-secret")
	require.ErrorIs(t, err, ErrControlTransport, "untrusted TLS certificate must be refused")
	_, err = client.Info(context.Background(), origin, "secret\nInjected: header")
	require.ErrorIs(t, err, ErrControlCredential)
	_, err = client.Info(context.Background(), "https://evil.example", "synthetic-secret")
	require.ErrorIs(t, err, ErrControlTarget)
	require.False(t, errors.Is(err, ErrControlAuth))
	wrongHostname, err := NewControlClient([]string{"https://not-in-certificate.example"})
	require.NoError(t, err)
	wrongTransport, ok := wrongHostname.http.Transport.(*http.Transport)
	require.True(t, ok)
	wrongTransport.TLSClientConfig.RootCAs = serverTransport.TLSClientConfig.RootCAs
	wrongTransport.DialContext = transport.DialContext
	_, err = wrongHostname.Info(context.Background(), "https://not-in-certificate.example", "synthetic-secret")
	require.ErrorIs(t, err, ErrControlTransport, "trusting a CA must not bypass hostname verification")
}
