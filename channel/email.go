package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"message-pusher/common"
	"message-pusher/model"
	"strconv"
	"strings"
	"time"
)

type emailChannelConfig struct {
	TLSMode        string `json:"tls_mode"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	HTML           *bool  `json:"html"`
	FromName       string `json:"from_name"`
	FromAddress    string `json:"from_address"`
}

func SendEmailMessage(message *model.Message, user *model.User, channel_ *model.Channel) error {
	return SendEmailMessageContext(context.Background(), message, user, channel_)
}

func SendEmailMessageContext(ctx context.Context, message *model.Message, user *model.User, channel_ *model.Channel) error {
	if message == nil || user == nil || channel_ == nil {
		return common.NewDeliveryError("invalid_request", "", false, false, 0, 0, errors.New("message, user, and channel are required"))
	}
	to := strings.TrimSpace(message.To)
	if to != "" {
		if user.SendEmailToOthers != common.SendEmailToOthersAllowed && user.Role < common.RoleAdminUser {
			return common.NewDeliveryError("permission", "", false, false, 0, 0, errors.New("sending email to other recipients is not permitted"))
		}
	} else {
		to = strings.TrimSpace(user.Email)
	}
	if to == "" {
		return common.NewDeliveryError("invalid_target", "", false, false, 0, 0, errors.New("no email recipient is configured"))
	}
	config, err := resolveSMTPChannelConfig(channel_)
	if err != nil {
		return err
	}
	subject := strings.TrimSpace(message.Title)
	content := message.Content
	if subject == common.SystemName || subject == "" {
		subject = message.Description
	} else if message.Description != "" {
		content = fmt.Sprintf("%s\n\n%s", message.Description, message.Content)
	}
	if subject == "" {
		subject = common.SystemName
	}
	htmlContent := ""
	if config.htmlEnabled() {
		htmlContent, err = common.Markdown2HTML(content)
		if err != nil {
			return common.NewDeliveryError("invalid_request", "", false, false, 0, 0, errors.New("email content could not be rendered as HTML"))
		}
	}
	return common.SendEmailContext(ctx, config.smtp, subject, to, content, htmlContent)
}

type resolvedSMTPChannelConfig struct {
	smtp common.SMTPConfig
	html *bool
}

func (config resolvedSMTPChannelConfig) htmlEnabled() bool {
	return config.html == nil || *config.html
}

func resolveSMTPChannelConfig(channel_ *model.Channel) (resolvedSMTPChannelConfig, error) {
	var channelConfig emailChannelConfig
	if value := strings.TrimSpace(channel_.Other); value != "" {
		if err := json.Unmarshal([]byte(value), &channelConfig); err != nil {
			return resolvedSMTPChannelConfig{}, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("invalid SMTP channel configuration"))
		}
	}
	if channelConfig.TimeoutSeconds < 0 || channelConfig.TimeoutSeconds > 120 {
		return resolvedSMTPChannelConfig{}, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("SMTP timeout_seconds must be 0 or between 1 and 120"))
	}
	customSMTP := channel_.URL != "" || channel_.AppId != "" || channel_.AccountId != "" || channel_.Secret != ""
	config := common.SMTPConfig{
		Host:        common.SMTPServer,
		Port:        common.SMTPPort,
		Username:    common.SMTPAccount,
		Password:    common.SMTPToken,
		FromName:    common.SystemName,
		FromAddress: common.SMTPAccount,
		Timeout:     30 * time.Second,
	}
	if customSMTP {
		config.Host = strings.TrimSpace(channel_.URL)
		config.Username = strings.TrimSpace(channel_.AppId)
		config.Password = channel_.Secret
		if config.Host == "" || strings.Contains(config.Host, "://") || strings.ContainsAny(config.Host, "/\\\r\n@") {
			return resolvedSMTPChannelConfig{}, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("SMTP channel URL must contain a hostname only"))
		}
		if channel_.AccountId != "" {
			port, err := strconv.Atoi(strings.TrimSpace(channel_.AccountId))
			if err != nil || port < 1 || port > 65535 {
				return resolvedSMTPChannelConfig{}, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("SMTP channel account_id must contain a valid port"))
			}
			config.Port = port
		}
		if config.Username == "" || config.Password == "" {
			return resolvedSMTPChannelConfig{}, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom SMTP channels require app_id username and Secret password"))
		}
		config.FromAddress = config.Username
	}
	if config.Port == 465 {
		config.TLSMode = "tls"
	} else {
		config.TLSMode = "starttls"
	}
	if channelConfig.TLSMode != "" {
		config.TLSMode = strings.ToLower(strings.TrimSpace(channelConfig.TLSMode))
	}
	if channelConfig.TimeoutSeconds > 0 {
		config.Timeout = time.Duration(channelConfig.TimeoutSeconds) * time.Second
	}
	if channelConfig.FromName != "" {
		config.FromName = channelConfig.FromName
	}
	if channelConfig.FromAddress != "" {
		config.FromAddress = channelConfig.FromAddress
	}
	return resolvedSMTPChannelConfig{smtp: config, html: channelConfig.HTML}, nil
}
