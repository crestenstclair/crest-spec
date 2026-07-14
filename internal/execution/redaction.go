package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const MaxMetadataBytes = 64 * 1024

var bearerPattern = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/=-]+`)

func CanonicalRedacted(value any) (string, string, error) {
	redacted := redactValue(value, "")
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return "", "", fmt.Errorf("encode redacted metadata: %w", err)
	}
	if len(encoded) > MaxMetadataBytes {
		return "", "", fmt.Errorf("redacted metadata exceeds %d bytes", MaxMetadataBytes)
	}
	sum := sha256.Sum256(encoded)
	return string(encoded), hex.EncodeToString(sum[:]), nil
}

// RedactText removes credential-shaped material before operational text is
// persisted. Structured host metadata should use CanonicalRedacted so secret
// key names are also detected recursively.
func RedactText(value string) string {
	return bearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
}

func redactValue(value any, key string) any {
	if sensitiveKey(key) {
		return "[REDACTED]"
	}
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			result[childKey] = redactValue(childValue, childKey)
		}
		return result
	case map[string]string:
		result := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			result[childKey] = redactValue(childValue, childKey)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = redactValue(child, "")
		}
		return result
	case []string:
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = redactValue(child, "")
		}
		return result
	case string:
		return bearerPattern.ReplaceAllString(typed, "Bearer [REDACTED]")
	default:
		return value
	}
}

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(key))
	for _, marker := range []string{
		"authorization", "accesstoken", "refreshtoken", "apikey", "password",
		"passwd", "secret", "credential", "cookie", "privatekey", "clientsecret",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
