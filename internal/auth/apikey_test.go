package auth

import "testing"

func TestAPIKeyID(t *testing.T) {
	for _, tc := range []struct {
		token, keyID string
		ok           bool
	}{
		{APIKeyToken("42GDRZA7X7QD5NQ7", "TYCG+abc/def="), "42GDRZA7X7QD5NQ7", true},
		{"prt_ak_42GDRZA7X7QD5NQ7", "42GDRZA7X7QD5NQ7", false},
		{"prt_ak__secret", "", false},
		{"42GDRZA7X7QD5NQ7:secret", "", false},
	} {
		keyID, ok := APIKeyID(tc.token)
		if keyID != tc.keyID || ok != tc.ok {
			t.Errorf("APIKeyID(%q) = %q, %v; want %q, %v", tc.token, keyID, ok, tc.keyID, tc.ok)
		}
	}
}
