package common

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

type SMTPConfig struct {
	Host        string
	Port        int
	Username    string
	Password    string
	TLSMode     string
	FromName    string
	FromAddress string
	Timeout     time.Duration
}

// SendEmail keeps the original call shape while using certificate-validated
// TLS and a bounded SMTP session.
func SendEmail(subject string, receiver string, content string) error {
	tlsMode := "starttls"
	if SMTPPort == 465 {
		tlsMode = "tls"
	}
	config := SMTPConfig{
		Host:        SMTPServer,
		Port:        SMTPPort,
		Username:    SMTPAccount,
		Password:    SMTPToken,
		TLSMode:     tlsMode,
		FromName:    SystemName,
		FromAddress: SMTPAccount,
		Timeout:     30 * time.Second,
	}
	return SendEmailContext(context.Background(), config, subject, receiver, "", content)
}

// SendEmailContext sends to one or more comma, semicolon, or pipe separated
// recipients. If both body forms are present it builds a multipart/alternative
// message so clients can choose HTML or plain text.
func SendEmailContext(ctx context.Context, config SMTPConfig, subject, receivers, textBody, htmlBody string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	config.Host = strings.TrimSpace(config.Host)
	config.Username = strings.TrimSpace(config.Username)
	if !validSMTPHost(config.Host) || config.Port < 1 || config.Port > 65535 {
		return NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("SMTP host and a valid port are required"))
	}
	if config.TLSMode == "" {
		if config.Port == 465 {
			config.TLSMode = "tls"
		} else {
			config.TLSMode = "starttls"
		}
	}
	if config.TLSMode != "tls" && config.TLSMode != "starttls" && config.TLSMode != "none" {
		return NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("SMTP TLS mode must be tls, starttls, or none"))
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	if config.Timeout > 2*time.Minute {
		return NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("SMTP timeout cannot exceed two minutes"))
	}
	if config.Username != "" && config.Password == "" {
		return NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("SMTP password is required when a username is configured"))
	}
	if config.Username != "" && config.Password != "" && config.TLSMode == "none" {
		return NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("SMTP authentication requires TLS"))
	}
	addresses, err := parseSMTPRecipients(receivers)
	if err != nil {
		return NewDeliveryError("invalid_target", "", false, false, 0, 0, err)
	}
	if textBody == "" && htmlBody == "" {
		return NewDeliveryError("invalid_request", "", false, false, 0, 0, errors.New("email body is empty"))
	}
	fromAddress := config.FromAddress
	if fromAddress == "" {
		fromAddress = config.Username
	}
	from, err := mail.ParseAddress(fromAddress)
	if err != nil || from.Address == "" {
		return NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("SMTP From address is invalid"))
	}
	if strings.ContainsAny(subject, "\r\n") {
		return NewDeliveryError("invalid_request", "", false, false, 0, 0, errors.New("email subject contains a line break"))
	}
	if strings.ContainsAny(config.FromName, "\r\n") {
		return NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("SMTP From name contains a line break"))
	}
	if config.FromName != "" {
		from.Name = config.FromName
	}
	message, err := buildSMTPMessage(subject, addresses, from, textBody, htmlBody)
	if err != nil {
		return NewDeliveryError("invalid_request", "", false, false, 0, 0, err)
	}
	operationCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	address := net.JoinHostPort(config.Host, strconv.Itoa(config.Port))
	var conn net.Conn
	if config.TLSMode == "tls" {
		dialer := &net.Dialer{Timeout: config.Timeout}
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: config.Host, MinVersion: tls.VersionTLS12}}).DialContext(operationCtx, "tcp", address)
	} else {
		conn, err = (&net.Dialer{Timeout: config.Timeout}).DialContext(operationCtx, "tcp", address)
	}
	if err != nil {
		return classifySMTPError(err, false)
	}
	defer conn.Close()
	if deadline, ok := operationCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stopClose := closeConnOnContext(operationCtx, conn)
	defer stopClose()
	client, err := smtp.NewClient(conn, config.Host)
	if err != nil {
		return classifySMTPError(err, false)
	}
	defer client.Close()
	if config.TLSMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return NewDeliveryError("tls", "", false, false, 0, 0, errors.New("SMTP server does not advertise STARTTLS"))
		}
		if err = client.StartTLS(&tls.Config{ServerName: config.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return classifySMTPError(err, false)
		}
	}
	if config.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", config.Username, config.Password, config.Host)); err != nil {
			return classifySMTPError(err, false)
		}
	}
	if err = client.Mail(from.Address); err != nil {
		return classifySMTPError(err, false)
	}
	for _, address := range addresses {
		if err = client.Rcpt(address.Address); err != nil {
			return classifySMTPError(err, false)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return classifySMTPError(err, false)
	}
	if _, err = io.Copy(writer, bytes.NewReader(message)); err != nil {
		_ = writer.Close()
		return classifySMTPError(err, true)
	}
	if err = writer.Close(); err != nil {
		return classifySMTPError(err, true)
	}
	return nil
}

func validSMTPHost(host string) bool {
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if strings.ContainsAny(host, " \t\r\n:/\\@[]") || len(host) > 253 {
		return false
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

func parseSMTPRecipients(value string) ([]*mail.Address, error) {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer(";", ",", "|", ",").Replace(value)
	if value == "" {
		return nil, errors.New("at least one email recipient is required")
	}
	addresses, err := mail.ParseAddressList(value)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("email recipient list is invalid")
	}
	for _, address := range addresses {
		if address == nil || address.Address == "" || strings.ContainsAny(address.Address, "\r\n") {
			return nil, errors.New("email recipient list is invalid")
		}
	}
	return addresses, nil
}

func buildSMTPMessage(subject string, recipients []*mail.Address, from *mail.Address, textBody, htmlBody string) ([]byte, error) {
	var body bytes.Buffer
	headerTo := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		headerTo = append(headerTo, recipient.String())
	}
	fromHeader := from.String()
	if from.Name != "" {
		fromHeader = (&mail.Address{Name: from.Name, Address: from.Address}).String()
	}
	encodedSubject := mime.QEncoding.Encode("UTF-8", subject)
	fmt.Fprintf(&body, "To: %s\r\nFrom: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nDate: %s\r\n", strings.Join(headerTo, ", "), fromHeader, encodedSubject, time.Now().UTC().Format(time.RFC1123Z))
	if textBody != "" && htmlBody != "" {
		multipartWriter := multipart.NewWriter(&body)
		fmt.Fprintf(&body, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", multipartWriter.Boundary())
		textHeader := textproto.MIMEHeader{}
		textHeader.Set("Content-Type", "text/plain; charset=UTF-8")
		textHeader.Set("Content-Transfer-Encoding", "8bit")
		textPart, err := multipartWriter.CreatePart(textHeader)
		if err != nil {
			return nil, err
		}
		if _, err = io.WriteString(textPart, normalizeCRLF(textBody)); err != nil {
			return nil, err
		}
		htmlHeader := textproto.MIMEHeader{}
		htmlHeader.Set("Content-Type", "text/html; charset=UTF-8")
		htmlHeader.Set("Content-Transfer-Encoding", "8bit")
		htmlPart, err := multipartWriter.CreatePart(htmlHeader)
		if err != nil {
			return nil, err
		}
		if _, err = io.WriteString(htmlPart, normalizeCRLF(htmlBody)); err != nil {
			return nil, err
		}
		if err = multipartWriter.Close(); err != nil {
			return nil, err
		}
		return body.Bytes(), nil
	}
	if htmlBody != "" {
		body.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
		body.WriteString(normalizeCRLF(htmlBody))
	} else {
		body.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		body.WriteString(normalizeCRLF(textBody))
	}
	body.WriteString("\r\n")
	return body.Bytes(), nil
}

func normalizeCRLF(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
}

func closeConnOnContext(ctx context.Context, conn net.Conn) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

func classifySMTPError(err error, messageDataMayHaveBeenAccepted bool) error {
	if err == nil {
		return nil
	}
	newDeliveryError := func(category, providerCode string, retryable, unknown bool, retryAfter time.Duration, httpStatus int, cause error) error {
		return NewDeliveryErrorWithAttempted(category, providerCode, retryable, unknown, messageDataMayHaveBeenAccepted, retryAfter, httpStatus, cause)
	}
	var responseErr *textproto.Error
	if errors.As(err, &responseErr) {
		code := strconv.Itoa(responseErr.Code)
		retryable := responseErr.Code >= 400 && responseErr.Code < 500
		category := "smtp_permanent"
		if retryable {
			category = "smtp_temporary"
		}
		return newDeliveryError(category, code, retryable, false, 0, 0, fmt.Errorf("SMTP server returned status %d", responseErr.Code))
	}
	if errors.Is(err, context.Canceled) {
		return newDeliveryError("cancelled", "", false, messageDataMayHaveBeenAccepted, 0, 0, errors.New("SMTP delivery was cancelled"))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return newDeliveryError("timeout", "", !messageDataMayHaveBeenAccepted, messageDataMayHaveBeenAccepted, 0, 0, errors.New("SMTP delivery timed out"))
	}
	var certificateErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	if errors.As(err, &certificateErr) || errors.As(err, &unknownAuthority) || errors.As(err, &hostnameErr) {
		return newDeliveryError("tls", "", false, false, 0, 0, errors.New("SMTP TLS certificate validation failed"))
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return newDeliveryError("dns", "", true, false, 0, 0, errors.New("SMTP host DNS lookup failed"))
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return newDeliveryError("timeout", "", !messageDataMayHaveBeenAccepted, messageDataMayHaveBeenAccepted, 0, 0, errors.New("SMTP network operation timed out"))
	}
	if messageDataMayHaveBeenAccepted {
		return newDeliveryError("delivery_unknown", "", false, true, 0, 0, errors.New("SMTP connection ended after message data was sent; delivery is unknown"))
	}
	return newDeliveryError("network", "", true, false, 0, 0, errors.New("SMTP connection failed"))
}
