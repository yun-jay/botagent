package alert

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestTelegramAlerter_IsConfigured(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		chatID   string
		expected bool
	}{
		{"both set", "tok", "123", true},
		{"no token", "", "123", false},
		{"no chat", "tok", "", false},
		{"neither", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ta := NewTelegram(tt.token, tt.chatID, testLogger())
			if got := ta.IsConfigured(); got != tt.expected {
				t.Errorf("IsConfigured() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTelegramAlerter_Send_Success(t *testing.T) {
	var receivedPath string
	var receivedChatID string
	var receivedText string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedChatID = r.URL.Query().Get("chat_id")
		receivedText = r.URL.Query().Get("text")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	ta := newTelegramWithBaseURL("test-token", "12345", server.URL, testLogger())
	err := ta.Send(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if receivedPath != "/bottest-token/sendMessage" {
		t.Errorf("path = %q, want %q", receivedPath, "/bottest-token/sendMessage")
	}
	if receivedChatID != "12345" {
		t.Errorf("chat_id = %q, want %q", receivedChatID, "12345")
	}
	if receivedText != "hello world" {
		t.Errorf("text = %q, want %q", receivedText, "hello world")
	}
}

func TestTelegramAlerter_Send_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
	}))
	defer server.Close()

	ta := newTelegramWithBaseURL("test-token", "12345", server.URL, testLogger())
	err := ta.Send(context.Background(), "hello")
	if err == nil {
		t.Fatal("Send() should return error on API error")
	}
	if got := err.Error(); got == "" {
		t.Error("error message should not be empty")
	}
}

func TestTelegramAlerter_Send_NotConfigured(t *testing.T) {
	ta := NewTelegram("", "", testLogger())
	err := ta.Send(context.Background(), "hello")
	if err != nil {
		t.Errorf("Send() on unconfigured alerter should not error, got: %v", err)
	}
}

func TestTelegramAlerter_Send_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	ta := newTelegramWithBaseURL("tok", "123", server.URL, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := ta.Send(ctx, "hello")
	if err == nil {
		t.Error("Send() should return error when context is cancelled")
	}
}

func TestNopAlerter(t *testing.T) {
	nop := &NopAlerter{}
	if nop.IsConfigured() {
		t.Error("NopAlerter should not be configured")
	}
	if err := nop.Send(context.Background(), "test"); err != nil {
		t.Errorf("NopAlerter.Send() should not error, got: %v", err)
	}
}
