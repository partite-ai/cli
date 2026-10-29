package auth

import "strings"

// apiKeyTokenPrefix starts an API key token: prt_ak_<key id>_<secret>.
const apiKeyTokenPrefix = "prt_ak_"

// APIKeyToken returns the token for an API key id and secret.
func APIKeyToken(keyID, secret string) string {
	return apiKeyTokenPrefix + keyID + "_" + secret
}

// APIKeyID returns the key id in an API key token. It reports false when the
// token is not an API key token.
func APIKeyID(token string) (string, bool) {
	rest, ok := strings.CutPrefix(token, apiKeyTokenPrefix)
	if !ok {
		return "", false
	}
	keyID, secret, _ := strings.Cut(rest, "_")
	return keyID, keyID != "" && secret != ""
}
