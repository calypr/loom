package redact

import (
	"net/url"
	"regexp"
	"strings"
)

const credentialRedacted = "[redacted]"

var secretParamPattern = regexp.MustCompile(`(?i)\b(password|sslpassword)\s*=\s*('[^']*'|[^\s]+)`)

func RedactDSN(dsn string) string {
	if dsn == "" {
		return dsn
	}
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" && u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			schemeEnd := strings.Index(dsn, "://")
			if schemeEnd >= 0 {
				if at := strings.Index(dsn[schemeEnd+3:], "@"); at >= 0 {
					return dsn[:schemeEnd+3] + u.User.Username() + ":" + credentialRedacted + dsn[schemeEnd+3+at:]
				}
			}
		}
		return dsn
	}
	return secretParamPattern.ReplaceAllStringFunc(dsn, func(match string) string {
		eq := strings.Index(match, "=")
		key := strings.TrimSpace(match[:eq])
		value := match[eq+1:]
		if strings.HasPrefix(value, "'") {
			return key + "='" + credentialRedacted + "'"
		}
		return key + "=" + credentialRedacted
	})
}

func ScrubSecretText(text, secret string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, credentialRedacted)
}

func ExtractDSNPassword(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" && u.User != nil {
		if password, hasPassword := u.User.Password(); hasPassword {
			return password
		}
	}
	match := secretParamPattern.FindStringSubmatch(dsn)
	if match == nil {
		return ""
	}
	value := match[2]
	value = strings.TrimPrefix(value, "'")
	value = strings.TrimSuffix(value, "'")
	return value
}
