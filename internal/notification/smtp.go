package notification

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	mailpkg "net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

type SMTPConfig struct {
	Host, TLSMode, Username, Password string
	FromAddress, FromName, ReplyTo    string
	Port                              int
	ConnectTimeout, CommandTimeout    time.Duration
}

type SMTPProvider struct{ cfg SMTPConfig }

func NewSMTPProvider(cfg SMTPConfig) (*SMTPProvider, error) {
	if cfg.Host == "" || cfg.Port < 1 || cfg.Port > 65535 || (cfg.TLSMode != "starttls" && cfg.TLSMode != "implicit") || cfg.ConnectTimeout <= 0 || cfg.CommandTimeout <= 0 {
		return nil, errors.New("notification: invalid SMTP configuration")
	}
	if _, err := mailpkg.ParseAddress(cfg.FromAddress); err != nil {
		return nil, errors.New("notification: invalid SMTP from address")
	}
	if cfg.ReplyTo != "" {
		if _, err := mailpkg.ParseAddress(cfg.ReplyTo); err != nil {
			return nil, errors.New("notification: invalid SMTP reply-to")
		}
	}
	return &SMTPProvider{cfg: cfg}, nil
}

func (provider *SMTPProvider) Send(ctx context.Context, message Message) (string, error) {
	if message.Channel != ChannelEmail {
		return "", fmt.Errorf("%w: wrong channel", ErrPermanent)
	}
	recipient, err := mailpkg.ParseAddress(message.Recipient)
	if err != nil || strings.ContainsAny(message.Subject, "\r\n") {
		return "", fmt.Errorf("%w: invalid envelope", ErrPermanent)
	}
	payload, err := smtpMessage(provider.cfg, recipient.Address, message)
	if err != nil {
		return "", fmt.Errorf("%w: message encoding", ErrPermanent)
	}
	address := net.JoinHostPort(provider.cfg.Host, strconv.Itoa(provider.cfg.Port))
	dialer := &net.Dialer{Timeout: provider.cfg.ConnectTimeout}
	tlsConfig := &tls.Config{ServerName: provider.cfg.Host, MinVersion: tls.VersionTLS12}
	var connection net.Conn
	if provider.cfg.TLSMode == "implicit" {
		connection, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", address)
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return "", fmt.Errorf("%w: SMTP connection", ErrTemporary)
	}
	defer func() { _ = connection.Close() }()
	deadline := time.Now().Add(provider.cfg.CommandTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return "", fmt.Errorf("%w: SMTP deadline", ErrTemporary)
	}
	client := textproto.NewConn(connection)
	if _, _, err := client.ReadResponse(220); err != nil {
		return "", classifySMTP(err)
	}
	if err := smtpCommand(client, 250, "EHLO %s", "hackwerk"); err != nil {
		return "", classifySMTP(err)
	}
	if provider.cfg.TLSMode == "starttls" {
		if err := smtpCommand(client, 220, "STARTTLS"); err != nil {
			return "", classifySMTP(err)
		}
		tlsConnection := tls.Client(connection, tlsConfig)
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			return "", fmt.Errorf("%w: SMTP TLS handshake", ErrTemporary)
		}
		connection = tlsConnection
		client = textproto.NewConn(connection)
		if err := smtpCommand(client, 250, "EHLO %s", "hackwerk"); err != nil {
			return "", classifySMTP(err)
		}
	}
	if provider.cfg.Username != "" {
		credentials := base64.StdEncoding.EncodeToString([]byte("\x00" + provider.cfg.Username + "\x00" + provider.cfg.Password))
		if err := smtpCommand(client, 235, "AUTH PLAIN %s", credentials); err != nil {
			return "", classifySMTP(err)
		}
	}
	from, _ := mailpkg.ParseAddress(provider.cfg.FromAddress)
	if err := smtpCommand(client, 250, "MAIL FROM:<%s>", from.Address); err != nil {
		return "", classifySMTP(err)
	}
	if err := smtpCommand(client, 250, "RCPT TO:<%s>", recipient.Address); err != nil {
		return "", classifySMTP(err)
	}
	if err := smtpCommand(client, 354, "DATA"); err != nil {
		return "", classifySMTP(err)
	}
	writer := client.DotWriter()
	if _, err := writer.Write(payload); err != nil {
		_ = writer.Close()
		return "", fmt.Errorf("%w: SMTP data", ErrTemporary)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("%w: SMTP data", ErrTemporary)
	}
	if _, _, err := client.ReadResponse(250); err != nil {
		return "", classifySMTP(err)
	}
	_ = smtpCommand(client, 221, "QUIT")
	return "", nil
}

func smtpCommand(client *textproto.Conn, expectedCode int, format string, args ...any) error {
	id, err := client.Cmd(format, args...)
	if err != nil {
		return err
	}
	client.StartResponse(id)
	defer client.EndResponse(id)
	_, _, err = client.ReadResponse(expectedCode)
	return err
}

func smtpMessage(cfg SMTPConfig, recipient string, message Message) ([]byte, error) {
	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	from := (&mailpkg.Address{Name: cfg.FromName, Address: cfg.FromAddress}).String()
	headers := []string{
		"From: " + from, "To: " + recipient, "Subject: " + mime.QEncoding.Encode("utf-8", message.Subject),
		"MIME-Version: 1.0", "Content-Type: multipart/alternative; boundary=" + strconv.Quote(multipartWriter.Boundary()),
		"Message-ID: <" + message.NotificationID + "@" + cfg.Host + ">",
	}
	if cfg.ReplyTo != "" {
		headers = append(headers, "Reply-To: "+cfg.ReplyTo)
	}
	for _, header := range headers {
		body.WriteString(header + "\r\n")
	}
	body.WriteString("\r\n")
	for _, part := range []struct{ contentType, value string }{{"text/plain; charset=utf-8", message.Text}, {"text/html; charset=utf-8", message.HTML}} {
		partWriter, err := multipartWriter.CreatePart(textproto.MIMEHeader{"Content-Type": {part.contentType}, "Content-Transfer-Encoding": {"quoted-printable"}})
		if err != nil {
			return nil, err
		}
		quoted := quotedprintable.NewWriter(partWriter)
		if _, err := io.WriteString(quoted, part.value); err != nil {
			return nil, err
		}
		if err := quoted.Close(); err != nil {
			return nil, err
		}
	}
	if err := multipartWriter.Close(); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

func classifySMTP(err error) error {
	var protocolError *textproto.Error
	if errors.As(err, &protocolError) && protocolError.Code >= 500 && protocolError.Code < 600 {
		return fmt.Errorf("%w: SMTP rejected", ErrPermanent)
	}
	return fmt.Errorf("%w: SMTP unavailable", ErrTemporary)
}
