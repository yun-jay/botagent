package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// TelegramAlerter implements Alerter via the Telegram Bot API.
type TelegramAlerter struct {
	botToken string
	chatID   string
	client   *http.Client
	log      *slog.Logger
	baseURL  string
}

// NewTelegram creates a new TelegramAlerter.
func NewTelegram(botToken, chatID string, logger *slog.Logger) *TelegramAlerter {
	return &TelegramAlerter{
		botToken: botToken,
		chatID:   chatID,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		log:     logger,
		baseURL: "https://api.telegram.org",
	}
}

// newTelegramWithBaseURL creates a TelegramAlerter with a custom base URL (for testing).
func newTelegramWithBaseURL(botToken, chatID, baseURL string, logger *slog.Logger) *TelegramAlerter {
	ta := NewTelegram(botToken, chatID, logger)
	ta.baseURL = baseURL
	return ta
}

// Send delivers a markdown-formatted message via Telegram.
func (ta *TelegramAlerter) Send(ctx context.Context, text string) error {
	if !ta.IsConfigured() {
		ta.log.Debug("telegram not configured, skipping alert")
		return nil
	}

	apiURL := fmt.Sprintf("%s/bot%s/sendMessage", ta.baseURL, ta.botToken)
	params := url.Values{
		"chat_id":    {ta.chatID},
		"text":       {text},
		"parse_mode": {"Markdown"},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL+"?"+params.Encode(), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := ta.client.Do(req)
	if err != nil {
		return fmt.Errorf("send telegram message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		var errResp struct {
			OK          bool   `json:"ok"`
			Description string `json:"description"`
		}
		_ = json.Unmarshal(body, &errResp)
		return fmt.Errorf("telegram API error %d: %s", resp.StatusCode, errResp.Description)
	}

	return nil
}

// IsConfigured returns true if both bot token and chat ID are set.
func (ta *TelegramAlerter) IsConfigured() bool {
	return ta.botToken != "" && ta.chatID != ""
}
