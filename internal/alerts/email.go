package alerts

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"db-restore-automation/internal/config"
)

const (
	emailDialTimeout      = 10 * time.Second
	emailOperationTimeout = 15 * time.Second
)

type EmailNotifier struct {
	Config config.EmailConfig
}

type parsedEmailAddress struct {
	Envelope string
	Header   string
}

func (n EmailNotifier) Notify(
	ctx context.Context,
	event Event,
) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"email notification cancelled before validation: %w",
			err,
		)
	}

	preparedEvent, err := PrepareEvent(event)
	if err != nil {
		return fmt.Errorf(
			"invalid email alert event: %w",
			err,
		)
	}

	smtpHost := strings.TrimSpace(n.Config.SMTPHost)
	if err := validateSMTPHost(smtpHost); err != nil {
		return err
	}

	if n.Config.SMTPPort < 1 ||
		n.Config.SMTPPort > 65535 {
		return fmt.Errorf(
			"SMTP port must be between 1 and 65535",
		)
	}

	usernameEnvironment := strings.TrimSpace(
		n.Config.UsernameEnv,
	)

	passwordEnvironment := strings.TrimSpace(
		n.Config.PasswordEnv,
	)

	fromEnvironment := strings.TrimSpace(
		n.Config.FromEnv,
	)

	if err := validateEnvironmentVariableName(
		usernameEnvironment,
		"email username environment variable",
	); err != nil {
		return err
	}

	if err := validateEnvironmentVariableName(
		passwordEnvironment,
		"email password environment variable",
	); err != nil {
		return err
	}

	if err := validateEnvironmentVariableName(
		fromEnvironment,
		"email from environment variable",
	); err != nil {
		return err
	}

	username, err := requiredEnvironmentValue(
		usernameEnvironment,
		"email username",
		true,
	)
	if err != nil {
		return err
	}

	password, err := requiredEnvironmentValue(
		passwordEnvironment,
		"email password",
		false,
	)
	if err != nil {
		return err
	}

	fromValue, err := requiredEnvironmentValue(
		fromEnvironment,
		"email from address",
		true,
	)
	if err != nil {
		return err
	}

	fromAddress, err := parseEmailAddress(
		fromValue,
		"email from address",
	)
	if err != nil {
		return err
	}

	recipients, err := parseEmailRecipients(
		n.Config.To,
	)
	if err != nil {
		return err
	}

	recipientHeaders := make(
		[]string,
		0,
		len(recipients),
	)

	for _, recipient := range recipients {
		recipientHeaders = append(
			recipientHeaders,
			recipient.Header,
		)
	}

	message := buildEmailMessage(
		fromAddress.Header,
		recipientHeaders,
		preparedEvent,
	)

	address := net.JoinHostPort(
		smtpHost,
		strconv.Itoa(n.Config.SMTPPort),
	)

	dialer := net.Dialer{
		Timeout: emailDialTimeout,
	}

	connection, err := dialer.DialContext(
		ctx,
		"tcp",
		address,
	)
	if err != nil {
		return fmt.Errorf(
			"connect to SMTP server %q: %w",
			address,
			err,
		)
	}

	defer connection.Close()

	cancelWatcherDone := make(chan struct{})

	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()

		case <-cancelWatcherDone:
		}
	}()

	defer close(cancelWatcherDone)

	if err := setSMTPDeadline(
		ctx,
		connection,
	); err != nil {
		return err
	}

	client, err := smtp.NewClient(
		connection,
		smtpHost,
	)
	if err != nil {
		return smtpOperationError(
			ctx,
			"create SMTP client",
			err,
		)
	}

	defer client.Close()

	if err := setSMTPDeadline(
		ctx,
		connection,
	); err != nil {
		return err
	}

	startTLSAvailable, _ := client.Extension(
		"STARTTLS",
	)

	if !startTLSAvailable {
		return fmt.Errorf(
			"SMTP server %q does not advertise STARTTLS; refusing to send credentials or email over an unencrypted connection",
			smtpHost,
		)
	}

	tlsConfig := &tls.Config{
		ServerName: smtpHost,
		MinVersion: tls.VersionTLS12,
	}

	if err := client.StartTLS(tlsConfig); err != nil {
		return smtpOperationError(
			ctx,
			"start SMTP TLS",
			err,
		)
	}

	if err := setSMTPDeadline(
		ctx,
		connection,
	); err != nil {
		return err
	}

	authAvailable, _ := client.Extension(
		"AUTH",
	)

	if !authAvailable {
		return fmt.Errorf(
			"SMTP server %q does not advertise AUTH",
			smtpHost,
		)
	}

	auth := smtp.PlainAuth(
		"",
		username,
		password,
		smtpHost,
	)

	if err := client.Auth(auth); err != nil {
		return smtpOperationError(
			ctx,
			"authenticate with SMTP server",
			err,
		)
	}

	if err := setSMTPDeadline(
		ctx,
		connection,
	); err != nil {
		return err
	}

	if err := client.Mail(
		fromAddress.Envelope,
	); err != nil {
		return smtpOperationError(
			ctx,
			"set SMTP sender",
			err,
		)
	}

	for _, recipient := range recipients {
		if err := setSMTPDeadline(
			ctx,
			connection,
		); err != nil {
			return err
		}

		if err := client.Rcpt(
			recipient.Envelope,
		); err != nil {
			return smtpOperationError(
				ctx,
				fmt.Sprintf(
					"set SMTP recipient %q",
					recipient.Envelope,
				),
				err,
			)
		}
	}

	if err := setSMTPDeadline(
		ctx,
		connection,
	); err != nil {
		return err
	}

	writer, err := client.Data()
	if err != nil {
		return smtpOperationError(
			ctx,
			"start SMTP message data",
			err,
		)
	}

	_, writeErr := writer.Write(
		[]byte(message),
	)

	closeErr := writer.Close()

	if writeErr != nil || closeErr != nil {
		combinedErr := errors.Join(
			writeErr,
			closeErr,
		)

		return smtpOperationError(
			ctx,
			"write SMTP message data",
			combinedErr,
		)
	}

	if err := setSMTPDeadline(
		ctx,
		connection,
	); err != nil {
		return err
	}

	if err := client.Quit(); err != nil {
		return smtpOperationError(
			ctx,
			"finish SMTP session",
			err,
		)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"email notification context ended after sending: %w",
			err,
		)
	}

	return nil
}

func buildEmailMessage(
	from string,
	to []string,
	event Event,
) string {
	event = event.Normalize()

	from = sanitizeEmailHeaderValue(from)

	safeRecipients := make(
		[]string,
		0,
		len(to),
	)

	for _, recipient := range to {
		recipient = sanitizeEmailHeaderValue(
			recipient,
		)

		if recipient != "" {
			safeRecipients = append(
				safeRecipients,
				recipient,
			)
		}
	}

	subjectText := fmt.Sprintf(
		"DB restore %s: %s",
		event.Result,
		event.JobName,
	)

	subjectText = sanitizeEmailHeaderValue(
		subjectText,
	)

	subject := mime.QEncoding.Encode(
		"UTF-8",
		subjectText,
	)

	messageDate := event.FinishedAt
	if messageDate.IsZero() {
		messageDate = time.Now()
	}

	body := strings.ToValidUTF8(
		formatEvent(event),
		"\uFFFD",
	)

	body = normalizeEmailBody(body)

	var builder strings.Builder

	fmt.Fprintf(
		&builder,
		"From: %s\r\n",
		from,
	)

	fmt.Fprintf(
		&builder,
		"To: %s\r\n",
		strings.Join(safeRecipients, ", "),
	)

	fmt.Fprintf(
		&builder,
		"Subject: %s\r\n",
		subject,
	)

	fmt.Fprintf(
		&builder,
		"Date: %s\r\n",
		messageDate.Format(time.RFC1123Z),
	)

	builder.WriteString(
		"MIME-Version: 1.0\r\n",
	)

	builder.WriteString(
		"Content-Type: text/plain; charset=UTF-8\r\n",
	)

	builder.WriteString(
		"Content-Transfer-Encoding: 8bit\r\n",
	)

	builder.WriteString(
		"Auto-Submitted: auto-generated\r\n",
	)

	builder.WriteString(
		"X-Auto-Response-Suppress: All\r\n",
	)

	builder.WriteString("\r\n")
	builder.WriteString(body)

	if !strings.HasSuffix(body, "\r\n") {
		builder.WriteString("\r\n")
	}

	return builder.String()
}

func parseEmailRecipients(
	values []string,
) ([]parsedEmailAddress, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf(
			"email recipient list must contain at least one address",
		)
	}

	recipients := make(
		[]parsedEmailAddress,
		0,
		len(values),
	)

	seen := make(map[string]struct{})

	for index, value := range values {
		recipient, err := parseEmailAddress(
			value,
			fmt.Sprintf(
				"email recipient[%d]",
				index,
			),
		)
		if err != nil {
			return nil, err
		}

		key := strings.ToLower(
			recipient.Envelope,
		)

		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf(
				"duplicate email recipient %q",
				recipient.Envelope,
			)
		}

		seen[key] = struct{}{}

		recipients = append(
			recipients,
			recipient,
		)
	}

	return recipients, nil
}

func parseEmailAddress(
	value string,
	field string,
) (parsedEmailAddress, error) {
	value = strings.TrimSpace(value)

	if value == "" {
		return parsedEmailAddress{}, fmt.Errorf(
			"%s is required",
			field,
		)
	}

	if containsEmailHeaderControlCharacter(value) {
		return parsedEmailAddress{}, fmt.Errorf(
			"%s must not contain control characters",
			field,
		)
	}

	address, err := mail.ParseAddress(value)
	if err != nil {
		return parsedEmailAddress{}, fmt.Errorf(
			"%s %q is invalid: %w",
			field,
			value,
			err,
		)
	}

	envelopeAddress := strings.TrimSpace(
		address.Address,
	)

	if envelopeAddress == "" {
		return parsedEmailAddress{}, fmt.Errorf(
			"%s does not contain an email address",
			field,
		)
	}

	if containsEmailHeaderControlCharacter(
		envelopeAddress,
	) {
		return parsedEmailAddress{}, fmt.Errorf(
			"%s contains unsafe characters",
			field,
		)
	}

	// Parse the mailbox again without a display name to ensure the envelope
	// value itself is a valid mailbox and not another address-list form.
	envelopeMailbox, err := mail.ParseAddress(
		envelopeAddress,
	)
	if err != nil ||
		envelopeMailbox.Address != envelopeAddress {
		return parsedEmailAddress{}, fmt.Errorf(
			"%s contains an invalid envelope address %q",
			field,
			envelopeAddress,
		)
	}

	return parsedEmailAddress{
		Envelope: envelopeAddress,
		Header:   address.String(),
	}, nil
}

func validateSMTPHost(
	host string,
) error {
	host = strings.TrimSpace(host)

	if host == "" {
		return fmt.Errorf(
			"SMTP host is required",
		)
	}

	if strings.ContainsRune(host, '\x00') ||
		strings.ContainsAny(host, "\r\n\t ") {
		return fmt.Errorf(
			"SMTP host must not contain whitespace or control characters",
		)
	}

	if strings.ContainsAny(host, `/\`) {
		return fmt.Errorf(
			"SMTP host must be a hostname or IP address, not a path",
		)
	}

	return nil
}

func setSMTPDeadline(
	ctx context.Context,
	connection net.Conn,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"email notification cancelled: %w",
			err,
		)
	}

	deadline := time.Now().Add(
		emailOperationTimeout,
	)

	if contextDeadline, exists := ctx.Deadline();
		exists && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}

	if err := connection.SetDeadline(deadline); err != nil {
		return fmt.Errorf(
			"set SMTP connection deadline: %w",
			err,
		)
	}

	return nil
}

func smtpOperationError(
	ctx context.Context,
	operation string,
	err error,
) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return fmt.Errorf(
			"%s cancelled or timed out: %w",
			operation,
			contextErr,
		)
	}

	return fmt.Errorf(
		"%s: %w",
		operation,
		err,
	)
}

func requiredEnvironmentValue(
	name string,
	description string,
	trimValue bool,
) (string, error) {
	value, exists := os.LookupEnv(name)
	if !exists {
		return "", fmt.Errorf(
			"%s environment variable %q is not set",
			description,
			name,
		)
	}

	if trimValue {
		value = strings.TrimSpace(value)
	}

	if value == "" {
		return "", fmt.Errorf(
			"%s environment variable %q is empty",
			description,
			name,
		)
	}

	if strings.ContainsRune(value, '\x00') {
		return "", fmt.Errorf(
			"%s environment variable %q contains a null character",
			description,
			name,
		)
	}

	return value, nil
}

func validateEnvironmentVariableName(
	name string,
	description string,
) error {
	name = strings.TrimSpace(name)

	if name == "" {
		return fmt.Errorf(
			"%s name is required",
			description,
		)
	}

	for index, character := range name {
		if index == 0 {
			if character != '_' &&
				!unicode.IsLetter(character) {
				return fmt.Errorf(
					"%s %q is invalid",
					description,
					name,
				)
			}

			continue
		}

		if character != '_' &&
			!unicode.IsLetter(character) &&
			!unicode.IsDigit(character) {
			return fmt.Errorf(
				"%s %q is invalid",
				description,
				name,
			)
		}
	}

	return nil
}

func sanitizeEmailHeaderValue(
	value string,
) string {
	value = strings.TrimSpace(value)

	var builder strings.Builder
	builder.Grow(len(value))

	for _, character := range value {
		if character == '\r' ||
			character == '\n' ||
			character == '\x00' {
			builder.WriteByte(' ')
			continue
		}

		if unicode.IsControl(character) {
			builder.WriteByte(' ')
			continue
		}

		builder.WriteRune(character)
	}

	return strings.Join(
		strings.Fields(builder.String()),
		" ",
	)
}

func containsEmailHeaderControlCharacter(
	value string,
) bool {
	for _, character := range value {
		if character == '\r' ||
			character == '\n' ||
			character == '\x00' ||
			unicode.IsControl(character) {
			return true
		}
	}

	return false
}

func normalizeEmailBody(
	value string,
) string {
	value = strings.ReplaceAll(
		value,
		"\r\n",
		"\n",
	)

	value = strings.ReplaceAll(
		value,
		"\r",
		"\n",
	)

	value = strings.ReplaceAll(
		value,
		"\x00",
		"",
	)

	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(
			value,
			"\uFFFD",
		)
	}

	return strings.ReplaceAll(
		value,
		"\n",
		"\r\n",
	)
}
