package auth

import (
	"fmt"
	"net/smtp"
)

type EmailConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	From     string
}

type EmailSender interface {
	SendOTP(to string, otp string) error
	SendTeamInvite(to string, token string, tenantID string, firmName string) error
}

type SMTPEmailSender struct {
	config EmailConfig
}

func NewSMTPEmailSender(config EmailConfig) *SMTPEmailSender {
	return &SMTPEmailSender{config: config}
}

func (s *SMTPEmailSender) SendOTP(to string, otp string) error {
	addr := fmt.Sprintf("%s:%s", s.config.Host, s.config.Port)
	auth := smtp.PlainAuth("", s.config.User, s.config.Password, s.config.Host)

	msg := []byte(fmt.Sprintf("To: %s\r\n"+
		"Subject: Your OTP Code\r\n"+
		"\r\n"+
		"Your OTP code is: %s\r\n", to, otp))

	err := smtp.SendMail(addr, auth, s.config.From, []string{to}, msg)
	if err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}

	return nil
}

func (s *SMTPEmailSender) SendTeamInvite(to string, token string, tenantID string, firmName string) error {
	addr := fmt.Sprintf("%s:%s", s.config.Host, s.config.Port)
	auth := smtp.PlainAuth("", s.config.User, s.config.Password, s.config.Host)

	msg := []byte(fmt.Sprintf("To: %s\r\n"+
		"Subject: Invitation to join %s\r\n"+
		"\r\n"+
		"You have been invited to join the firm %s.\r\n"+
		"Your invitation token is: %s\r\n", to, firmName, firmName, token))

	err := smtp.SendMail(addr, auth, s.config.From, []string{to}, msg)
	if err != nil {
		return fmt.Errorf("failed to send team invite email: %w", err)
	}

	return nil
}
