package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/kubestellar/hive/v2/pkg/config"
)

const httpTimeoutSeconds = 10

type Notifier struct {
	cfg    config.NotificationsConfig
	hiveID string
	client *http.Client
	logger *slog.Logger
}

func New(cfg config.NotificationsConfig, logger *slog.Logger) *Notifier {
	return &Notifier{
		cfg: cfg,
		client: &http.Client{
			Timeout: httpTimeoutSeconds * time.Second,
		},
		logger: logger,
	}
}

// SetHiveID configures the Hive instance ID to prefix notification titles.
func (n *Notifier) SetHiveID(id string) {
	n.hiveID = id
}

type Priority string

const (
	PriorityHigh    Priority = "high"
	PriorityDefault Priority = "default"
	PriorityLow     Priority = "low"
)

func (n *Notifier) Send(title, message string, priority Priority) {
	if n.hiveID != "" {
		title = fmt.Sprintf("[%s] %s", n.hiveID, title)
	}
	if n.cfg.Ntfy != nil {
		go n.sendNtfy(title, message, priority)
	}
	if n.cfg.Slack != nil {
		go n.sendSlack(title, message)
	}
	if n.cfg.Discord != nil && n.cfg.Discord.Webhook != "" {
		go n.sendDiscordWebhook(title, message)
	}
}

func (n *Notifier) sendNtfy(title, message string, priority Priority) {
	url := fmt.Sprintf("%s/%s", n.cfg.Ntfy.Server, n.cfg.Ntfy.Topic)

	req, err := http.NewRequest("POST", url, bytes.NewBufferString(message))
	if err != nil {
		n.logger.Warn("ntfy request creation failed", "error", err)
		return
	}

	req.Header.Set("Title", title)
	req.Header.Set("Priority", string(priority))

	resp, err := n.client.Do(req)
	if err != nil {
		n.logger.Warn("ntfy send failed", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		n.logger.Warn("ntfy returned error", "status", resp.StatusCode)
	}
}

func (n *Notifier) sendSlack(title, message string) {
	payload := map[string]string{
		"text": fmt.Sprintf("*%s*\n%s", title, message),
	}

	body, _ := json.Marshal(payload)
	resp, err := n.client.Post(n.cfg.Slack.Webhook, "application/json", bytes.NewReader(body))
	if err != nil {
		n.logger.Warn("slack send failed", "error", err)
		return
	}
	defer resp.Body.Close()
}

func (n *Notifier) sendDiscordWebhook(title, message string) {
	payload := map[string]string{
		"content": fmt.Sprintf("**%s**\n%s", title, message),
	}

	body, _ := json.Marshal(payload)
	resp, err := n.client.Post(n.cfg.Discord.Webhook, "application/json", bytes.NewReader(body))
	if err != nil {
		n.logger.Warn("discord webhook send failed", "error", err)
		return
	}
	defer resp.Body.Close()
}
