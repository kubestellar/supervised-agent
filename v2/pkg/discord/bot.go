package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	discordAPIBase    = "https://discord.com/api/v10"
	gatewayURL        = "wss://gateway.discord.gg/?v=10&encoding=json"
	heartbeatJitterMs = 500
	httpTimeoutS      = 10
)

type Bot struct {
	token     string
	channelID string
	commands  map[string]CommandHandler
	mu        sync.RWMutex
	logger    *slog.Logger
	client    *http.Client
}

type CommandHandler func(ctx context.Context, args string) (string, error)

type Config struct {
	Token     string
	ChannelID string
}

func NewBot(cfg Config, logger *slog.Logger) *Bot {
	return &Bot{
		token:     cfg.Token,
		channelID: cfg.ChannelID,
		commands:  make(map[string]CommandHandler),
		logger:    logger,
		client: &http.Client{
			Timeout: httpTimeoutS * time.Second,
		},
	}
}

func (b *Bot) RegisterCommand(name string, handler CommandHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.commands[name] = handler
}

func (b *Bot) Start(ctx context.Context) error {
	if b.token == "" {
		return fmt.Errorf("discord bot token not configured")
	}

	b.logger.Info("discord bot starting", "channel", b.channelID)

	go b.pollLoop(ctx)
	return nil
}

func (b *Bot) SendMessage(content string) error {
	payload := map[string]string{"content": content}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/channels/%s/messages", discordAPIBase, b.channelID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bot "+b.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("discord send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord API %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (b *Bot) pollLoop(ctx context.Context) {
	const pollIntervalS = 5
	ticker := time.NewTicker(pollIntervalS * time.Second)
	defer ticker.Stop()

	var lastMessageID string

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			messages, err := b.fetchMessages(lastMessageID)
			if err != nil {
				b.logger.Warn("discord poll failed", "error", err)
				continue
			}

			for i := len(messages) - 1; i >= 0; i-- {
				msg := messages[i]
				lastMessageID = msg.ID
				b.handleMessage(ctx, msg)
			}
		}
	}
}

type discordMessage struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Author  struct {
		ID  string `json:"id"`
		Bot bool   `json:"bot"`
	} `json:"author"`
}

func (b *Bot) fetchMessages(after string) ([]discordMessage, error) {
	url := fmt.Sprintf("%s/channels/%s/messages?limit=10", discordAPIBase, b.channelID)
	if after != "" {
		url += "&after=" + after
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bot "+b.token)

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("discord API %d: %s", resp.StatusCode, string(body))
	}

	var messages []discordMessage
	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		return nil, err
	}

	return messages, nil
}

func (b *Bot) handleMessage(ctx context.Context, msg discordMessage) {
	if msg.Author.Bot {
		return
	}

	content := strings.TrimSpace(msg.Content)
	if !strings.HasPrefix(content, "!hive ") {
		return
	}

	parts := strings.SplitN(content[6:], " ", 2)
	cmdName := parts[0]
	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}

	b.mu.RLock()
	handler, ok := b.commands[cmdName]
	b.mu.RUnlock()

	if !ok {
		if err := b.SendMessage(fmt.Sprintf("Unknown command: `%s`. Try `!hive help`", cmdName)); err != nil {
			b.logger.Warn("discord reply failed", "error", err)
		}
		return
	}

	reply, err := handler(ctx, args)
	if err != nil {
		reply = fmt.Sprintf("Error: %s", err)
	}

	if err := b.SendMessage(reply); err != nil {
		b.logger.Warn("discord reply failed", "error", err)
	}
}
