package p2p

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
)

func TestEncryptedStreamReaderDoesNotSendPlaintextChunksAndReceiverDecrypts(t *testing.T) {
	key := mustTestTransferKey(t)
	plaintext := []byte("secret artifact payload")
	senderCrypto := mustTestChunkCipher(t, key)
	receiverCrypto := mustTestChunkCipher(t, key)
	dc := newFakeDataChannel()

	manifest, err := StreamReader(context.Background(), "tr_secure", bytes.NewReader(plaintext), dc, SenderOptions{
		ChunkSize:  7,
		Encryption: senderCrypto,
	})
	if err != nil {
		t.Fatalf("encrypted stream reader: %v", err)
	}

	if len(dc.sent) < 2 {
		t.Fatalf("sent frames = %d, want chunks plus manifest", len(dc.sent))
	}
	first, err := DecodeFrame(dc.sent[0])
	if err != nil {
		t.Fatalf("decode first encrypted frame: %v", err)
	}
	if first.Encryption == nil || first.Encryption.Algorithm != EncryptionAlgorithmAES256GCM {
		t.Fatalf("chunk encryption metadata = %#v", first.Encryption)
	}
	if bytes.Contains(dc.sent[0], plaintext[:7]) || bytes.Equal(first.Data, plaintext[:7]) {
		t.Fatalf("encrypted chunk leaked plaintext: frame=%s data=%x", string(dc.sent[0]), first.Data)
	}

	var out bytes.Buffer
	receiver := NewReceiver("tr_secure", &out, ReceiverOptions{Encryption: receiverCrypto, RequireEncryption: true})
	var completed *Manifest
	for _, msg := range dc.sent {
		completed, err = receiver.Accept(msg)
		if err != nil {
			t.Fatalf("encrypted receiver accept: %v", err)
		}
	}
	if completed == nil || *completed != manifest {
		t.Fatalf("completed manifest = %#v, want %#v", completed, manifest)
	}
	if !bytes.Equal(out.Bytes(), plaintext) {
		t.Fatalf("decrypted plaintext = %q, want %q", out.Bytes(), plaintext)
	}
	wantDigest := sha256.Sum256(plaintext)
	if manifest.SHA256Hex != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("manifest digest = %s, want plaintext digest", manifest.SHA256Hex)
	}
}

func TestStreamReaderRequiresEncryptionUnlessPlaintextExplicitlyAllowed(t *testing.T) {
	dc := newFakeDataChannel()
	_, err := StreamReader(context.Background(), "tr_secure", bytes.NewBufferString("plaintext"), dc, SenderOptions{ChunkSize: 9})
	if !errors.Is(err, ErrEncryptionRequired) {
		t.Fatalf("StreamReader without encryption error = %v, want ErrEncryptionRequired", err)
	}
	if len(dc.sent) != 0 {
		t.Fatalf("StreamReader sent %d frames without encryption", len(dc.sent))
	}
}

func TestReceiverRequiresEncryptedChunksByDefault(t *testing.T) {
	key := mustTestTransferKey(t)
	receiver := NewReceiver("tr_secure", &bytes.Buffer{}, ReceiverOptions{Encryption: mustTestChunkCipher(t, key)})
	plainChunk, err := EncodeFrame(Frame{Version: ProtocolVersion, Type: FrameTypeChunk, TransferID: "tr_secure", Sequence: 0, Offset: 0, Data: []byte("plaintext")})
	if err != nil {
		t.Fatalf("encode plaintext frame: %v", err)
	}

	if _, err := receiver.Accept(plainChunk); !errors.Is(err, ErrEncryptionRequired) {
		t.Fatalf("receiver accept plaintext error = %v, want ErrEncryptionRequired", err)
	}
	if _, err := receiver.Accept(plainChunk); !errors.Is(err, ErrTransferFailed) {
		t.Fatalf("post-failure accept error = %v, want ErrTransferFailed", err)
	}
}

func TestReceiverFailsClosedOnTamperedEncryptedChunk(t *testing.T) {
	key := mustTestTransferKey(t)
	dc := newFakeDataChannel()
	if _, err := StreamReader(context.Background(), "tr_secure", bytes.NewBufferString("tamper me"), dc, SenderOptions{ChunkSize: 9, Encryption: mustTestChunkCipher(t, key)}); err != nil {
		t.Fatalf("encrypted stream reader: %v", err)
	}
	frame, err := DecodeFrame(dc.sent[0])
	if err != nil {
		t.Fatalf("decode encrypted frame: %v", err)
	}
	frame.Data[0] ^= 0xff
	tampered, err := encodeFrameUnsafe(frame)
	if err != nil {
		t.Fatalf("encode tampered frame: %v", err)
	}

	receiver := NewReceiver("tr_secure", &bytes.Buffer{}, ReceiverOptions{Encryption: mustTestChunkCipher(t, key), RequireEncryption: true})
	if _, err := receiver.Accept(tampered); !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("tampered encrypted chunk error = %v, want ErrDecryptionFailed", err)
	}
	if _, err := receiver.Accept(dc.sent[1]); !errors.Is(err, ErrTransferFailed) {
		t.Fatalf("post-tamper accept error = %v, want ErrTransferFailed", err)
	}
}

func TestReceiverRejectsStandaloneManifestByDefault(t *testing.T) {
	manifest := Manifest{TransferID: "tr_secure", TotalBytes: 0, ChunkCount: 0, SHA256Hex: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}
	encoded, err := EncodeManifestFrame(manifest)
	if err != nil {
		t.Fatalf("encode empty manifest: %v", err)
	}
	receiver := NewReceiver("tr_secure", &bytes.Buffer{}, ReceiverOptions{Encryption: mustTestChunkCipher(t, mustTestTransferKey(t))})
	if _, err := receiver.Accept(encoded); !errors.Is(err, ErrEncryptionRequired) {
		t.Fatalf("standalone manifest error = %v, want ErrEncryptionRequired", err)
	}
}

func TestEncryptedZeroByteTransferAuthenticatesKeyPossession(t *testing.T) {
	key := mustTestTransferKey(t)
	dc := newFakeDataChannel()
	manifest, err := StreamReader(context.Background(), "tr_empty", bytes.NewReader(nil), dc, SenderOptions{Encryption: mustTestChunkCipher(t, key)})
	if err != nil {
		t.Fatalf("encrypted empty stream reader: %v", err)
	}
	if manifest.TotalBytes != 0 || manifest.ChunkCount != 1 {
		t.Fatalf("empty encrypted manifest = %#v", manifest)
	}
	if len(dc.sent) != 2 {
		t.Fatalf("empty encrypted frame count = %d, want sentinel chunk plus manifest", len(dc.sent))
	}
	first, err := DecodeFrame(dc.sent[0])
	if err != nil {
		t.Fatalf("decode empty encrypted sentinel: %v", err)
	}
	if first.Type != FrameTypeChunk || first.Encryption == nil || len(first.Data) == 0 {
		t.Fatalf("first frame is not encrypted empty sentinel: %#v", first)
	}

	var out bytes.Buffer
	receiver := NewReceiver("tr_empty", &out, ReceiverOptions{Encryption: mustTestChunkCipher(t, key)})
	var completed *Manifest
	for _, msg := range dc.sent {
		completed, err = receiver.Accept(msg)
		if err != nil {
			t.Fatalf("receive encrypted empty transfer: %v", err)
		}
	}
	if completed == nil || *completed != manifest || out.Len() != 0 {
		t.Fatalf("empty encrypted completion = %#v, bytes=%d, manifest=%#v", completed, out.Len(), manifest)
	}
}

func TestDefaultKeyDeliveryPlansKeepRawKeysOutOfBackend(t *testing.T) {
	key := mustTestTransferKey(t)
	agentPlan, err := DefaultKeyDeliveryPlan(RecipientAgent)
	if err != nil {
		t.Fatalf("agent key delivery plan: %v", err)
	}
	if agentPlan.Mode != KeyDeliveryAgentPublicKeyEnvelope || agentPlan.BackendSeesRawKey {
		t.Fatalf("agent key delivery plan = %#v", agentPlan)
	}
	recipientPrivate, recipientPublic, err := GenerateAgentEnvelopeKeyPair()
	if err != nil {
		t.Fatalf("agent envelope key pair: %v", err)
	}
	agentEnvelope, err := WrapTransferKeyForAgent("agent-b", recipientPublic, key)
	if err != nil {
		t.Fatalf("agent key envelope: %v", err)
	}
	unwrapped, err := OpenAgentKeyEnvelope("agent-b", recipientPrivate, agentEnvelope)
	if err != nil {
		t.Fatalf("open agent key envelope: %v", err)
	}
	if unwrapped != key {
		t.Fatalf("unwrapped key mismatch")
	}
	assertJSONDoesNotContain(t, agentEnvelope, key.Bytes())

	browserPlan, err := DefaultKeyDeliveryPlan(RecipientBrowser)
	if err != nil {
		t.Fatalf("browser key delivery plan: %v", err)
	}
	if browserPlan.Mode != KeyDeliveryBrowserURLFragment || browserPlan.BackendSeesRawKey {
		t.Fatalf("browser key delivery plan = %#v", browserPlan)
	}
	fragment := NewBrowserKeyFragment(key)
	if fragment.Fragment == "" || !fragment.BackendSeesRawKeyFree() {
		t.Fatalf("browser key fragment = %#v", fragment)
	}
	assertJSONDoesNotContain(t, fragment, key.Bytes())
}

func assertJSONDoesNotContain(t *testing.T, v any, secret []byte) {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if bytes.Contains(encoded, secret) || bytes.Contains(encoded, []byte(hex.EncodeToString(secret))) || bytes.Contains(encoded, []byte(base64.StdEncoding.EncodeToString(secret))) || bytes.Contains(encoded, []byte(base64.RawURLEncoding.EncodeToString(secret))) {
		t.Fatalf("backend payload exposes secret key: %s", encoded)
	}
}

func mustTestTransferKey(t *testing.T) TransferKey {
	t.Helper()
	key, err := TransferKeyFromBytes(bytes.Repeat([]byte{0x42}, TransferKeySize))
	if err != nil {
		t.Fatalf("transfer key: %v", err)
	}
	return key
}

func mustTestChunkCipher(t *testing.T, key TransferKey) *ChunkCipher {
	t.Helper()
	cipher, err := NewChunkCipher(key)
	if err != nil {
		t.Fatalf("chunk cipher: %v", err)
	}
	return cipher
}
