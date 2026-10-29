package templates

import "testing"

func TestCustomerEmailURLPercentEncodesMailtoDelimiters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "plain address", value: "maria.maier@example.test", want: "mailto:maria.maier@example.test"},
		{name: "plus and tilde stay literal", value: "maria+tag~x@example.test", want: "mailto:maria+tag~x@example.test"},
		{name: "header and recipient injection", value: "a?cc=evil&bcc=x@example.test", want: "mailto:a%3Fcc%3Devil%26bcc%3Dx@example.test"},
		{name: "fragment percent and slash", value: "a#b%c/d{e}@example.test", want: "mailto:a%23b%25c%2Fd%7Be%7D@example.test"},
		{name: "non ascii", value: "jürgen@example.test", want: "mailto:j%C3%BCrgen@example.test"},
		{name: "display name rejected", value: "Maria <maria@example.test>", want: ""},
		{name: "invalid rejected", value: "not an address", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := customerEmailURL(test.value); got != test.want {
				t.Fatalf("customerEmailURL(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestMailtoAddressEscapeEncodesSeparatorsAndControlBytes(t *testing.T) {
	t.Parallel()
	got := mailtoAddressEscape("a b,c:d;e\r\n=f@example.test")
	want := "a%20b%2Cc%3Ad%3Be%0D%0A%3Df@example.test"
	if got != want {
		t.Fatalf("mailtoAddressEscape() = %q, want %q", got, want)
	}
}
