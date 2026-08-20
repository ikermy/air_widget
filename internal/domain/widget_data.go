package domain

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// WidgetConfig is the internal representation of the JSON stored in
// WidgetBotData.Data. It is intentionally not added to WidgetBotData: Data
// remains the single storage field for widget configuration.
type WidgetConfig struct {
	AllowedUrls  []string   `json:"allowedUrls"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	NeverExpires bool       `json:"neverExpires"`
}

// ParseWidgetConfig parses and validates widget configuration stored in Data.
func ParseWidgetConfig(tokenJSON string, now time.Time) (WidgetConfig, error) {
	var config WidgetConfig
	if strings.TrimSpace(tokenJSON) == "" {
		return config, fmt.Errorf("empty widget data")
	}
	if err := json.Unmarshal([]byte(tokenJSON), &config); err != nil {
		return config, fmt.Errorf("invalid widget data JSON: %w", err)
	}
	return ParseWidgetConfigForGeneration(config, now)
}

func ParseWidgetConfigForGeneration(config WidgetConfig, now time.Time) (WidgetConfig, error) {
	if len(config.AllowedUrls) == 0 {
		return config, fmt.Errorf("AllowedUrls is required")
	}
	if config.NeverExpires && config.ExpiresAt != nil {
		return config, fmt.Errorf("expiresAt and neverExpires are mutually exclusive")
	}
	if !config.NeverExpires && (config.ExpiresAt == nil || !config.ExpiresAt.After(now)) {
		return config, fmt.Errorf("expiresAt must be in the future")
	}

	seen := make(map[string]struct{}, len(config.AllowedUrls))
	for index, origin := range config.AllowedUrls {
		normalized, err := NormalizeOrigin(origin)
		if err != nil {
			return config, fmt.Errorf("AllowedUrls[%d]: %w", index, err)
		}
		if _, exists := seen[normalized]; exists {
			return config, fmt.Errorf("duplicate allowed origin %q", normalized)
		}
		seen[normalized] = struct{}{}
		config.AllowedUrls[index] = normalized
	}
	return config, nil
}

// NormalizeOrigin returns scheme://host[:port] and rejects paths, queries,
// fragments, credentials and wildcard origins.
func NormalizeOrigin(raw string) (string, error) {
	origin := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid origin %q", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported origin scheme in %q", raw)
	}
	if strings.Contains(u.Host, "*") {
		return "", fmt.Errorf("wildcard origin is not allowed")
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), nil
}

func (c WidgetConfig) AllowsOrigin(origin string) bool {
	normalized, err := NormalizeOrigin(origin)
	if err != nil {
		return false
	}
	for _, allowed := range c.AllowedUrls {
		if normalized == allowed {
			return true
		}
	}
	return false
}
