package tui

import "testing"

func TestNormalizeRemoteArch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{raw: "x86_64", want: "amd64"},
		{raw: "amd64", want: "amd64"},
		{raw: "aarch64", want: "arm64"},
		{raw: "arm64", want: "arm64"},
		{raw: "i686", want: "386"},
		{raw: "armv7l", want: "armv7"},
		{raw: "mips", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.raw, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeRemoteArch(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeRemoteArch(%q) error = %v, wantErr %v", tt.raw, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("normalizeRemoteArch(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestPostExToolNames(t *testing.T) {
	t.Parallel()

	names := postExToolNames([]string{"linpeas", "chisel"})
	if len(names) != 2 {
		t.Fatalf("expected 2 tool names, got %d", len(names))
	}
	if names[0] != "LinPEAS" || names[1] != "Chisel" {
		t.Fatalf("unexpected tool names: %#v", names)
	}
}

func TestChiselAssetPatternMatchesArch(t *testing.T) {
	t.Parallel()

	match := chiselLinuxAssetPattern.FindStringSubmatch("chisel_1.9.1_linux_arm64.gz")
	if len(match) != 2 || match[1] != "arm64" {
		t.Fatalf("unexpected chisel asset match: %#v", match)
	}
}
