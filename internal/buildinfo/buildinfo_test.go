package buildinfo

import "testing"

func TestInfoDisplayVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info Info
		want string
	}{
		{name: "version and full commit", info: Info{Version: "0.1.23", Commit: "11f91120aeba15b59f7c99d805a4ba08a8906672"}, want: "0.1.23 · 11f9112"},
		{name: "already short commit", info: Info{Version: "0.1.23", Commit: "11f9112"}, want: "0.1.23 · 11f9112"},
		{name: "unstamped local build", info: Info{Version: "dev", Commit: "unknown"}, want: "dev"},
		{name: "empty metadata", info: Info{}, want: "dev"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.info.DisplayVersion(); got != test.want {
				t.Fatalf("DisplayVersion() = %q, want %q", got, test.want)
			}
		})
	}
}
