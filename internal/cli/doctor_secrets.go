package cli

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type configSecretFinding struct {
	Path   string
	Reason string
}

var configSecretValuePatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{name: "GitHub token literal", re: regexp.MustCompile(`(?:github_pat_[A-Za-z0-9_]{20,}|gh[opsur]_[A-Za-z0-9_]{20,})`)},
	{name: "Linear API token literal", re: regexp.MustCompile(`lin_(?:api|oauth)_[A-Za-z0-9_-]{20,}`)},
	{name: "OpenAI API key literal", re: regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`)},
	{name: "Slack token literal", re: regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{20,}`)},
	{name: "AWS access key literal", re: regexp.MustCompile(`(?:AKIA|ASIA)[0-9A-Z]{16}`)},
	{name: "Discord webhook URL literal", re: regexp.MustCompile(`https://(?:discord|discordapp)\.com/api/webhooks/[0-9]+/[A-Za-z0-9_-]+`)},
	{name: "bearer token literal", re: regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{24,}`)},
}

var configSecretEnvVarNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func configSecretFindings(cfg map[string]any) []configSecretFinding {
	var findings []configSecretFinding
	collectConfigSecretFindings(nil, cfg, &findings)
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path == findings[j].Path {
			return findings[i].Reason < findings[j].Reason
		}
		return findings[i].Path < findings[j].Path
	})
	return findings
}

func collectConfigSecretFindings(path []string, value any, findings *[]configSecretFinding) {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			nextPath := append(append([]string(nil), path...), key)
			collectConfigSecretFindings(nextPath, v[key], findings)
		}
	case []any:
		for _, item := range v {
			collectConfigSecretFindings(path, item, findings)
		}
	case []map[string]any:
		for _, item := range v {
			collectConfigSecretFindings(path, item, findings)
		}
	case string:
		collectConfigSecretStringFinding(path, v, findings)
	default:
		if configSecretPath(path) && !isZeroConfigValue(v) {
			*findings = append(*findings, configSecretFinding{
				Path:   configPathLabel(path),
				Reason: "secret-like config key",
			})
		}
	}
}

func collectConfigSecretStringFinding(path []string, value string, findings *[]configSecretFinding) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return
	}
	if configSecretEnvReference(trimmed) || configSecretPlaceholder(trimmed) {
		return
	}
	for _, pattern := range configSecretValuePatterns {
		if pattern.re.MatchString(trimmed) {
			*findings = append(*findings, configSecretFinding{
				Path:   configPathLabel(path),
				Reason: pattern.name,
			})
			return
		}
	}
	if configSecretPath(path) {
		*findings = append(*findings, configSecretFinding{
			Path:   configPathLabel(path),
			Reason: "secret-like config key",
		})
		return
	}
}

func configSecretPath(path []string) bool {
	for _, segment := range path {
		if configSecretSegment(segment) {
			return true
		}
	}
	return false
}

func configSecretSegment(segment string) bool {
	normalized := normalizeConfigKey(segment)
	switch normalized {
	case "agent_id",
		"agent_login",
		"agent_user_id",
		"budget_tokens",
		"cached_input_tokens",
		"input_tokens",
		"max_tokens",
		"output_tokens",
		"reasoning_output_tokens",
		"token_budget",
		"token_budget_notices",
		"token_budget_ratio",
		"token_available_at",
		"tokens",
		"tokens_added",
		"tokens_allocated",
		"tokens_consumed",
		"tokens_per_day",
		"tokens_released",
		"tokens_remaining",
		"tokens_used":
		return false
	case "access_token",
		"api_key",
		"apikey",
		"auth_token",
		"authorization",
		"bearer",
		"client_secret",
		"cookie",
		"password",
		"passwd",
		"private_key",
		"refresh_token",
		"secret",
		"secrets",
		"webhook",
		"webhook_secret",
		"webhook_token",
		"webhook_url":
		return true
	}
	for _, suffix := range []string{
		"_access_token",
		"_api_key",
		"_apikey",
		"_auth_token",
		"_client_secret",
		"_password",
		"_passwd",
		"_private_key",
		"_refresh_token",
		"_secret",
		"_token",
		"_webhook",
		"_webhook_secret",
		"_webhook_token",
		"_webhook_url",
	} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func normalizeConfigKey(key string) string {
	replacer := strings.NewReplacer("-", "_", " ", "_")
	return strings.ToLower(replacer.Replace(strings.TrimSpace(key)))
}

func configPathLabel(path []string) string {
	if len(path) == 0 {
		return "<root>"
	}
	if len(path) == 1 {
		return path[0]
	}
	return fmt.Sprintf("[%s].%s", strings.Join(path[:len(path)-1], "."), path[len(path)-1])
}

func configSecretEnvReference(value string) bool {
	name := ""
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		name = strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
	} else if strings.HasPrefix(value, "$") {
		name = strings.TrimPrefix(value, "$")
	} else if strings.HasPrefix(value, "env:") {
		name = strings.TrimPrefix(value, "env:")
	} else {
		return false
	}
	if !configSecretEnvVarNamePattern.MatchString(name) {
		return false
	}
	for _, pattern := range configSecretValuePatterns {
		if pattern.re.MatchString(name) {
			return false
		}
	}
	return true
}

func configSecretPlaceholder(value string) bool {
	normalized := strings.ToLower(strings.Trim(value, " <>[]{}()\"'"))
	switch normalized {
	case "redacted", "placeholder", "replace-me", "changeme", "change-me", "example", "unset":
		return true
	default:
		return strings.Trim(value, "*xX") == ""
	}
}

func isZeroConfigValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case bool:
		return !v
	case int:
		return v == 0
	case int64:
		return v == 0
	case uint64:
		return v == 0
	case float64:
		return v == 0
	case []any:
		return len(v) == 0
	case []map[string]any:
		return len(v) == 0
	default:
		return false
	}
}
