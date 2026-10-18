// Package notify delivers "new result" reminders.
package notify

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"unicode/utf16"
)

type Notifier interface {
	Notify(ctx context.Context, title, body string) error
}

// Multi sends to every notifier and joins the errors.
type Multi []Notifier

func (m Multi) Notify(ctx context.Context, title, body string) error {
	var errs []error
	for _, n := range m {
		if err := n.Notify(ctx, title, body); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// WindowsToast shows a native Windows notification. Works from WSL by calling
// powershell.exe, using only built-in WinRT APIs (no modules to install).
type WindowsToast struct{}

func (WindowsToast) Notify(ctx context.Context, title, body string) error {
	xml := fmt.Sprintf(`<toast><visual><binding template="ToastGeneric"><text>%s</text><text>%s</text></binding></visual></toast>`,
		xmlEscape(title), xmlEscape(body))
	script := `
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml('` + strings.ReplaceAll(xml, "'", "''") + `')
$toast = [Windows.UI.Notifications.ToastNotification]::new($xml)
$appId = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe'
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($appId).Show($toast)
`
	// -EncodedCommand (UTF-16LE base64) sidesteps all shell quoting issues.
	out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive",
		"-EncodedCommand", encodePS(script)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("windows toast: %w: %s", err, out)
	}
	return nil
}

// DesktopNotify uses notify-send (Linux desktops).
type DesktopNotify struct{}

func (DesktopNotify) Notify(ctx context.Context, title, body string) error {
	out, err := exec.CommandContext(ctx, "notify-send", "-a", "FITET", title, body).CombinedOutput()
	if err != nil {
		return fmt.Errorf("notify-send: %w: %s", err, out)
	}
	return nil
}

// Ntfy pushes to https://ntfy.sh/<topic>; subscribe to the topic in the ntfy
// phone app to get reminders on your phone. Pick a hard-to-guess topic name:
// anyone who knows it can read the messages.
type Ntfy struct {
	Server string // default https://ntfy.sh
	Topic  string
}

func (n Ntfy) Notify(ctx context.Context, title, body string) error {
	server := n.Server
	if server == "" {
		server = "https://ntfy.sh"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(server, "/")+"/"+n.Topic, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	req.Header.Set("Tags", "ping_pong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy: %s", resp.Status)
	}
	return nil
}

func encodePS(s string) string {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, r := range u {
		binary.LittleEndian.PutUint16(b[2*i:], r)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}
