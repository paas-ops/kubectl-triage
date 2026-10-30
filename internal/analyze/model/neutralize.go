package model

import "strings"

// Neutralization placeholders are user-visible report text, so they stay in
// Chinese like the rest of the report (ADR-004 D5).
const (
	removedURL         = "[已移除外部链接]"
	removedCommand     = "[已移除命令]"
	removedInstruction = "[已移除可疑指令内容]"
)

// maxErrorMessageRunes bounds an error message before it becomes report text.
const maxErrorMessageRunes = 500

// maxProblemRunes bounds one correction-round problem line.
const maxProblemRunes = 300

// commandFragments are never echoed into a report. The list follows the M1
// analyzer's fragments and adds the read-only guarantees from 需求文档 §8
// (no kubectl apply/delete/exec in generated reports).
var commandFragments = []string{
	"rm -rf", "rm -fr", "curl ", "wget ", "bash -c", "sh -c", "nc -e",
	"chmod +x", "base64 -d", "mkfifo", "echo ", "nohup ",
	"kubectl delete", "kubectl apply", "kubectl exec", "kubectl drain",
	"kubectl replace", "kubectl patch", "kubectl create", "kubectl scale",
	"sudo ", "| sh", "| bash", "> /dev/",
}

// instructionMarkers identify prompt-injection style text in model output.
var instructionMarkers = []string{
	"ignore previous instructions", "ignore all previous", "ignore the above",
	"disregard previous", "disregard all", "exfiltrate", "system prompt",
	"忽略之前指令", "忽略以上指令", "忽略前面的指令", "忽略上面的指令",
	"泄露数据", "外传",
}

// externalURLPrefixes are URL schemes that never belong in a report. Evidence
// URLs are never followed either; this only cleans output text.
var externalURLPrefixes = []string{"http://", "https://", "ftp://"}

// neutralizeText makes an untrusted model string report-safe: external URLs are
// replaced everywhere, and command/instruction-like content truncates the value
// with a placeholder from the first offending fragment onward.
func neutralizeText(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	cleaned := stripExternalURLs(trimmed)
	lower := strings.ToLower(cleaned)
	cut := -1
	instruction := false
	for _, fragment := range commandFragments {
		if at := strings.Index(lower, fragment); at >= 0 && (cut < 0 || at < cut) {
			cut = at
			instruction = false
		}
	}
	for _, marker := range instructionMarkers {
		if at := strings.Index(lower, marker); at >= 0 && (cut < 0 || at < cut) {
			cut = at
			instruction = true
		}
	}
	if cut < 0 {
		return strings.TrimSpace(cleaned)
	}
	replacement := removedCommand
	if instruction {
		replacement = removedInstruction
	}
	return strings.TrimSpace(strings.TrimSpace(cleaned[:cut]) + " " + replacement)
}

// stripExternalURLs replaces every http(s)/ftp URL with a placeholder.
func stripExternalURLs(s string) string {
	var builder strings.Builder
	lower := strings.ToLower(s)
	i := 0
	for i < len(s) {
		found := -1
		for _, prefix := range externalURLPrefixes {
			if at := strings.Index(lower[i:], prefix); at >= 0 && (found < 0 || at < found) {
				found = at
			}
		}
		if found < 0 {
			builder.WriteString(s[i:])
			break
		}
		start := i + found
		builder.WriteString(s[i:start])
		builder.WriteString(removedURL)
		j := start
		for j < len(s) && !isURLDelimiter(s[j]) {
			j++
		}
		i = j
	}
	return builder.String()
}

// isURLDelimiter marks bytes that terminate a URL inside free-form text.
func isURLDelimiter(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '"', '\'', '<', '>', '(', ')', '[', ']', '{', '}', ',', ';', '|':
		return true
	default:
		return false
	}
}

// truncateRunes bounds s to limit runes without splitting a UTF-8 sequence.
func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

// scrubSecret defensively removes an API key from text that may end up in a
// report. Very short keys are skipped to avoid replacing common words.
func scrubSecret(s, secret string) string {
	secret = strings.TrimSpace(secret)
	if len(secret) < 4 {
		return s
	}
	return strings.ReplaceAll(s, secret, "[REDACTED]")
}
