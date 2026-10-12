package carpool

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrControlTarget     = apperrors.BadRequest("CARPOOL_CONTROL_TARGET", "node target is not permitted")
	ErrControlTransport  = apperrors.ServiceUnavailable("CARPOOL_CONTROL_TRANSPORT", "node connection unavailable")
	ErrControlProtocol   = apperrors.ServiceUnavailable("CARPOOL_CONTROL_PROTOCOL", "node protocol is incompatible")
	ErrControlAuth       = apperrors.ServiceUnavailable("CARPOOL_CONTROL_AUTH", "node administrator credential rejected")
	ErrControlCompliance = apperrors.ServiceUnavailable("CARPOOL_CONTROL_COMPLIANCE", "node compliance review required")
)

const controlResponseLimit = 1 << 20
const controlPrefix = "/api/v1/admin/carpool-control/v1"

type ControlInfo struct {
	Protocol     int      `json:"protocol_version"`
	MainID       string   `json:"main_instance_id"`
	NodeID       string   `json:"node_id"`
	CarID        string   `json:"car_id"`
	InstanceID   string   `json:"node_instance_id"`
	Capabilities []string `json:"capabilities"`
}

type ControlClient struct {
	allowed map[string]struct{}
	http    *http.Client
}

func canonicalControlOrigin(raw string) (string, error) {
	if len(raw) > 512 || strings.ContainsAny(raw, "?#\\") || strings.TrimSpace(raw) != raw {
		return "", ErrControlTarget
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawPath != "" || u.Host == "" {
		return "", ErrControlTarget
	}
	host := strings.ToLower(u.Hostname())
	if _, err := netip.ParseAddr(host); err == nil || len(host) > 253 || !strings.Contains(host, ".") {
		return "", ErrControlTarget
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrControlTarget
		}
		for _, ch := range label {
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
				return "", ErrControlTarget
			}
		}
	}
	port := u.Port()
	if port == "" {
		if strings.Contains(u.Host, ":") {
			return "", ErrControlTarget
		}
	} else {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "", ErrControlTarget
		}
	}
	if port == "" || port == "443" {
		return "https://" + host, nil
	}
	return "https://" + net.JoinHostPort(host, port), nil
}

func NewControlClient(origins []string) (*ControlClient, error) {
	if len(origins) == 0 || len(origins) > 1000 {
		return nil, ErrControlTarget
	}
	allowed := make(map[string]struct{}, len(origins))
	for _, raw := range origins {
		origin, err := canonicalControlOrigin(raw)
		if err != nil {
			return nil, err
		}
		if _, exists := allowed[origin]; exists {
			return nil, ErrControlTarget
		}
		allowed[origin] = struct{}{}
	}
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	lookup := func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DisableKeepAlives:      true,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    3 * time.Second,
		ResponseHeaderTimeout:  5 * time.Second,
		MaxResponseHeaderBytes: 16 << 10,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return controlDial(ctx, network, address, lookup, dialer.DialContext)
		},
	}
	return &ControlClient{allowed: allowed, http: &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}, nil
}

func (c *ControlClient) origin(raw string) (string, error) {
	if c == nil {
		return "", ErrControlTarget
	}
	origin, err := canonicalControlOrigin(raw)
	if err != nil {
		return "", err
	}
	if _, ok := c.allowed[origin]; !ok {
		return "", ErrControlTarget
	}
	return origin, nil
}

var controlReservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("3fff::/20"),
}

var controlIPv6Global = netip.MustParsePrefix("2000::/3")

func controlPublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || (ip.Is6() && !controlIPv6Global.Contains(ip)) {
		return false
	}
	return !slices.ContainsFunc(controlReservedPrefixes, func(p netip.Prefix) bool { return p.Contains(ip) })
}

// Resolve once per connection, reject any unsafe answer, and dial numeric IPs only.
func controlDial(ctx context.Context, network, address string, lookup func(context.Context, string) ([]netip.Addr, error), dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrControlTarget
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil, ErrControlTransport
	}
	if len(ips) == 0 || len(ips) > 64 {
		return nil, ErrControlTarget
	}
	for _, ip := range ips {
		if !controlPublicIP(ip) {
			return nil, ErrControlTarget
		}
	}
	for _, ip := range ips {
		conn, err := dial(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if err != nil {
			continue
		}
		peer, ok := conn.RemoteAddr().(*net.TCPAddr)
		if ok {
			actual, valid := netip.AddrFromSlice(peer.IP)
			if valid && controlPublicIP(actual) && actual.Unmap() == ip.Unmap() && strconv.Itoa(peer.Port) == port {
				return conn, nil
			}
		}
		_ = conn.Close()
		return nil, ErrControlTarget
	}
	return nil, ErrControlTransport
}

func (c *ControlClient) Info(ctx context.Context, origin, credential string) (ControlInfo, error) {
	var result ControlInfo
	data, err := c.request(ctx, origin, credential, http.MethodGet, controlPrefix+"/info", nil)
	if err != nil {
		return result, err
	}
	if json.Unmarshal(data, &result) != nil || result.Protocol != 1 || !slices.Contains(result.Capabilities, "connection-check") {
		return ControlInfo{}, ErrControlProtocol
	}
	for _, id := range []string{result.MainID, result.NodeID, result.CarID, result.InstanceID} {
		if strings.TrimSpace(id) == "" || len(id) > 128 {
			return ControlInfo{}, ErrControlProtocol
		}
	}
	return result, nil
}

// Only typed methods in this package choose paths. Callers cannot proxy arbitrary URLs.
func (c *ControlClient) request(ctx context.Context, rawOrigin, credential, method, path string, body io.Reader) (json.RawMessage, error) {
	return c.requestBound(ctx, rawOrigin, credential, method, path, body, nil)
}

func (c *ControlClient) requestBound(ctx context.Context, rawOrigin, credential, method, path string, body io.Reader, target *ManagedTarget) (json.RawMessage, error) {
	return c.requestCommand(ctx, rawOrigin, credential, method, path, body, target, "")
}

func (c *ControlClient) requestCommand(ctx context.Context, rawOrigin, credential, method, path string, body io.Reader, target *ManagedTarget, operation string) (json.RawMessage, error) {
	origin, err := c.origin(rawOrigin)
	if err != nil {
		return nil, err
	}
	if !validControlCredential(credential) {
		return nil, ErrControlCredential
	}
	req, err := http.NewRequestWithContext(ctx, method, origin+path, body)
	if err != nil {
		return nil, ErrControlProtocol
	}
	req.Header.Set("x-api-key", credential)
	req.Header.Set("Accept", "application/json")
	if operation != "" {
		req.Header.Set("Idempotency-Key", operation)
	}
	if target != nil {
		req.Header.Set(ControlHeaderMain, target.MainID)
		req.Header.Set(ControlHeaderNode, target.NodeID)
		req.Header.Set(ControlHeaderCar, target.CarID)
		req.Header.Set(ControlHeaderInstance, target.InstanceID)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, ErrControlTarget) {
			return nil, ErrControlTarget
		}
		return nil, ErrControlTransport
	}
	defer func() { _ = res.Body.Close() }()
	switch {
	case res.StatusCode == 401 || (res.StatusCode == 403 && target == nil):
		return nil, ErrControlAuth
	case res.StatusCode == 423:
		return nil, ErrControlCompliance
	case res.StatusCode >= 300 && res.StatusCode < 400:
		return nil, ErrControlTarget
	case res.StatusCode >= 500 || res.StatusCode == 429:
		return nil, ErrControlTransport
	case res.StatusCode != http.StatusOK && (target == nil || (res.StatusCode != 400 && res.StatusCode != 403 && res.StatusCode != 404 && res.StatusCode != 409)):
		return nil, ErrControlProtocol
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, controlResponseLimit+1))
	if err != nil {
		return nil, ErrControlTransport
	}
	if len(data) > controlResponseLimit {
		return nil, ErrControlProtocol
	}
	if res.StatusCode != http.StatusOK {
		return nil, controlCommandError(res.StatusCode, data)
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Code == nil || *envelope.Code != 0 || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, ErrControlProtocol
	}
	return envelope.Data, nil
}

func controlCommandError(status int, data []byte) error {
	var envelope struct {
		Code   int    `json:"code"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(data, &envelope) == nil && envelope.Code == status {
		// Never forward remote messages or metadata, even for known error codes.
		switch {
		case status == 409 && envelope.Reason == "CARPOOL_KEY_GONE":
			return ErrManagedKeyGone
		case status == 409 && envelope.Reason == "CARPOOL_CONFLICT":
			return ErrMainConflict
		case status == 409 && envelope.Reason == "NODE_MEMBERSHIP_VERSION_CONFLICT":
			return ErrVersionConflict
		case status == 404 && envelope.Reason == "CARPOOL_NOT_FOUND":
			return ErrMainNotFound
		case status == 403 && envelope.Reason == "NODE_MEMBERSHIP_REQUIRED":
			return ErrRequired
		case status == 400 && envelope.Reason == "CARPOOL_CONTROL_TARGET":
			return ErrControlTarget
		case status == 400 && envelope.Reason == "CARPOOL_GRANT_WINDOW":
			return ErrControlGrantWindow
		}
	}
	if status == 403 {
		return ErrControlAuth
	}
	return ErrControlProtocol
}
