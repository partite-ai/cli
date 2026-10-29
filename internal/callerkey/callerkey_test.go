package callerkey

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestKeyIsStoredAndSigns(t *testing.T) {
	t.Setenv("PARTITE_CONFIG_DIR", t.TempDir())
	t.Setenv("PARTITE_NO_KEYRING", "1")

	if k, err := Load("dev"); err != nil || k != nil {
		t.Fatalf("Load before create = %v, %v", k, err)
	}
	k, err := LoadOrCreate("dev")
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadOrCreate("dev")
	if err != nil {
		t.Fatal(err)
	}
	if again.Issuer() != k.Issuer() || !strings.HasPrefix(k.Issuer(), "urn:partite-cli:") {
		t.Errorf("issuers = %s, %s", k.Issuer(), again.Issuer())
	}

	now := time.Unix(1_800_000_000, 0)
	token, err := k.Sign("https://mesh.example/access-points/capt_1", "alice", map[string]any{"tier": "gold"}, now, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts", len(parts))
	}

	// Check the signature with the public key from the key set, as an access
	// point would.
	var set struct {
		Keys []struct{ Kid, X, Y string }
	}
	if err := json.Unmarshal(k.JWKS(), &set); err != nil || len(set.Keys) != 1 {
		t.Fatalf("JWKS = %s, %v", k.JWKS(), err)
	}
	jwk := set.Keys[0]
	if jwk.Kid != k.KeyID() {
		t.Errorf("kid = %s, want %s", jwk.Kid, k.KeyID())
	}
	pub := ecdsa.PublicKey{Curve: k.private.Curve, X: decodeInt(t, jwk.X), Y: decodeInt(t, jwk.Y)}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("signature = %x, %v", sig, err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Error("signature does not verify with the key set")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["iss"] != k.Issuer() || claims["sub"] != "alice" || claims["aud"] != "https://mesh.example/access-points/capt_1" ||
		claims["tier"] != "gold" || claims["exp"] != float64(now.Add(5*time.Minute).Unix()) {
		t.Errorf("claims = %v", claims)
	}
}

func decodeInt(t *testing.T, s string) *big.Int {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return new(big.Int).SetBytes(b)
}
