// Package outbound provides the shared network boundary for external HTTP providers.
package outbound

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// ErrRestrictedAddress reports that a provider resolved to an address which
// must not be reachable from application-controlled outbound requests.
var ErrRestrictedAddress = errors.New("outbound: restricted destination address")

type resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

// Transport returns a clone of the standard HTTP transport with connection-time
// DNS validation. Proxies are intentionally disabled because they could resolve
// the unchecked provider hostname outside this trust boundary.
func Transport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{}
	transport.DialContext = restrictedDialContext(net.DefaultResolver, dialer.DialContext)
	return transport
}

// InternalServiceTransport returns a transport that can reach exactly one
// private service endpoint. It is intentionally separate from Transport so an
// internal Compose service cannot weaken the boundary for external providers.
func InternalServiceTransport(host, port string) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{}
	transport.DialContext = internalServiceDialContext(net.DefaultResolver, dialer.DialContext, host, port)
	return transport
}

// TailscaleServiceTransport returns a transport that can reach exactly one
// numeric Tailscale IPv4 endpoint. It deliberately avoids DNS and proxy
// resolution so the configured peer cannot be changed at request time.
func TailscaleServiceTransport(host, port string) (*http.Transport, error) {
	address, err := netip.ParseAddr(host)
	if err != nil || !address.Is4() || !tailscaleIPv4Prefix.Contains(address) || port != "5000" {
		return nil, fmt.Errorf("%w: tailscale service endpoint", ErrRestrictedAddress)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{}
	transport.DialContext = tailscaleServiceDialContext(dialer.DialContext, address, port)
	return transport, nil
}

// DialContext returns a connection-time DNS validating dialer for non-HTTP
// protocols such as SMTP.
func DialContext(timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	return restrictedDialContext(net.DefaultResolver, dialer.DialContext)
}

func restrictedDialContext(nameResolver resolver, dial dialContextFunc) dialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("outbound: invalid destination: %w", err)
		}
		if host == "" || port == "" {
			return nil, errors.New("outbound: invalid destination")
		}
		addresses, err := resolve(ctx, nameResolver, host)
		if err != nil {
			return nil, err
		}
		for _, candidate := range addresses {
			if restricted(candidate) {
				return nil, fmt.Errorf("%w: %s", ErrRestrictedAddress, host)
			}
		}

		var dialErr error
		for _, candidate := range addresses {
			connection, err := dial(ctx, network, net.JoinHostPort(candidate.String(), port))
			if err == nil {
				return connection, nil
			}
			dialErr = errors.Join(dialErr, err)
		}
		return nil, fmt.Errorf("outbound: connecting provider: %w", dialErr)
	}
}

func internalServiceDialContext(nameResolver resolver, dial dialContextFunc, allowedHost, allowedPort string) dialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || !strings.EqualFold(host, allowedHost) || port != allowedPort {
			return nil, fmt.Errorf("%w: internal service endpoint", ErrRestrictedAddress)
		}
		addresses, err := resolve(ctx, nameResolver, host)
		if err != nil {
			return nil, err
		}
		for _, candidate := range addresses {
			if !candidate.IsValid() || !candidate.IsPrivate() || candidate.IsLoopback() ||
				candidate.IsUnspecified() || candidate.IsLinkLocalUnicast() ||
				candidate.IsLinkLocalMulticast() || candidate.IsMulticast() {
				return nil, fmt.Errorf("%w: internal service resolution", ErrRestrictedAddress)
			}
		}

		var dialErr error
		for _, candidate := range addresses {
			connection, err := dial(ctx, network, net.JoinHostPort(candidate.String(), port))
			if err == nil {
				return connection, nil
			}
			dialErr = errors.Join(dialErr, err)
		}
		return nil, fmt.Errorf("outbound: connecting internal service: %w", dialErr)
	}
}

func tailscaleServiceDialContext(dial dialContextFunc, allowedAddress netip.Addr, allowedPort string) dialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		candidate, parseErr := netip.ParseAddr(host)
		if err != nil || parseErr != nil || candidate != allowedAddress || port != allowedPort ||
			!candidate.Is4() || !tailscaleIPv4Prefix.Contains(candidate) {
			return nil, fmt.Errorf("%w: tailscale service endpoint", ErrRestrictedAddress)
		}
		connection, err := dial(ctx, network, net.JoinHostPort(allowedAddress.String(), allowedPort))
		if err != nil {
			return nil, fmt.Errorf("outbound: connecting tailscale service: %w", err)
		}
		return connection, nil
	}
}

func resolve(ctx context.Context, nameResolver resolver, host string) ([]netip.Addr, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{address.Unmap()}, nil
	}
	addresses, err := nameResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("outbound: resolving provider: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("outbound: provider resolved without addresses")
	}
	for index := range addresses {
		addresses[index] = addresses[index].Unmap()
	}
	return addresses, nil
}

func restricted(address netip.Addr) bool {
	return !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() ||
		address.IsLoopback() || address.IsUnspecified() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsMulticast() || specialUse(address)
}

func specialUse(address netip.Addr) bool {
	for _, prefix := range restrictedPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

var restrictedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("233.252.0.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("100:0:0:1::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
}

var tailscaleIPv4Prefix = netip.MustParsePrefix("100.64.0.0/10")
