package selfupdate

import (
	"net/http"
	"testing"
	"time"
)

func TestConfigWithDefaults_ClientHasTimeout(t *testing.T) {
	cfg := Config{}.WithDefaults()
	if cfg.Client == nil {
		t.Fatal("WithDefaults left Client nil")
	}
	if cfg.Client.Timeout <= 0 {
		t.Errorf("default client Timeout = %v, want > 0 (an unbounded client can hang pawl update forever)", cfg.Client.Timeout)
	}
}

func TestConfigWithDefaults_InjectedClientKept(t *testing.T) {
	injected := &http.Client{Timeout: 3 * time.Second}
	cfg := Config{Client: injected}.WithDefaults()
	if cfg.Client != injected {
		t.Errorf("WithDefaults replaced an explicitly injected client")
	}
}
