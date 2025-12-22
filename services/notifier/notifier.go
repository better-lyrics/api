package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"lyrics-api-go/logcolors"
	"net/http"
	"net/smtp"
	"time"

	log "github.com/sirupsen/logrus"
)

// Notifier interface for different notification methods
type Notifier interface {
	Send(subject, message string) error
}

// =============================================================================
// EMAIL NOTIFIER
// =============================================================================

type EmailNotifier struct {
	SMTPHost     string
	SMTPPort     string
	SMTPUsername string
	SMTPPassword string
	FromEmail    string
	ToEmail      string
}

func (e *EmailNotifier) Send(subject, message string) error {
	auth := smtp.PlainAuth("", e.SMTPUsername, e.SMTPPassword, e.SMTPHost)

	msg := []byte(fmt.Sprintf("From: %s\r\n"+
		"To: %s\r\n"+
		"Subject: %s\r\n"+
		"\r\n"+
		"%s\r\n", e.FromEmail, e.ToEmail, subject, message))

	addr := e.SMTPHost + ":" + e.SMTPPort
	err := smtp.SendMail(addr, auth, e.FromEmail, []string{e.ToEmail}, msg)
	if err != nil {
		return fmt.Errorf("failed to send email: %v", err)
	}

	log.Infof("%s Email notification sent to %s", logcolors.LogNotifier, e.ToEmail)
	return nil
}

// =============================================================================
// TELEGRAM NOTIFIER
// =============================================================================

type TelegramNotifier struct {
	BotToken string
	ChatID   string
}

func (t *TelegramNotifier) Send(subject, message string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.BotToken)

	fullMessage := fmt.Sprintf("*%s*\n\n%s", subject, message)

	payload := map[string]interface{}{
		"chat_id":    t.ChatID,
		"text":       fullMessage,
		"parse_mode": "Markdown",
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal telegram payload: %v", err)
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to send telegram message: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned status %d", resp.StatusCode)
	}

	log.Infof("%s Telegram notification sent to chat %s", logcolors.LogNotifier, t.ChatID)
	return nil
}

// =============================================================================
// NTFY.SH NOTIFIER (Simple Push Notifications)
// =============================================================================

type NtfyNotifier struct {
	Topic  string // Your unique topic name
	Server string // Default: https://ntfy.sh
}

func (n *NtfyNotifier) Send(subject, message string) error {
	server := n.Server
	if server == "" {
		server = "https://ntfy.sh"
	}

	url := fmt.Sprintf("%s/%s", server, n.Topic)

	req, err := http.NewRequest("POST", url, bytes.NewBufferString(message))
	if err != nil {
		return fmt.Errorf("failed to create ntfy request: %v", err)
	}

	req.Header.Set("Title", subject)
	req.Header.Set("Priority", "high")
	req.Header.Set("Tags", "warning")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send ntfy notification: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ntfy returned status %d", resp.StatusCode)
	}

	log.Infof("%s Ntfy notification sent to topic %s", logcolors.LogNotifier, n.Topic)
	return nil
}
