package tui

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	xterm "github.com/charmbracelet/x/term"
)

// f12Seq is the ANSI escape sequence for the F12 key.
const f12Seq = "\x1b[24~"

// InteractiveSession implements tea.ExecCommand.
// It bridges local stdin/stdout with a remote net.Conn.
// Press F12 to detach and return to the TUI.
type InteractiveSession struct {
	conn net.Conn
}

// NewInteractiveSession returns a session for the given connection.
func NewInteractiveSession(conn net.Conn) *InteractiveSession {
	return &InteractiveSession{conn: conn}
}

func (s *InteractiveSession) SetStdin(_ io.Reader)  {}
func (s *InteractiveSession) SetStdout(_ io.Writer) {}
func (s *InteractiveSession) SetStderr(_ io.Writer) {}

// drainStdin discards any bytes already buffered in stdin (leftover TUI input).
func drainStdin() {
	fd := int(os.Stdin.Fd())
	syscall.SetNonblock(fd, true) //nolint:errcheck
	buf := make([]byte, 4096)
	for {
		n, _ := os.Stdin.Read(buf)
		if n == 0 {
			break
		}
	}
	syscall.SetNonblock(fd, false) //nolint:errcheck
}

// Run bridges stdin ↔ conn until F12 is pressed or the connection is closed.
func (s *InteractiveSession) Run() error {
	fd := os.Stdin.Fd()

	// Put the local terminal into raw mode so every keystroke is sent
	// immediately and control characters pass through.
	if xterm.IsTerminal(fd) {
		state, err := xterm.MakeRaw(fd)
		if err == nil {
			defer xterm.Restore(fd, state) //nolint:errcheck
		}
	}

	// Give bubbletea a moment to finish its last render (exit alt-screen,
	// restore terminal), then throw away any stale keypresses that were
	// buffered while the TUI was active.
	time.Sleep(150 * time.Millisecond)
	drainStdin()

	// Watch for SIGWINCH (terminal resize) and forward to remote.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)
	go func() {
		for range sigCh {
		}
	}()

	done := make(chan error, 2)

	// remote → local stdout
	go func() {
		_, err := io.Copy(os.Stdout, s.conn)
		done <- err
	}()

	// Auto-upgrade to a proper PTY-backed shell.
	// This turns a dumb pipe shell (e.g. nc -e /bin/bash) into a full
	// interactive terminal with job control, readline, and Ctrl+C support.
	upgradePayload := "python3 -c 'import pty; pty.spawn(\"/bin/bash\")' 2>/dev/null || " +
		"python -c 'import pty; pty.spawn(\"/bin/bash\")' 2>/dev/null || " +
		"script -qc /bin/bash /dev/null\n"
	s.conn.Write([]byte(upgradePayload)) //nolint:errcheck

	// Wait for the remote PTY to initialise, then push terminal dimensions,
	// set TERM, clear the screen, and print the detach hint — all as a single
	// remote command so the hint flows through io.Copy in the correct order.
	time.Sleep(400 * time.Millisecond)
	w, h, err := xterm.GetSize(fd)
	hint := `printf '\033[33m[rsh]\033[0m Attached \xe2\x80\x94 press \033[1mF12\033[22m to detach\r\n\r\n'`
	if err == nil {
		s.conn.Write([]byte(fmt.Sprintf( //nolint:errcheck
			"stty rows %d columns %d 2>/dev/null; export TERM=xterm-256color; export SHELL=/bin/bash; clear; %s\n", h, w, hint,
		)))
	} else {
		s.conn.Write([]byte(fmt.Sprintf("export TERM=xterm-256color; export SHELL=/bin/bash; clear; %s\n", hint))) //nolint:errcheck
	}

	// local stdin → remote; watch for F12 to detach
	go func() {
		seq := []byte(f12Seq)
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				if bytes.Contains(chunk, seq) {
					done <- nil
					return
				}
				if _, werr := s.conn.Write(chunk); werr != nil {
					done <- werr
					return
				}
			}
			if err != nil {
				done <- err
				return
			}
		}
	}()

	<-done

	os.Stdout.WriteString("\r\n\033[33m[rsh]\033[0m Detached from shell.\r\n") //nolint:errcheck
	return nil
}
