package config_test

import (
	"os"
	"testing"

	"github.com/yunus/botagent/config"
)

func TestMustEnv_Present(t *testing.T) {
	t.Setenv("TEST_MUST_ENV", "hello")
	if got := config.MustEnv("TEST_MUST_ENV"); got != "hello" {
		t.Errorf("MustEnv = %q, want %q", got, "hello")
	}
}

func TestMustEnv_Missing(t *testing.T) {
	os.Unsetenv("TEST_MUST_ENV_MISSING")
	defer func() {
		r := recover()
		if r == nil {
			t.Error("MustEnv should panic on missing var")
		}
	}()
	config.MustEnv("TEST_MUST_ENV_MISSING")
}

func TestOptEnv_Present(t *testing.T) {
	t.Setenv("TEST_OPT_ENV", "world")
	if got := config.OptEnv("TEST_OPT_ENV", "default"); got != "world" {
		t.Errorf("OptEnv = %q, want %q", got, "world")
	}
}

func TestOptEnv_Missing(t *testing.T) {
	os.Unsetenv("TEST_OPT_ENV_MISSING")
	if got := config.OptEnv("TEST_OPT_ENV_MISSING", "fallback"); got != "fallback" {
		t.Errorf("OptEnv = %q, want %q", got, "fallback")
	}
}

func TestOptEnvFloat_Valid(t *testing.T) {
	t.Setenv("TEST_FLOAT", "3.14")
	if got := config.OptEnvFloat("TEST_FLOAT", 0); got != 3.14 {
		t.Errorf("OptEnvFloat = %v, want %v", got, 3.14)
	}
}

func TestOptEnvFloat_Invalid(t *testing.T) {
	t.Setenv("TEST_FLOAT_BAD", "notanumber")
	if got := config.OptEnvFloat("TEST_FLOAT_BAD", 1.5); got != 1.5 {
		t.Errorf("OptEnvFloat = %v, want %v (default)", got, 1.5)
	}
}

func TestOptEnvFloat_Missing(t *testing.T) {
	os.Unsetenv("TEST_FLOAT_MISSING")
	if got := config.OptEnvFloat("TEST_FLOAT_MISSING", 2.5); got != 2.5 {
		t.Errorf("OptEnvFloat = %v, want %v", got, 2.5)
	}
}

func TestOptEnvInt_Valid(t *testing.T) {
	t.Setenv("TEST_INT", "42")
	if got := config.OptEnvInt("TEST_INT", 0); got != 42 {
		t.Errorf("OptEnvInt = %v, want %v", got, 42)
	}
}

func TestOptEnvInt_Invalid(t *testing.T) {
	t.Setenv("TEST_INT_BAD", "abc")
	if got := config.OptEnvInt("TEST_INT_BAD", 10); got != 10 {
		t.Errorf("OptEnvInt = %v, want %v (default)", got, 10)
	}
}

func TestOptEnvInt_Missing(t *testing.T) {
	os.Unsetenv("TEST_INT_MISSING")
	if got := config.OptEnvInt("TEST_INT_MISSING", 99); got != 99 {
		t.Errorf("OptEnvInt = %v, want %v", got, 99)
	}
}

func TestOptEnvBool(t *testing.T) {
	tests := []struct {
		name       string
		envVal     string
		set        bool
		defaultVal bool
		want       bool
	}{
		{"true", "true", true, false, true},
		{"1", "1", true, false, true},
		{"yes", "yes", true, false, true},
		{"false", "false", true, true, false},
		{"0", "0", true, true, false},
		{"no", "no", true, true, false},
		{"missing_default_true", "", false, true, true},
		{"missing_default_false", "", false, false, false},
		{"invalid_default_true", "maybe", true, true, true},
		{"invalid_default_false", "maybe", true, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_BOOL_" + tt.name
			if tt.set {
				t.Setenv(key, tt.envVal)
			} else {
				os.Unsetenv(key)
			}
			if got := config.OptEnvBool(key, tt.defaultVal); got != tt.want {
				t.Errorf("OptEnvBool(%q, %v) = %v, want %v", tt.envVal, tt.defaultVal, got, tt.want)
			}
		})
	}
}

func TestLoadEnv_MissingFileIsOK(t *testing.T) {
	err := config.LoadEnv("/nonexistent/.env")
	if err != nil {
		t.Errorf("LoadEnv should not error on missing file, got: %v", err)
	}
}
