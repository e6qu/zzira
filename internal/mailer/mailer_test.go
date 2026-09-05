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
