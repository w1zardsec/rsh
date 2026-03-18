package tui

import "testing"

func TestValidatePort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		port    string
		wantErr bool
	}{
		{name: "empty uses default", port: "", wantErr: false},
		{name: "valid lower range", port: "1", wantErr: false},
		{name: "valid upper range", port: "65535", wantErr: false},
		{name: "zero rejected", port: "0", wantErr: true},
		{name: "too large rejected", port: "65536", wantErr: true},
		{name: "alpha rejected", port: "abc", wantErr: true},
		{name: "mixed rejected", port: "44a4", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validatePort(tt.port)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validatePort(%q) error = %v, wantErr %v", tt.port, err, tt.wantErr)
			}
		})
	}
}
