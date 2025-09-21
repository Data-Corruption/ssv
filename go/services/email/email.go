package email

import (
	"context"
	"net/mail"
	"net/smtp"
	"ssv/go/database/config"

	"github.com/Data-Corruption/stdx/xhttp"
)

const (
	smtpServer = "smtp.gmail.com"
	smtpPort   = "587"
)

var ErrNotConfigured = &xhttp.Err{Code: 500, Msg: "email service not configured", Err: nil}

// GetConfig retrieves the email sender and password from the config.
func GetConfig(ctx context.Context) (string, string, error) {
	var err error
	var sender, pass string
	if sender, err = config.Get[string](ctx, "emailSender"); err != nil {
		return "", "", err
	}
	if pass, err = config.Get[string](ctx, "emailPassword"); err != nil {
		return "", "", err
	}
	if sender == "" || pass == "" {
		return "", "", ErrNotConfigured
	}
	return sender, pass, nil
}

// IsAddressValid checks if the given email is valid.
// It does not check if the email is already taken.
func IsAddressValid(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}

// SendEmail sends an email to the specified email address.
func SendEmail(ctx context.Context, to, subject, body string) error {
	sender, pass, err := GetConfig(ctx)
	if err != nil {
		return err
	}

	// setup message
	message := []byte("To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" +
		body + "\r\n")

	// SMTP server configuration.
	auth := smtp.PlainAuth("", sender, pass, smtpServer)

	// TLS connection to send the email
	addr := smtpServer + ":" + smtpPort
	return smtp.SendMail(addr, auth, sender, []string{to}, message)
}
