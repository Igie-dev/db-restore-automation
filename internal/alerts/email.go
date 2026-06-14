package alerts

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"

	"db-restore-automation/internal/config"
)

type EmailNotifier struct {
	Config config.EmailConfig
}

func (n EmailNotifier) Notify(ctx context.Context, event Event) error {
	username := os.Getenv(n.Config.UsernameEnv)
	password := os.Getenv(n.Config.PasswordEnv)
	from := os.Getenv(n.Config.FromEnv)
	if username == "" || password == "" || from == "" {
		return fmt.Errorf("email username, password, or from environment variable is empty")
	}

	addr := fmt.Sprintf("%s:%d", n.Config.SMTPHost, n.Config.SMTPPort)
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, n.Config.SMTPHost)
	if err != nil {
		return err
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: n.Config.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if ok, _ := client.Extension("AUTH"); ok {
		if err := client.Auth(smtp.PlainAuth("", username, password, n.Config.SMTPHost)); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range n.Config.To {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	message := buildEmailMessage(from, n.Config.To, event)
	if _, err := writer.Write([]byte(message)); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func buildEmailMessage(from string, to []string, event Event) string {
	var b strings.Builder
	w := bufio.NewWriter(&b)
	fmt.Fprintf(w, "From: %s\r\n", from)
	fmt.Fprintf(w, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(w, "Subject: DB restore %s: %s\r\n", event.Result, event.JobName)
	fmt.Fprint(w, "MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n")
	fmt.Fprintln(w, formatEvent(event))
	_ = w.Flush()
	return b.String()
}
