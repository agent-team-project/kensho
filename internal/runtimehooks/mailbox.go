package runtimehooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	teamtemplate "github.com/jamesaud/agent-team/internal/template"
)

const (
	HookCommandStatus = "Checking daemon mailbox"
	scriptName        = "agent-team-mailbox-inject.py"
)

type MailboxHook struct {
	Command    string
	RuntimeDir string
}

func ClaudeSettingsJSON(hook *MailboxHook) ([]byte, error) {
	if hook == nil || strings.TrimSpace(hook.Command) == "" {
		return nil, nil
	}
	handler := map[string]any{
		"type":    "command",
		"command": hook.Command,
		"timeout": 10,
	}
	group := map[string]any{"hooks": []any{handler}}
	settings := map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []any{group},
			"PreToolUse":       []any{group},
		},
	}
	return json.MarshalIndent(settings, "", "  ")
}

func CodexConfigArgs(hook *MailboxHook) []string {
	if hook == nil || strings.TrimSpace(hook.Command) == "" {
		return nil
	}
	handler := fmt.Sprintf(`{type="command", command=%s, timeout=10, statusMessage=%s}`,
		strconv.Quote(hook.Command), strconv.Quote(HookCommandStatus))
	return []string{
		"-c", "hooks.UserPromptSubmit=[{hooks=[" + handler + "]}]",
		"-c", "hooks.PreToolUse=[{hooks=[" + handler + "]}]",
	}
}

func MailboxInjectionEnabled(config teamtemplate.Tree) bool {
	v, ok := config.GetDotted("runtime.hooks.mailbox_injection")
	if !ok {
		return true
	}
	switch value := v.(type) {
	case bool:
		return value
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err == nil {
			return parsed
		}
	}
	return true
}

func PrepareMailboxHook(runtimeDir string) (*MailboxHook, error) {
	hooksDir := filepath.Join(runtimeDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return nil, fmt.Errorf("create runtime hooks dir: %w", err)
	}
	scriptPath := filepath.Join(hooksDir, scriptName)
	if err := os.WriteFile(scriptPath, []byte(mailboxHookScript), 0o755); err != nil {
		return nil, fmt.Errorf("write mailbox hook: %w", err)
	}
	return &MailboxHook{
		Command:    shellQuoteCommand([]string{"python3", scriptPath}),
		RuntimeDir: runtimeDir,
	}, nil
}

func WriteClaudeSettings(runtimeDir string, hook *MailboxHook) (string, error) {
	body, err := ClaudeSettingsJSON(hook)
	if err != nil {
		return "", fmt.Errorf("build Claude mailbox hook settings: %w", err)
	}
	settingsPath := filepath.Join(runtimeDir, ".claude", "agent-team-hooks.settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return "", fmt.Errorf("create Claude mailbox hook settings dir: %w", err)
	}
	if err := os.WriteFile(settingsPath, body, 0o644); err != nil {
		return "", fmt.Errorf("write Claude mailbox hook settings: %w", err)
	}
	return settingsPath, nil
}

func shellQuoteCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '@' || r == '%' || r == '+' || r == '=' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'))
	}) == -1 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

const mailboxHookScript = `#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys
import tempfile

MAX_BYTES = 32768
HEADING = "## New daemon mailbox messages"


def main():
    event = hook_event_name()
    team_root = os.environ.get("AGENT_TEAM_ROOT", "").strip()
    instance = os.environ.get("AGENT_TEAM_INSTANCE", "").strip()
    if not team_root or not instance:
        return 0

    inbox_dir = Path(team_root) / "daemon" / instance
    messages = read_messages(inbox_dir / "mailbox.jsonl")
    cursor = read_cursor(inbox_dir / "mailbox-cursor.txt")
    unread = messages_after_cursor(messages, cursor)
    if not unread:
        return 0

    section = format_messages(unread)
    write_cursor(inbox_dir, unread[-1].get("id", ""))
    output = {
        "hookSpecificOutput": {
            "hookEventName": event,
            "additionalContext": section,
        }
    }
    print(json.dumps(output, ensure_ascii=False))
    return 0


def hook_event_name():
    raw = sys.stdin.read()
    if raw:
        try:
            payload = json.loads(raw)
        except Exception:
            payload = {}
        for key in ("hook_event_name", "hookEventName"):
            value = str(payload.get(key, "")).strip()
            if value:
                return value
    return os.environ.get("AGENT_TEAM_HOOK_EVENT", "UserPromptSubmit").strip() or "UserPromptSubmit"


def read_messages(path):
    if not path.exists():
        return []
    out = []
    with path.open("r", encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if not line:
                continue
            try:
                value = json.loads(line)
            except Exception:
                continue
            if isinstance(value, dict):
                out.append(value)
    return out


def read_cursor(path):
    try:
        return path.read_text(encoding="utf-8").strip()
    except FileNotFoundError:
        return ""


def messages_after_cursor(messages, cursor):
    if not cursor:
        return messages
    for index, msg in enumerate(messages):
        if str(msg.get("id", "")) == cursor:
            return messages[index + 1 :]
    return messages


def format_messages(messages):
    lines = [
        HEADING,
        "",
        "Unread daemon mailbox messages were delivered automatically at this turn boundary. Handle them before continuing unless they explicitly say otherwise.",
        "",
    ]
    for index, msg in enumerate(messages, 1):
        sender = str(msg.get("from", "")).strip() or "unknown"
        msg_id = str(msg.get("id", "")).strip()
        ts = str(msg.get("ts", "")).strip()
        header = f"{index}. From: {sender}"
        if ts:
            header += f" at {ts}"
        if msg_id:
            header += f" (id: {msg_id})"
        lines.append(header)
        body = str(msg.get("body", "")).rstrip("\n")
        if body:
            lines.extend("   " + line for line in body.splitlines())
        else:
            lines.append("   (empty)")
        if index != len(messages):
            lines.append("")
    text = "\n".join(lines)
    if len(text.encode("utf-8")) <= MAX_BYTES:
        return text
    note = f"\n\n[truncated: unread mailbox delivery capped at {MAX_BYTES} bytes; {len(messages)} message(s) were marked delivered]"
    limit = max(0, MAX_BYTES - len(note.encode("utf-8")))
    body = text.encode("utf-8")[:limit].decode("utf-8", "ignore")
    return body + note


def write_cursor(inbox_dir, cursor):
    if not cursor:
        return
    inbox_dir.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(prefix="cursor-", suffix=".tmp", dir=str(inbox_dir), text=True)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            handle.write(cursor + "\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp, inbox_dir / "mailbox-cursor.txt")
    finally:
        try:
            os.unlink(tmp)
        except FileNotFoundError:
            pass


if __name__ == "__main__":
    raise SystemExit(main())
`
