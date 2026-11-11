package redact

import (
	"net/url"
	"regexp"
	"strings"
)

// credentialRedacted replaces every secret value in rendered connection
// material. Open uses these helpers so startup errors can name the failing
// operation and host without ever repeating a password, and without dropping
// the underlying cause (which travels in the wrapped error, not the text).
const credentialRedacted = "[redacted]"

// secretParamPattern matches the libpq credential parameters. passfile and
// service are deliberately NOT in this set: they name a file or a service,
// not a secret, and stay visible for diagnosis.
var secretParamPattern = regexp.MustCompile(`(?i)\b(password|sslpassword)\s*=\s*('[^']*'|[^\s]+)`)

// redactDSN renders a connection string safe for logs and error text. It
// covers both accepted DSN forms: a URL with userinfo credentials and a
// libpq keyword/value string with credential parameters. Anything the
// helpers cannot classify is returned with a whole-string guard: if it
// contains '=' at all, the caller must additionally pass the known secret
// through scrubSecretText before surfacing text.
func RedactDSN(dsn string) string {
	if dsn == "" {
		return dsn
	}
	// URL form: splice the credential out textually so the redaction marker
	// survives verbatim (a net/url round trip would percent-encode it).
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

// scrubSecretText removes an exact secret from arbitrary error text. It is a
// no-op on an empty secret so a caller cannot erase ordinary text.
func ScrubSecretText(text, secret string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, credentialRedacted)
}

// extractDSNPassword returns the credential embedded in a DSN, if any, for
// use as the scrubSecretText secret. It understands both accepted forms.
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
