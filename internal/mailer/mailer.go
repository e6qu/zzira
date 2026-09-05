package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/store"
)

type Message struct {
	Recipient string
	Subject   string
	Body      string
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
	connection, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", s.Address)
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(connection, s.Host)
	if err != nil {
		_ = connection.Close()
		return err
	}
	defer client.Close()
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
	payload := "From: " + s.From + "\r\nTo: " + message.Recipient + "\r\nSubject: " + message.Subject +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + normalizeBody(message.Body)
	if _, err := io.WriteString(writer, payload); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
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
}

func (r *Runner) Run(ctx context.Context) {
	poll := r.Poll
	if poll <= 0 {
		poll = time.Second
	}
	for {
		delivery, err := r.Store.ClaimEmailDelivery(ctx)
		if err == nil && delivery != nil {
			sendErr := r.Sender.Send(ctx, Message{Recipient: delivery.Recipient, Subject: delivery.Subject, Body: delivery.Body})
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
