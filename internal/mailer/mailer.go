package mailer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/store"
)

type Message struct {
	Recipient string
	Subject   string
	Body      string
	// HTMLBody, when set, is sent as an HTML alternative to Body.
	HTMLBody string
	// From, when set, is the address the message says it comes from: a
	// project's own sender address. The envelope stays the site's, so a
	// bounce comes back to the site rather than to a project mailbox that
	// may not exist.
	From string
}

type Sender interface {
	Send(context.Context, Message) error
}

type SMTP struct {
	Address  string
	Host     string
	Username string
	Password string
	From     string
}

// SMTPFromEnv returns nil when invitation email is intentionally disabled. A
// partial configuration is rejected so the API never claims delivery through
// a silently unusable provider.
func SMTPFromEnv() (*SMTP, error) {
	address := strings.TrimSpace(os.Getenv("ZZIRA_SMTP_ADDR"))
	from := strings.TrimSpace(os.Getenv("ZZIRA_SMTP_FROM"))
	username := strings.TrimSpace(os.Getenv("ZZIRA_SMTP_USERNAME"))
	password := os.Getenv("ZZIRA_SMTP_PASSWORD")
	if address == "" && from == "" && username == "" && password == "" {
		return nil, nil
	}
	if address == "" || from == "" {
		return nil, errors.New("ZZIRA_SMTP_ADDR and ZZIRA_SMTP_FROM are both required when invitation email is enabled")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("ZZIRA_SMTP_ADDR must be host:port: %w", err)
	}
	if (username == "") != (password == "") {
		return nil, errors.New("ZZIRA_SMTP_USERNAME and ZZIRA_SMTP_PASSWORD must be configured together")
	}
	fromAddress, err := mail.ParseAddress(from)
	if err != nil || fromAddress.Address != from {
		return nil, errors.New("ZZIRA_SMTP_FROM must be one email address without a display name")
	}
	return &SMTP{Address: address, Host: host, Username: username, Password: password, From: from}, nil
}

func (s *SMTP) Send(ctx context.Context, message Message) error {
	if strings.ContainsAny(message.Recipient+message.Subject+s.From, "\r\n") {
		return errors.New("mail headers cannot contain newlines")
	}
	from := s.From
	if message.From != "" {
		address, err := mail.ParseAddress(message.From)
		if err != nil || address.Address != message.From {
			return fmt.Errorf("sender %q is not one plain email address", message.From)
		}
		from = message.From
	}
	connection, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", s.Address)
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(connection, s.Host)
	if err != nil {
		_ = connection.Close()
		return err
	}
	defer func() { _ = client.Close() }()
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if s.Username != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return errors.New("SMTP server does not advertise authentication")
		}
		if err := client.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return err
		}
	}
	// The envelope sender stays the site's; only the From header carries a
	// project's address.
	if err := client.Mail(s.From); err != nil {
		return err
	}
	if err := client.Rcpt(message.Recipient); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	payload, err := messagePayload(from, message)
	if err != nil {
		_ = writer.Close()
		return err
	}
	if _, err := io.WriteString(writer, payload); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// messagePayload is a message's headers and body. The subject is RFC 2047
// encoded when it is not plain ASCII. A message with an HTML part is
// multipart/alternative with plain text first, as RFC 2046 orders it, and both
// parts quoted-printable so long lines and UTF-8 survive any relay.
func messagePayload(from string, message Message) (string, error) {
	headers := "From: " + from + "\r\nTo: " + message.Recipient + "\r\nSubject: " + mime.QEncoding.Encode("utf-8", message.Subject) + "\r\nMIME-Version: 1.0\r\n"
	if message.HTMLBody == "" {
		return headers + "Content-Type: text/plain; charset=UTF-8\r\n\r\n" + normalizeBody(message.Body), nil
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	boundary := "zzira-" + hex.EncodeToString(random[:])
	var b strings.Builder
	b.WriteString(headers + "Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
	for _, part := range []struct{ contentType, body string }{{"text/plain", message.Body}, {"text/html", message.HTMLBody}} {
		b.WriteString("--" + boundary + "\r\nContent-Type: " + part.contentType + "; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
		encoder := quotedprintable.NewWriter(&b)
		if _, err := encoder.Write([]byte(part.body)); err != nil {
			return "", err
		}
		if err := encoder.Close(); err != nil {
			return "", err
		}
		b.WriteString("\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.String(), nil
}

var relativeHref = regexp.MustCompile(`href="/([^/"])`)

// absoluteLinks turns the site-relative links queued mail carries into links
// to this site: href="/..." attributes in HTML, and lines that are only a
// path in plain text. Protocol-relative links are left alone.
func absoluteLinks(baseURL, body string, isHTML bool) string {
	base := strings.TrimRight(baseURL, "/")
	if base == "" || body == "" {
		return body
	}
	if isHTML {
		return relativeHref.ReplaceAllString(body, `href="`+base+`/$1`)
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "/") && !strings.HasPrefix(line, "//") && !strings.ContainsAny(line, " \t") {
			lines[i] = base + line
		}
	}
	return strings.Join(lines, "\n")
}

func normalizeBody(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
}

type Runner struct {
	Store  *store.Store
	Sender Sender
	Poll   time.Duration
	// BaseURL makes the site-relative links in queued mail absolute.
	BaseURL string
}

func (r *Runner) Run(ctx context.Context) {
	poll := r.Poll
	if poll <= 0 {
		poll = time.Second
	}
	for {
		delivery, err := r.Store.ClaimEmailDelivery(ctx)
		if err == nil && delivery != nil {
			sendErr := r.Sender.Send(ctx, Message{Recipient: delivery.Recipient, Subject: delivery.Subject, From: delivery.Sender,
				Body: absoluteLinks(r.BaseURL, delivery.Body, false), HTMLBody: absoluteLinks(r.BaseURL, delivery.HTMLBody, true)})
			if err := r.Store.CompleteEmailDelivery(ctx, delivery.ID, sendErr); err != nil {
				log.Printf("complete invitation email delivery %d: %v", delivery.ID, err)
			}
			continue
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("claim invitation email delivery: %v", err)
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}
