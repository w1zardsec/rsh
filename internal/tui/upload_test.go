package tui

import (
	"strings"
	"testing"
)

func TestBuildUploadPayloadQuotesPaths(t *testing.T) {
	t.Parallel()

	payload, err := buildUploadPayload(
		[]byte("hello"),
		"/tmp/dir with space/o'hare.txt",
		"/tmp/.rsh-fixed.b64",
		"__BEGIN__",
		"__END__",
	)
	if err != nil {
		t.Fatalf("buildUploadPayload returned error: %v", err)
	}

	if !strings.Contains(payload, "'/tmp/.rsh-fixed.b64'") {
		t.Fatalf("expected temp path to be shell-quoted, payload: %s", payload)
	}

	if !strings.Contains(payload, "'/tmp/dir with space/o'\"'\"'hare.txt'") {
		t.Fatalf("expected remote path to be shell-quoted, payload: %s", payload)
	}

	if !strings.Contains(payload, "printf '__BEGIN__'") || !strings.Contains(payload, "printf '__END__'") {
		t.Fatalf("expected status markers in payload, payload: %s", payload)
	}
}

func TestNewUploadTempPathIsScopedToTmp(t *testing.T) {
	t.Parallel()

	path, err := newUploadTempPath("../payload.bin")
	if err != nil {
		t.Fatalf("newUploadTempPath returned error: %v", err)
	}

	if !strings.HasPrefix(path, "/tmp/.rsh-payload_bin-") {
		t.Fatalf("unexpected temp path: %s", path)
	}
}

func TestWrapBase64BreaksLongLines(t *testing.T) {
	t.Parallel()

	wrapped := wrapBase64("abcdefghijklmnopqrstuvwxyz", 5)
	lines := strings.Split(wrapped, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected wrapped output to contain multiple lines, got %q", wrapped)
	}
	for i, line := range lines[:len(lines)-1] {
		if len(line) != 5 {
			t.Fatalf("line %d length = %d, want 5", i, len(line))
		}
	}
}
