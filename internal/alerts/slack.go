package alerts

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"db-restore-automation/internal/config"
)

const (
	slackRequestTimeout         = 10 * time.Second
	slackDialTimeout            = 5 * time.Second
	slackTLSHandshakeTimeout    = 5 * time.Second
	slackResponseHeaderTimeout  = 8 * time.Second
	slackMaximumMessageBytes    = 30 * 1024
	slackMaximumResponseBytes   = 4 * 1024
	slackWebhookUserAgent       = "db-restore-automation/1.0"
)

type SlackNotifier struct {
	Config config.SlackConfig
}

type slackWebhookPayload struct {
	Text        string `json:"text"`
	UnfurlLinks bool   `json:"unfurl_links"`
	UnfurlMedia bool   `json:"unfurl_media"`
}

func (n SlackNotifier) Notify(
	ctx context.Context,
	event Event,
) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"Slack notification cancelled before validation: %w",
			err,
		)
	}

	preparedEvent, err := PrepareEvent(event)
	if err != nil {
		return fmt.Errorf(
			"invalid Slack alert event: %w",
			err,
		)
	}

	webhookEnvironmentName := strings.TrimSpace(
		n.Config.WebhookURLEnv,
	)

	if err := validateSlackEnvironmentVariableName(
		webhookEnvironmentName,
	); err != nil {
		return err
	}

	rawWebhookURL, err := requiredSlackEnvironmentValue(
		webhookEnvironmentName,
	)
	if err != nil {
		return err
	}

	webhookURL, err := validateSlackWebhookURL(
		rawWebhookURL,
	)
	if err != nil {
		return err
	}

	message := formatEvent(preparedEvent)
	message = preventSlackMentions(message)
	message = truncateSlackMessage(
		message,
		slackMaximumMessageBytes,
	)

	payload := slackWebhookPayload{
		Text:        message,
		UnfurlLinks: false,
		UnfurlMedia: false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf(
			"encode Slack webhook payload: %w",
			err,
		)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		webhookURL,
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf(
			"create Slack webhook request: %w",
			sanitizeSlackHTTPError(err),
		)
	}

	request.Header.Set(
		"Content-Type",
		"application/json; charset=utf-8",
	)

	request.Header.Set(
		"Accept",
		"text/plain, application/json",
	)

	request.Header.Set(
		"User-Agent",
		slackWebhookUserAgent,
	)

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,

		DialContext: (&net.Dialer{
			Timeout:   slackDialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,

		ForceAttemptHTTP2: true,

		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},

		TLSHandshakeTimeout: slackTLSHandshakeTimeout,

		ResponseHeaderTimeout: slackResponseHeaderTimeout,

		ExpectContinueTimeout: time.Second,

		IdleConnTimeout: 30 * time.Second,

		MaxIdleConns:        2,
		MaxIdleConnsPerHost: 1,

		MaxResponseHeaderBytes: 32 * 1024,
	}

	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport: transport,
		Timeout:   slackRequestTimeout,

		// Never follow redirects for a webhook request. Following a redirect
		// could send the alert payload to an unintended destination and may
		// expose the secret webhook URL through request errors or proxies.
		CheckRedirect: func(
			request *http.Request,
			via []*http.Request,
		) error {
			return http.ErrUseLastResponse
		},
	}

	response, err := client.Do(request)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return fmt.Errorf(
				"Slack notification cancelled or timed out: %w",
				contextErr,
			)
		}

		return fmt.Errorf(
			"send Slack webhook request: %w",
			sanitizeSlackHTTPError(err),
		)
	}

	responseBody, responseErr := readSlackResponseBody(
		response.Body,
		slackMaximumResponseBytes,
	)
	if responseErr != nil {
		return fmt.Errorf(
			"read Slack webhook response: %w",
			responseErr,
		)
	}

	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		responseText := sanitizeSlackResponseText(
			responseBody,
		)

		if responseText == "" {
			return fmt.Errorf(
				"Slack webhook returned HTTP status %s",
				response.Status,
			)
		}

		return fmt.Errorf(
			"Slack webhook returned HTTP status %s: %s",
			response.Status,
			responseText,
		)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"Slack notification context ended after sending: %w",
			err,
		)
	}

	return nil
}

func validateSlackWebhookURL(
	value string,
) (string, error) {
	value = strings.TrimSpace(value)

	if value == "" {
		return "", fmt.Errorf(
			"Slack webhook URL is empty",
		)
	}

	if strings.ContainsRune(value, '\x00') ||
		strings.ContainsAny(value, "\r\n\t ") {
		return "", fmt.Errorf(
			"Slack webhook URL must not contain whitespace or control characters",
		)
	}

	parsedURL, err := url.ParseRequestURI(value)
	if err != nil {
		return "", fmt.Errorf(
			"Slack webhook URL is invalid",
		)
	}

	if !parsedURL.IsAbs() {
		return "", fmt.Errorf(
			"Slack webhook URL must be absolute",
		)
	}

	if !strings.EqualFold(
		parsedURL.Scheme,
		"https",
	) {
		return "", fmt.Errorf(
			"Slack webhook URL must use HTTPS",
		)
	}

	if strings.TrimSpace(parsedURL.Hostname()) == "" {
		return "", fmt.Errorf(
			"Slack webhook URL must contain a hostname",
		)
	}

	if parsedURL.User != nil {
		return "", fmt.Errorf(
			"Slack webhook URL must not contain user information",
		)
	}

	if parsedURL.Fragment != "" ||
		parsedURL.RawFragment != "" {
		return "", fmt.Errorf(
			"Slack webhook URL must not contain a fragment",
		)
	}

	if strings.TrimSpace(parsedURL.Path) == "" ||
		parsedURL.Path == "/" {
		return "", fmt.Errorf(
			"Slack webhook URL must contain a webhook path",
		)
	}

	return parsedURL.String(), nil
}

func validateSlackEnvironmentVariableName(
	name string,
) error {
	name = strings.TrimSpace(name)

	if name == "" {
		return fmt.Errorf(
			"Slack webhook environment variable name is required",
		)
	}

	for index, character := range name {
		isLetter := (character >= 'A' && character <= 'Z') ||
			(character >= 'a' && character <= 'z')

		isDigit := character >= '0' && character <= '9'

		if index == 0 {
			if !isLetter && character != '_' {
				return fmt.Errorf(
					"Slack webhook environment variable name %q is invalid",
					name,
				)
			}

			continue
		}

		if !isLetter &&
			!isDigit &&
			character != '_' {
			return fmt.Errorf(
				"Slack webhook environment variable name %q is invalid",
				name,
			)
		}
	}

	return nil
}

func requiredSlackEnvironmentValue(
	name string,
) (string, error) {
	value, exists := os.LookupEnv(name)
	if !exists {
		return "", fmt.Errorf(
			"Slack webhook environment variable %q is not set",
			name,
		)
	}

	value = strings.TrimSpace(value)

	if value == "" {
		return "", fmt.Errorf(
			"Slack webhook environment variable %q is empty",
			name,
		)
	}

	if strings.ContainsRune(value, '\x00') {
		return "", fmt.Errorf(
			"Slack webhook environment variable %q contains a null character",
			name,
		)
	}

	return value, nil
}

func readSlackResponseBody(
	body io.ReadCloser,
	maximumBytes int64,
) ([]byte, error) {
	if body == nil {
		return nil, fmt.Errorf(
			"Slack webhook response body is nil",
		)
	}

	if maximumBytes <= 0 {
		maximumBytes = slackMaximumResponseBytes
	}

	responseBody, readErr := io.ReadAll(
		io.LimitReader(
			body,
			maximumBytes+1,
		),
	)

	closeErr := body.Close()

	if readErr != nil || closeErr != nil {
		return nil, errors.Join(
			readErr,
			closeErr,
		)
	}

	if int64(len(responseBody)) > maximumBytes {
		responseBody = responseBody[:maximumBytes]
	}

	return responseBody, nil
}

func sanitizeSlackHTTPError(
	err error,
) error {
	if err == nil {
		return nil
	}

	var urlError *url.Error

	if errors.As(err, &urlError) {
		if urlError.Err != nil {
			return fmt.Errorf(
				"%s request failed: %w",
				strings.TrimSpace(urlError.Op),
				urlError.Err,
			)
		}

		return fmt.Errorf(
			"%s request failed",
			strings.TrimSpace(urlError.Op),
		)
	}

	return err
}

func sanitizeSlackResponseText(
	value []byte,
) string {
	text := strings.ToValidUTF8(
		string(value),
		"\uFFFD",
	)

	var builder strings.Builder
	builder.Grow(len(text))

	for _, character := range text {
		switch {
		case character == '\r',
			character == '\n',
			character == '\t':
			builder.WriteByte(' ')

		case character == '\x00':
			continue

		case unicode.IsControl(character):
			builder.WriteByte(' ')

		default:
			builder.WriteRune(character)
		}
	}

	return strings.Join(
		strings.Fields(builder.String()),
		" ",
	)
}

func preventSlackMentions(
	value string,
) string {
	replacer := strings.NewReplacer(
		"@channel",
		"@\u200Bchannel",

		"@here",
		"@\u200Bhere",

		"@everyone",
		"@\u200Beveryone",

		"<@",
		"<@\u200B",

		"<!",
		"<!\u200B",
	)

	return replacer.Replace(value)
}

func truncateSlackMessage(
	value string,
	maximumBytes int,
) string {
	if maximumBytes <= 0 ||
		len(value) <= maximumBytes {
		return value
	}

	const suffix = "\n… [message truncated]"

	availableBytes := maximumBytes - len(suffix)
	if availableBytes <= 0 {
		return suffix
	}

	if availableBytes > len(value) {
		availableBytes = len(value)
	}

	for availableBytes > 0 &&
		!utf8.ValidString(value[:availableBytes]) {
		availableBytes--
	}

	return value[:availableBytes] + suffix
}

func formatEvent(
	event Event,
) string {
	event = event.Normalize()

	result := strings.ToUpper(
		strings.TrimSpace(event.Result),
	)

	if result == "" {
		result = "UNKNOWN"
	}

	var builder strings.Builder

	fmt.Fprintf(
		&builder,
		"DB restore %s\n",
		result,
	)

	fmt.Fprintf(
		&builder,
		"Job: %s\n",
		formatAlertValue(event.JobName),
	)

	fmt.Fprintf(
		&builder,
		"Type: %s\n",
		formatAlertValue(event.JobType),
	)

	fmt.Fprintf(
		&builder,
		"Source: %s\n",
		formatAlertValue(event.Source),
	)

	fmt.Fprintf(
		&builder,
		"Target: %s\n",
		formatAlertValue(event.Target),
	)

	fmt.Fprintf(
		&builder,
		"Dry run: %t\n",
		event.DryRun,
	)

	fmt.Fprintf(
		&builder,
		"Started: %s\n",
		formatAlertTime(event.StartedAt),
	)

	fmt.Fprintf(
		&builder,
		"Finished: %s\n",
		formatAlertTime(event.FinishedAt),
	)

	fmt.Fprintf(
		&builder,
		"Duration: %s\n",
		event.EffectiveDuration().Round(
			time.Millisecond,
		),
	)

	fmt.Fprintf(
		&builder,
		"Host: %s\n",
		formatAlertValue(event.Host),
	)

	fmt.Fprintf(
		&builder,
		"Main log: %s",
		formatAlertValue(event.MainLogFile),
	)

	if strings.TrimSpace(event.ProviderLog) != "" {
		fmt.Fprintf(
			&builder,
			"\nProvider log: %s",
			formatAlertValue(event.ProviderLog),
		)
	}

	if strings.TrimSpace(event.Error) != "" {
		fmt.Fprintf(
			&builder,
			"\nError: %s",
			formatAlertValue(event.Error),
		)
	}

	return builder.String()
}

func formatAlertTime(
	value time.Time,
) string {
	if value.IsZero() {
		return "not set"
	}

	return value.UTC().Format(
		time.RFC3339,
	)
}

func formatAlertValue(
	value string,
) string {
	value = strings.TrimSpace(value)

	if value == "" {
		return "not set"
	}

	value = strings.ToValidUTF8(
		value,
		"\uFFFD",
	)

	var builder strings.Builder
	builder.Grow(len(value))

	for _, character := range value {
		switch character {
		case '\r':
			builder.WriteString(`\r`)

		case '\n':
			builder.WriteString(`\n`)

		case '\t':
			builder.WriteString(`\t`)

		case '\x00':
			builder.WriteString(`\u0000`)

		default:
			if unicode.IsControl(character) {
				fmt.Fprintf(
					&builder,
					`\u%04X`,
					character,
				)

				continue
			}

			builder.WriteRune(character)
		}
	}

	return builder.String()
}

