package outbound

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

type staticResolver struct {
	addresses []netip.Addr
}

func (resolver staticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), resolver.addresses...), nil
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
