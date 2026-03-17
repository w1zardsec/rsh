package tui

import (
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// uploadFile sends a local file to the remote shell over the existing connection.
//
// Strategy:
//  1. stty -echo    — suppress remote echo so base64 chunks don't flood the screen
//  2. printf chunks — write base64 data to a temp file in pieces (avoids heredoc
//     noise and sidesteps ARG_MAX limits for large files)
//  3. base64 -d     — decode temp file to the target path
//  4. printf notif  — print a coloured success/failure line through the same stream
//     so it appears in-shell when attached, and the caller can also
//     surface the result in the Event Log via the returned error
//  5. stty echo     — restore echo
func uploadFile(conn net.Conn, localPath, remotePath string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("read %q: %w", localPath, err)
	}

	encoded := base64.StdEncoding.EncodeToString(data)

	// Unique temp file so concurrent uploads don't collide.
	tmpFile := fmt.Sprintf("/tmp/.nw_%x", len(encoded)%0xffff)

	// Build commands as a slice so each is a clean shell statement.
	var cmds []string

	// 1. Silence echo for the duration.
	cmds = append(cmds, "stty -echo 2>/dev/null")

	// 2. Write base64 in 65 536-char chunks (≈ 48 KB binary each).
	//    Single-quoted printf args: base64 alphabet (A-Za-z0-9+/=) never contains '.
	const chunkLen = 65536
	for i, first := 0, true; i < len(encoded); i += chunkLen {
		end := i + chunkLen
		if end > len(encoded) {
			end = len(encoded)
		}
		chunk := encoded[i:end]
		if first {
			cmds = append(cmds, fmt.Sprintf("printf '%%s' '%s' > %s", chunk, tmpFile))
			first = false
		} else {
			cmds = append(cmds, fmt.Sprintf("printf '%%s' '%s' >> %s", chunk, tmpFile))
		}
	}

	// 3. Decode, then clean up temp file.
	fileName := filepath.Base(remotePath)
	cmds = append(cmds,
		fmt.Sprintf(
			"base64 -d %s > %s 2>/dev/null && rm -f %s && "+
				`printf '\r\n\033[32m[rsh] Upload OK: %s\033[0m\r\n' || `+
				`printf '\r\n\033[31m[rsh] Upload FAILED: %s\033[0m\r\n'; rm -f %s 2>/dev/null`,
			tmpFile, remotePath, tmpFile,
			fileName,
			fileName,
			tmpFile,
		),
	)

	// 4. Restore echo.
	cmds = append(cmds, "stty echo 2>/dev/null")

	// Send all commands, one per line.
	payload := strings.Join(cmds, "\n") + "\n"
	_, err = conn.Write([]byte(payload))
	return err
}
