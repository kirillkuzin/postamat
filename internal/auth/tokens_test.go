package auth

import (
	"strings"
	"testing"
)

func TestGenerateTokenReturnsPrefixedHighEntropyURLSafeSecret(t *testing.T) {
	token, err := GenerateToken("agt", 16)
	if err != nil {
		t.Fatalf("GenerateToken returned error: %v", err)
	}
	if !strings.HasPrefix(token, "agt_") {
		t.Fatalf("token %q does not use expected prefix", token)
	}
	secret := strings.TrimPrefix(token, "agt_")
	if len(secret) < 22 {
		t.Fatalf("secret is too short for 128 bits of entropy: got %d chars", len(secret))
	}
	if strings.ContainsAny(secret, "+/=") {
		t.Fatalf("secret %q is not raw URL-safe base64", secret)
	}
}

func TestGenerateTokenRequiresAtLeast128Bits(t *testing.T) {
	if _, err := GenerateToken("agt", 15); err == nil {
		t.Fatal("expected token byte size below 16 to be rejected")
	}
}

func TestHashTokenUsesPepperAndDoesNotStoreRawToken(t *testing.T) {
	hash, err := HashToken("agt_raw-token", "pepper-one")
	if err != nil {
		t.Fatalf("HashToken returned error: %v", err)
	}
	if strings.Contains(hash, "agt_raw-token") || strings.Contains(hash, "pepper-one") {
		t.Fatalf("hash %q leaks raw token or pepper", hash)
	}
	if !strings.HasPrefix(hash, "sha256:") {
		t.Fatalf("hash %q does not include algorithm prefix", hash)
	}

	same, err := VerifyToken("agt_raw-token", hash, "pepper-one")
	if err != nil {
		t.Fatalf("VerifyToken returned error: %v", err)
	}
	if !same {
		t.Fatal("expected raw token to verify with matching pepper")
	}

	wrongPepper, err := VerifyToken("agt_raw-token", hash, "pepper-two")
	if err != nil {
		t.Fatalf("VerifyToken returned error for wrong pepper: %v", err)
	}
	if wrongPepper {
		t.Fatal("expected verification with wrong pepper to fail")
	}
}

func TestVerifyTokenRejectsWrongTokenAndMalformedHash(t *testing.T) {
	hash, err := HashToken("agt_real", "pepper")
	if err != nil {
		t.Fatalf("HashToken returned error: %v", err)
	}
	ok, err := VerifyToken("agt_wrong", hash, "pepper")
	if err != nil {
		t.Fatalf("VerifyToken returned error for well-formed hash: %v", err)
	}
	if ok {
		t.Fatal("expected wrong token to fail verification")
	}

	if ok, err := VerifyToken("agt_real", "not-a-valid-hash", "pepper"); err == nil || ok {
		t.Fatalf("expected malformed stored hash to be rejected, ok=%v err=%v", ok, err)
	}
}
