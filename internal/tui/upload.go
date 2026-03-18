package tui

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// uploadFile sends a local file to the remote shell over the existing connection.
//
// Strategy:
//  1. Write base64 content to a temp file using a heredoc so the generated script
//     stays compact and predictable on dumb shells.
//  2. Decode the temp file into the target path on the remote host.
//  3. Emit a small marker-bounded status string that rsh reads back from the
//     connection, so the UI can report a real success/failure instead of
//     asking the operator to inspect the shell manually.
func uploadFile(conn net.Conn, localPath, remotePath string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("read %q: %w", localPath, err)
	}

	tmpFile, err := newUploadTempPath(filepath.Base(localPath))
	if err != nil {
		return err
	}

	startMarker := fmt.Sprintf("__RSH_UPLOAD_BEGIN_%d__", time.Now().UnixNano())
	endMarker := fmt.Sprintf("__RSH_UPLOAD_END_%d__", time.Now().UnixNano())

	payload, err := buildUploadPayload(data, remotePath, tmpFile, startMarker, endMarker)
	if err != nil {
		return err
	}

	drainRemoteConn(conn, 120*time.Millisecond)
	if err := sendRemoteCommand(conn, payload); err != nil {
		return err
	}

	status, err := readRemoteUntilMarkers(conn, startMarker, endMarker, uploadTimeoutForSize(len(data)))
	if err != nil {
		return err
	}

	status = strings.TrimSpace(status)
	if status != "OK" {
		if status == "" {
			status = "unknown remote upload error"
		}
		return fmt.Errorf("%s", status)
	}
	return nil
}

func buildUploadPayload(data []byte, remotePath, tmpFile, startMarker, endMarker string) (string, error) {
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		return "", fmt.Errorf("remote path is required")
	}

	encoded := wrapBase64(base64.StdEncoding.EncodeToString(data), 76)
	quotedTmp := shellQuote(tmpFile)
	quotedRemote := shellQuote(remotePath)
	hereDocMarker := "RSH_UPLOAD_" + sanitizeTempLabel(filepath.Base(tmpFile))

	// Build commands as a slice so each is a clean shell statement.
	var cmds []string

	// Start from a clean line so a remote interactive prompt doesn't end up
	// glued to the upload preamble.
	cmds = append(cmds, "")
	cmds = append(cmds, fmt.Sprintf("cat > %s <<'%s'", quotedTmp, hereDocMarker))
	cmds = append(cmds, encoded)
	cmds = append(cmds, hereDocMarker)
	cmds = append(cmds, "rsh_upload_status=FAIL")
	cmds = append(cmds,
		fmt.Sprintf(
			"if command -v base64 >/dev/null 2>&1 && base64 -d %s > %s 2>/dev/null; then rsh_upload_status=OK; else rsh_upload_status='FAIL: remote decode/write failed'; fi",
			quotedTmp,
			quotedRemote,
		),
	)
	cmds = append(cmds, fmt.Sprintf("rm -f %s 2>/dev/null", quotedTmp))
	cmds = append(cmds,
		fmt.Sprintf(
			"printf %s; printf '%%s' \"$rsh_upload_status\"; printf %s\n",
			shellQuote(startMarker),
			shellQuote(endMarker),
		),
	)

	return strings.Join(cmds, "\n"), nil
}

func newUploadTempPath(localName string) (string, error) {
	token := make([]byte, 6)
	if _, err := rand.Read(token); err != nil {
		token = []byte(fmt.Sprintf("%x", time.Now().UnixNano()))
	}
	base := strings.TrimSpace(filepath.Base(localName))
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "upload"
	}
	safeBase := sanitizeTempLabel(base)
	return fmt.Sprintf("/tmp/.rsh-%s-%x.b64", safeBase, token), nil
}

func sanitizeTempLabel(name string) string {
	var out []rune
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r)
		case r >= '0' && r <= '9':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "upload"
	}
	return string(out)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func wrapBase64(value string, lineLen int) string {
	if lineLen <= 0 || len(value) <= lineLen {
		return value
	}
	var out strings.Builder
	for i := 0; i < len(value); i += lineLen {
		end := i + lineLen
		if end > len(value) {
			end = len(value)
		}
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(value[i:end])
	}
	return out.String()
}

func uploadTimeoutForSize(size int) time.Duration {
	timeout := 10 * time.Second
	timeout += time.Duration(size/(128*1024)) * time.Second
	if timeout < 10*time.Second {
		return 10 * time.Second
	}
	if timeout > 2*time.Minute {
		return 2 * time.Minute
	}
	return timeout
}
