package mailer

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSMTPSendsInvitationMessage(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan string, 1)
	serverError := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			serverError <- err
			return
		}
		defer connection.Close()
		reader, writer := bufio.NewReader(connection), bufio.NewWriter(connection)
		write := func(value string) bool {
			if _, err := writer.WriteString(value); err != nil {
				serverError <- err
				return false
			}
			if err := writer.Flush(); err != nil {
				serverError <- err
				return false
			}
			return true
		}
		if !write("220 smtp.test ESMTP\r\n") {
			return
		}
		var message strings.Builder
		inData := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				serverError <- err
				return
			}
			if inData {
				if line == ".\r\n" {
					received <- message.String()
					inData = false
					if !write("250 queued\r\n") {
						return
					}
					continue
				}
				message.WriteString(line)
				continue
			}
			command := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(command, "EHLO"):
				if !write("250 smtp.test\r\n") {
					return
				}
			case strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
				if !write("250 ok\r\n") {
					return
				}
			case strings.HasPrefix(command, "DATA"):
				inData = true
				if !write("354 end with dot\r\n") {
					return
				}
			case strings.HasPrefix(command, "QUIT"):
				_ = write("221 bye\r\n")
				return
			default:
				serverError <- &smtpTestError{line: line}
				return
			}
		}
	}()

	sender := &SMTP{Address: listener.Addr().String(), Host: "127.0.0.1", From: "noreply@zzira.test"}
	err = sender.Send(context.Background(), Message{Recipient: "person@example.test", Subject: "Invitation", Body: "Welcome\nto ZZIRA"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverError:
		t.Fatal(err)
	case payload := <-received:
		for _, expected := range []string{"To: person@example.test", "Subject: Invitation", "Welcome\r\nto ZZIRA"} {
			if !strings.Contains(payload, expected) {
				t.Fatalf("message does not contain %q: %q", expected, payload)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SMTP server did not receive the invitation")
	}
}

type smtpTestError struct{ line string }

func (e *smtpTestError) Error() string { return "unexpected SMTP command: " + e.line }

func TestSMTPFromEnvRejectsPartialConfiguration(t *testing.T) {
	for _, name := range []string{"ZZIRA_SMTP_ADDR", "ZZIRA_SMTP_FROM", "ZZIRA_SMTP_USERNAME", "ZZIRA_SMTP_PASSWORD"} {
		t.Setenv(name, "")
	}
	configured, err := SMTPFromEnv()
	if err != nil || configured != nil {
		t.Fatalf("empty configuration = %+v, %v", configured, err)
	}
	t.Setenv("ZZIRA_SMTP_ADDR", "smtp.example.test:587")
	if _, err := SMTPFromEnv(); err == nil {
		t.Fatal("partial SMTP configuration was accepted")
	}
}

func TestMessagePayloadAlternatives(t *testing.T) {
	plain, err := messagePayload("noreply@zzira.test", Message{Recipient: "a@example.test", Subject: "Invitation", Body: "Hi"})
	if err != nil || !strings.Contains(plain, "Content-Type: text/plain; charset=UTF-8\r\n\r\nHi") || strings.Contains(plain, "multipart") {
		t.Fatalf("plain payload = %q err=%v", plain, err)
	}
	long := strings.Repeat("word ", 40)
	rich, err := messagePayload("noreply@zzira.test", Message{Recipient: "a@example.test", Subject: "[ZZ-1] Ship — soon", Body: "ZZ-1 — Ship\n/browse/ZZ-1", HTMLBody: "<p>" + long + "</p>"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rich, "Subject: =?utf-8?q?") || strings.Contains(rich, "Subject: [ZZ-1] Ship —") {
		t.Fatalf("subject was not encoded: %q", rich)
	}
	boundary := rich[strings.Index(rich, `boundary="`)+len(`boundary="`):]
	boundary = boundary[:strings.Index(boundary, `"`)]
	plainAt, htmlAt, closeAt := strings.Index(rich, "--"+boundary+"\r\nContent-Type: text/plain"), strings.Index(rich, "--"+boundary+"\r\nContent-Type: text/html"), strings.Index(rich, "--"+boundary+"--\r\n")
	if plainAt < 0 || htmlAt < plainAt || closeAt < htmlAt {
		t.Fatalf("parts out of order: %q", rich)
	}
	if !strings.Contains(rich, "ZZ-1 =E2=80=94 Ship") || !strings.Contains(rich, "=\r\n") {
		t.Fatalf("parts were not quoted-printable: %q", rich)
	}
	for _, line := range strings.Split(rich[plainAt:closeAt], "\r\n") {
		if strings.HasPrefix(line, "--"+boundary) || strings.HasPrefix(line, "Content-") {
			continue
		}
		if len(line) > 76 {
			t.Fatalf("line longer than 76 characters: %q", line)
		}
	}
}

func TestAbsoluteLinks(t *testing.T) {
	const base = "https://zzira.example/"
	if got := absoluteLinks(base, `<a href="/browse/ZZ-1">ZZ-1</a> <a href="//cdn.example/x">x</a> <a href="https://other.example/">o</a>`, true); got != `<a href="https://zzira.example/browse/ZZ-1">ZZ-1</a> <a href="//cdn.example/x">x</a> <a href="https://other.example/">o</a>` {
		t.Fatalf("html links = %q", got)
	}
	if got := absoluteLinks(base, "created ZZ-1\n\nZZ-1 — Ship\n/browse/ZZ-1\n/not a path", false); got != "created ZZ-1\n\nZZ-1 — Ship\nhttps://zzira.example/browse/ZZ-1\n/not a path" {
		t.Fatalf("text links = %q", got)
	}
	if got := absoluteLinks("", "/browse/ZZ-1", false); got != "/browse/ZZ-1" {
		t.Fatalf("no base URL changed the body: %q", got)
	}
}
