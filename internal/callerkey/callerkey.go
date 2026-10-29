// Package callerkey keeps the key the CLI signs its own tokens with, to talk
// to a mesh through an access point as any caller. Each context has one key.
// An access point accepts the tokens once partite access-point trust-cli adds
// the key as one of its issuers.
package callerkey

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/partite-ai/partite-cli/internal/credstore"
)

type Key struct {
	private *ecdsa.PrivateKey
}

// Load returns nil when the context has no key.
func Load(contextName string) (*Key, error) {
	s, err := credstore.GetCallerKey(contextName)
	if err != nil || s == "" {
		return nil, err
	}
	block, _ := pem.Decode([]byte(s))
	if block == nil {
		return nil, fmt.Errorf("the caller key of context %s is not PEM", contextName)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("read the caller key of context %s: %w", contextName, err)
	}
	private, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("the caller key of context %s is not an ECDSA key", contextName)
	}
	return &Key{private: private}, nil
}

// LoadOrCreate makes and stores a key when the context has none.
func LoadOrCreate(contextName string) (*Key, error) {
	k, err := Load(contextName)
	if err != nil || k != nil {
		return k, err
	}
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return nil, err
	}
	if err := credstore.PutCallerKey(contextName, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))); err != nil {
		return nil, err
	}
	return &Key{private: private}, nil
}

// publicJWK is the public key as a JWK, with its members in the order RFC 7638
// hashes them for the thumbprint.
func (k *Key) publicJWK() map[string]string {
	pub, err := k.private.PublicKey.ECDH()
	if err != nil {
		panic(err) // a P-256 key always converts
	}
	point := pub.Bytes() // 0x04, then x and y
	return map[string]string{
		"crv": "P-256",
		"kty": "EC",
		"x":   base64.RawURLEncoding.EncodeToString(point[1:33]),
		"y":   base64.RawURLEncoding.EncodeToString(point[33:]),
	}
}

// KeyID is the key's RFC 7638 thumbprint.
func (k *Key) KeyID() string {
	data, _ := json.Marshal(k.publicJWK()) // map keys marshal sorted
	sum := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Issuer is the iss of the key's tokens. It names the key, so two people's
// keys are two issuers.
func (k *Key) Issuer() string {
	return "urn:partite-cli:" + k.KeyID()
}

// JWKS is the key set an access point checks the key's tokens with.
func (k *Key) JWKS() json.RawMessage {
	jwk := k.publicJWK()
	jwk["kid"] = k.KeyID()
	jwk["alg"] = "ES256"
	jwk["use"] = "sig"
	data, _ := json.Marshal(map[string]any{"keys": []any{jwk}})
	return data
}

// Sign makes an ES256 token for audience, with claims and the registered
// claims the key sets.
func (k *Key) Sign(audience, subject string, claims map[string]any, now time.Time, lifetime time.Duration) (string, error) {
	header := map[string]string{"alg": "ES256", "typ": "JWT", "kid": k.KeyID()}
	payload := map[string]any{}
	for name, v := range claims {
		payload[name] = v
	}
	jti := make([]byte, 16)
	_, _ = rand.Read(jti)
	payload["iss"] = k.Issuer()
	payload["sub"] = subject
	payload["aud"] = audience
	payload["iat"] = now.Unix()
	payload["nbf"] = now.Unix()
	payload["exp"] = now.Add(lifetime).Unix()
	payload["jti"] = base64.RawURLEncoding.EncodeToString(jti)

	h, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	p, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, k.private, digest[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return input + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
