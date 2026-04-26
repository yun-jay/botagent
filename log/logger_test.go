package log_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	botlog "github.com/yunus/botagent/log"
)

func TestNew_JSONFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := botlog.NewWithWriter("test-bot", false, &buf)
	logger.Info("hello world", "key", "value")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log output is not valid JSON: %v\noutput: %s", err, buf.String())
	}

	// Verify required fields
	for _, field := range []string{"time", "level", "msg", "bot"} {
		if _, ok := entry[field]; !ok {
			t.Errorf("missing field %q in log output", field)
		}
	}
	if entry["bot"] != "test-bot" {
		t.Errorf("bot = %v, want %q", entry["bot"], "test-bot")
	}
	if entry["msg"] != "hello world" {
		t.Errorf("msg = %v, want %q", entry["msg"], "hello world")
	}
	if entry["key"] != "value" {
		t.Errorf("key = %v, want %q", entry["key"], "value")
	}
	if entry["level"] != "INFO" {
		t.Errorf("level = %v, want %q", entry["level"], "INFO")
	}
}

func TestNew_VerboseEnablesDebug(t *testing.T) {
	var buf bytes.Buffer
	logger := botlog.NewWithWriter("test-bot", true, &buf)
	logger.Debug("debug message")

	if buf.Len() == 0 {
		t.Error("verbose=true should enable debug logging, but got no output")
	}

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry["level"] != "DEBUG" {
		t.Errorf("level = %v, want %q", entry["level"], "DEBUG")
	}
}

func TestNew_NonVerboseFiltersDebug(t *testing.T) {
	var buf bytes.Buffer
	logger := botlog.NewWithWriter("test-bot", false, &buf)
	logger.Debug("should not appear")

	if buf.Len() != 0 {
		t.Errorf("verbose=false should filter debug logs, got: %s", buf.String())
	}
}

func TestWithComponent(t *testing.T) {
	var buf bytes.Buffer
	base := botlog.NewWithWriter("test-bot", false, &buf)
	logger := botlog.WithComponent(base, "executor")
	logger.Info("test")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry["component"] != "executor" {
		t.Errorf("component = %v, want %q", entry["component"], "executor")
	}
	if entry["bot"] != "test-bot" {
		t.Errorf("bot = %v, want %q", entry["bot"], "test-bot")
	}
}

func TestNew_DefaultWritesToStdout(t *testing.T) {
	logger := botlog.New("test-bot", false)
	if logger == nil {
		t.Fatal("New returned nil")
	}
	if !logger.Enabled(nil, slog.LevelInfo) {
		t.Error("INFO should be enabled")
	}
}
