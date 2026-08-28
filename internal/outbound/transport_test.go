package outbound

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

type staticResolver struct {
	addresses []netip.Addr
}

func (resolver staticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), resolver.addresses...), nil
}

type failingResolver struct{ err error }

func (resolver failingResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return nil, resolver.err
}

func TestRestrictedDialContextRejectsPrivateAndSpecialUseAddressesBeforeDial(t *testing.T) {
	t.Parallel()
	for _, values := range [][]netip.Addr{
		{netip.MustParseAddr("127.0.0.1")},
		{netip.MustParseAddr("10.0.0.8")},
		{netip.MustParseAddr("169.254.169.254")},
		{netip.MustParseAddr("0.0.0.1")},
		{netip.MustParseAddr("100.64.0.1")},
		{netip.MustParseAddr("192.88.99.2")},
		{netip.MustParseAddr("64:ff9b::c000:201")},
		{netip.MustParseAddr("64:ff9b:1::1")},
		{netip.MustParseAddr("100::1")},
		{netip.MustParseAddr("100:0:0:1::1")},
		{netip.MustParseAddr("2001:2::1")},
		{netip.MustParseAddr("2001:10::1")},
		{netip.MustParseAddr("2001:db8::1")},
		{netip.MustParseAddr("2002::1")},
		{netip.MustParseAddr("3fff::1")},
		{netip.MustParseAddr("5f00::1")},
		{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("192.168.1.2")},
	} {
		values := values
		t.Run(values[0].String(), func(t *testing.T) {
			t.Parallel()
			dialed := false
			dial := restrictedDialContext(staticResolver{addresses: values}, func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, errors.New("unexpected dial")
			})
			_, err := dial(t.Context(), "tcp", "provider.example:443")
			if !errors.Is(err, ErrRestrictedAddress) || dialed {
				t.Fatalf("dial error/dialed = %v/%v", err, dialed)
			}
		})
	}
}

func TestRestrictedDialContextDialsOnlyTheValidatedPublicIP(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("dial stopped")
	var dialedAddress string
	dial := restrictedDialContext(staticResolver{addresses: []netip.Addr{netip.MustParseAddr("93.184.216.34")}}, func(_ context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" {
			t.Fatalf("network = %q", network)
		}
		dialedAddress = address
		return nil, wantErr
	})
	_, err := dial(t.Context(), "tcp", "provider.example:443")
	if !errors.Is(err, wantErr) || dialedAddress != "93.184.216.34:443" {
		t.Fatalf("dial error/address = %v/%q", err, dialedAddress)
	}
}

func TestTransportDisablesProxyResolutionBypass(t *testing.T) {
	t.Parallel()
	if Transport().Proxy != nil {
		t.Fatal("outbound transport allows an unchecked proxy resolution path")
	}
}

func TestInternalServiceDialContextAllowsOnlyExactPrivateEndpoint(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("dial stopped")
	var dialedAddress string
	dial := internalServiceDialContext(
		staticResolver{addresses: []netip.Addr{netip.MustParseAddr("172.30.0.8")}},
		func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialedAddress = address
			return nil, wantErr
		},
		"osrm",
		"5000",
	)
	_, err := dial(t.Context(), "tcp", "osrm:5000")
	if !errors.Is(err, wantErr) || dialedAddress != "172.30.0.8:5000" {
		t.Fatalf("dial error/address = %v/%q", err, dialedAddress)
	}

	for _, address := range []string{"router:5000", "osrm:80", "osrm", "127.0.0.1:5000"} {
		if _, err := dial(t.Context(), "tcp", address); !errors.Is(err, ErrRestrictedAddress) {
			t.Fatalf("dial(%q) error = %v, want restricted address", address, err)
		}
	}
}

func TestInternalServiceDialContextRejectsNonPrivateOrMixedResolution(t *testing.T) {
	t.Parallel()
	for _, addresses := range [][]netip.Addr{
		{netip.MustParseAddr("127.0.0.1")},
		{netip.MustParseAddr("169.254.1.1")},
		{netip.MustParseAddr("93.184.216.34")},
		{netip.MustParseAddr("172.30.0.8"), netip.MustParseAddr("93.184.216.34")},
		{netip.MustParseAddr("ff02::1")},
	} {
		addresses := addresses
		t.Run(addresses[0].String(), func(t *testing.T) {
			t.Parallel()
			dialed := false
			dial := internalServiceDialContext(staticResolver{addresses: addresses}, func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, errors.New("unexpected dial")
			}, "osrm", "5000")
			_, err := dial(t.Context(), "tcp", "osrm:5000")
			if !errors.Is(err, ErrRestrictedAddress) || dialed {
				t.Fatalf("dial error/dialed = %v/%v", err, dialed)
			}
		})
	}
}

func TestInternalServiceTransportDisablesProxyResolutionBypass(t *testing.T) {
	t.Parallel()
	if InternalServiceTransport("osrm", "5000").Proxy != nil {
		t.Fatal("internal service transport allows a proxy resolution path")
	}
}

func TestTailscaleServiceDialContextAllowsOnlyConfiguredNumericEndpoint(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("dial stopped")
	var dialedAddress string
	dial := tailscaleServiceDialContext(func(_ context.Context, _ string, address string) (net.Conn, error) {
		dialedAddress = address
		return nil, wantErr
	}, netip.MustParseAddr("100.115.58.99"), "5000")
	_, err := dial(t.Context(), "tcp", "100.115.58.99:5000")
	if !errors.Is(err, wantErr) || dialedAddress != "100.115.58.99:5000" {
		t.Fatalf("dial error/address = %v/%q", err, dialedAddress)
	}

	for _, address := range []string{
		"100.115.58.98:5000", "100.115.58.99:80", "router:5000",
		"127.0.0.1:5000", "10.0.0.1:5000", "93.184.216.34:5000",
	} {
		dialedAddress = ""
		if _, err := dial(t.Context(), "tcp", address); !errors.Is(err, ErrRestrictedAddress) || dialedAddress != "" {
			t.Fatalf("dial(%q) error/address = %v/%q", address, err, dialedAddress)
		}
	}
}

func TestTailscaleServiceTransportValidatesEndpointAndDisablesProxy(t *testing.T) {
	t.Parallel()
	transport, err := TailscaleServiceTransport("100.115.58.99", "5000")
	if err != nil {
		t.Fatal(err)
	}
	if transport.Proxy != nil {
		t.Fatal("tailscale service transport allows a proxy resolution path")
	}
	for _, endpoint := range [][2]string{
		{"router", "5000"}, {"100.63.255.255", "5000"}, {"100.128.0.0", "5000"},
		{"10.0.0.1", "5000"}, {"100.115.58.99", "80"},
	} {
		if _, err := TailscaleServiceTransport(endpoint[0], endpoint[1]); !errors.Is(err, ErrRestrictedAddress) {
			t.Fatalf("TailscaleServiceTransport(%q, %q) error = %v", endpoint[0], endpoint[1], err)
		}
	}
}

func TestResolveHandlesIPLiteralsResolverFailuresAndEmptyAnswers(t *testing.T) {
	direct, err := resolve(t.Context(), staticResolver{}, "::ffff:93.184.216.34")
	if err != nil || len(direct) != 1 || direct[0].String() != "93.184.216.34" {
		t.Fatalf("resolve literal = %#v, %v", direct, err)
	}
	wantErr := errors.New("DNS unavailable")
	if _, err := resolve(t.Context(), failingResolver{err: wantErr}, "provider.example"); !errors.Is(err, wantErr) {
		t.Fatalf("resolve failure = %v, want wrapped resolver error", err)
	}
	if _, err := resolve(t.Context(), staticResolver{}, "provider.example"); err == nil {
		t.Fatal("resolve() accepted an empty DNS response")
	}
}

func TestRestrictedDialContextRejectsMalformedDestinationsAndTriesValidatedAddresses(t *testing.T) {
	dial := restrictedDialContext(staticResolver{addresses: []netip.Addr{netip.MustParseAddr("93.184.216.34")}}, func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("unexpected dial")
	})
	for _, address := range []string{"provider.example", ":443"} {
		if _, err := dial(t.Context(), "tcp", address); err == nil {
			t.Fatalf("dial(%q) accepted malformed destination", address)
		}
	}

	firstErr := errors.New("first address unavailable")
	secondErr := errors.New("second address unavailable")
	var attempts []string
	dial = restrictedDialContext(staticResolver{addresses: []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("1.1.1.1")}}, func(_ context.Context, _ string, address string) (net.Conn, error) {
		attempts = append(attempts, address)
		if len(attempts) == 1 {
			return nil, firstErr
		}
		return nil, secondErr
	})
	if _, err := dial(t.Context(), "tcp", "provider.example:443"); !errors.Is(err, firstErr) || !errors.Is(err, secondErr) || len(attempts) != 2 {
		t.Fatalf("dial errors/attempts = %v/%#v", err, attempts)
	}

	var peer net.Conn
	dial = restrictedDialContext(staticResolver{addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1")}}, func(context.Context, string, string) (net.Conn, error) {
		connection, other := net.Pipe()
		peer = other
		return connection, nil
	})
	connection, err := dial(t.Context(), "tcp", "provider.example:443")
	if err != nil || connection == nil || peer == nil {
		t.Fatalf("successful dial = %v, %v, %v", connection, peer, err)
	}
	_ = connection.Close()
	_ = peer.Close()
}

func TestDialContextReturnsRestrictedDialer(t *testing.T) {
	if dial := DialContext(time.Second); dial == nil {
		t.Fatal("DialContext() returned nil")
	}
}
