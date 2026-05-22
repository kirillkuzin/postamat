package p2p

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	TransferKeySize              = 32
	EncryptionAlgorithmAES256GCM = "AES-256-GCM"
	gcmNonceSize                 = 12
)

var (
	ErrInvalidTransferKey             = errors.New("invalid transfer key")
	ErrEncryptionRequired             = errors.New("encrypted chunk is required")
	ErrInvalidEncryptionMetadata      = errors.New("invalid encryption metadata")
	ErrUnsupportedEncryptionAlgorithm = errors.New("unsupported encryption algorithm")
	ErrDecryptionFailed               = errors.New("chunk decryption failed")
	ErrUnsupportedRecipientKind       = errors.New("unsupported recipient kind")
)

type TransferKey [TransferKeySize]byte

type EncryptionMetadata struct {
	Algorithm string `json:"alg"`
	Nonce     []byte `json:"nonce"`
}

type ChunkCipher struct {
	key  TransferKey
	rand io.Reader
}

type RecipientKind string

const (
	RecipientAgent   RecipientKind = "agent"
	RecipientBrowser RecipientKind = "browser"
)

type KeyDeliveryMode string

const (
	KeyDeliveryAgentPublicKeyEnvelope KeyDeliveryMode = "agent_public_key_envelope"
	KeyDeliveryBrowserURLFragment     KeyDeliveryMode = "browser_url_fragment"
)

type KeyDeliveryPlan struct {
	Recipient         RecipientKind
	Mode              KeyDeliveryMode
	BackendSeesRawKey bool
	Notes             string
}

type AgentKeyEnvelope struct {
	mode               KeyDeliveryMode
	recipientAgentID   string
	algorithm          string
	ephemeralPublicKey []byte
	nonce              []byte
	encryptedKey       []byte
}

type BrowserKeyFragment struct {
	Fragment string `json:"-"`
}

func NewRandomTransferKey() (TransferKey, error) {
	var key TransferKey
	if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
		return TransferKey{}, fmt.Errorf("%w: %v", ErrInvalidTransferKey, err)
	}
	return key, nil
}

func TransferKeyFromBytes(raw []byte) (TransferKey, error) {
	if len(raw) != TransferKeySize {
		return TransferKey{}, ErrInvalidTransferKey
	}
	var key TransferKey
	copy(key[:], raw)
	return key, nil
}

func (k TransferKey) Bytes() []byte {
	return append([]byte(nil), k[:]...)
}

func NewChunkCipher(key TransferKey) (*ChunkCipher, error) {
	return NewChunkCipherWithRand(key, rand.Reader)
}

func NewChunkCipherWithRand(key TransferKey, random io.Reader) (*ChunkCipher, error) {
	if random == nil {
		random = rand.Reader
	}
	if _, err := aes.NewCipher(key[:]); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTransferKey, err)
	}
	return &ChunkCipher{key: key, rand: random}, nil
}

func (c *ChunkCipher) EncryptChunk(transferID string, sequence uint64, offset int64, plaintext []byte) ([]byte, *EncryptionMetadata, error) {
	if c == nil {
		return nil, nil, ErrEncryptionRequired
	}
	gcm, err := c.aead()
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcmNonceSize)
	if _, err := io.ReadFull(c.rand, nonce); err != nil {
		return nil, nil, fmt.Errorf("%w: nonce: %v", ErrTransferFailed, err)
	}
	metadata := &EncryptionMetadata{Algorithm: EncryptionAlgorithmAES256GCM, Nonce: nonce}
	ciphertext := gcm.Seal(nil, nonce, plaintext, chunkAAD(transferID, sequence, offset))
	return ciphertext, metadata, nil
}

func (c *ChunkCipher) DecryptChunk(transferID string, sequence uint64, offset int64, metadata *EncryptionMetadata, ciphertext []byte) ([]byte, error) {
	if c == nil {
		return nil, ErrEncryptionRequired
	}
	if err := validateEncryptionMetadata(metadata); err != nil {
		return nil, err
	}
	gcm, err := c.aead()
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, metadata.Nonce, ciphertext, chunkAAD(transferID, sequence, offset))
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	return plaintext, nil
}

func (c *ChunkCipher) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTransferKey, err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTransferKey, err)
	}
	return gcm, nil
}

func validateEncryptionMetadata(metadata *EncryptionMetadata) error {
	if metadata == nil {
		return ErrEncryptionRequired
	}
	if metadata.Algorithm != EncryptionAlgorithmAES256GCM {
		return ErrUnsupportedEncryptionAlgorithm
	}
	if len(metadata.Nonce) != gcmNonceSize {
		return ErrInvalidEncryptionMetadata
	}
	return nil
}

func chunkAAD(transferID string, sequence uint64, offset int64) []byte {
	return []byte(fmt.Sprintf("postamat:p2p:v%d:%s:%d:%d", ProtocolVersion, transferID, sequence, offset))
}

func DefaultKeyDeliveryPlan(recipient RecipientKind) (KeyDeliveryPlan, error) {
	switch recipient {
	case RecipientAgent:
		return KeyDeliveryPlan{
			Recipient:         recipient,
			Mode:              KeyDeliveryAgentPublicKeyEnvelope,
			BackendSeesRawKey: false,
			Notes:             "Envelope the per-transfer content key to the recipient agent/device public key; backend stores/routes only encrypted envelope metadata.",
		}, nil
	case RecipientBrowser:
		return KeyDeliveryPlan{
			Recipient:         recipient,
			Mode:              KeyDeliveryBrowserURLFragment,
			BackendSeesRawKey: false,
			Notes:             "Put browser receive key material in the URL fragment or equivalent local-only channel; fragments are not sent to the backend in HTTP requests.",
		}, nil
	default:
		return KeyDeliveryPlan{}, ErrUnsupportedRecipientKind
	}
}

func GenerateAgentEnvelopeKeyPair() (privateKey []byte, publicKey []byte, err error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidTransferKey, err)
	}
	return append([]byte(nil), priv.Bytes()...), append([]byte(nil), priv.PublicKey().Bytes()...), nil
}

func WrapTransferKeyForAgent(recipientAgentID string, recipientPublicKey []byte, key TransferKey) (AgentKeyEnvelope, error) {
	if recipientAgentID == "" {
		return AgentKeyEnvelope{}, ErrTransferIDRequired
	}
	recipientPub, err := ecdh.X25519().NewPublicKey(recipientPublicKey)
	if err != nil {
		return AgentKeyEnvelope{}, fmt.Errorf("%w: recipient public key: %v", ErrInvalidTransferKey, err)
	}
	ephemeralPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return AgentKeyEnvelope{}, fmt.Errorf("%w: ephemeral key: %v", ErrInvalidTransferKey, err)
	}
	shared, err := ephemeralPriv.ECDH(recipientPub)
	if err != nil {
		return AgentKeyEnvelope{}, fmt.Errorf("%w: ecdh: %v", ErrInvalidTransferKey, err)
	}
	wrappingKey := deriveAgentWrappingKey(recipientAgentID, shared)
	block, err := aes.NewCipher(wrappingKey[:])
	if err != nil {
		return AgentKeyEnvelope{}, fmt.Errorf("%w: %v", ErrInvalidTransferKey, err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return AgentKeyEnvelope{}, fmt.Errorf("%w: %v", ErrInvalidTransferKey, err)
	}
	nonce := make([]byte, gcmNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return AgentKeyEnvelope{}, fmt.Errorf("%w: envelope nonce: %v", ErrTransferFailed, err)
	}
	ciphertext := gcm.Seal(nil, nonce, key[:], []byte(recipientAgentID))
	return AgentKeyEnvelope{
		mode:               KeyDeliveryAgentPublicKeyEnvelope,
		recipientAgentID:   recipientAgentID,
		algorithm:          "X25519-SHA256-AES-256-GCM",
		ephemeralPublicKey: append([]byte(nil), ephemeralPriv.PublicKey().Bytes()...),
		nonce:              nonce,
		encryptedKey:       ciphertext,
	}, nil
}

func OpenAgentKeyEnvelope(recipientAgentID string, recipientPrivateKey []byte, envelope AgentKeyEnvelope) (TransferKey, error) {
	if recipientAgentID == "" || recipientAgentID != envelope.recipientAgentID || envelope.mode != KeyDeliveryAgentPublicKeyEnvelope || envelope.algorithm != "X25519-SHA256-AES-256-GCM" {
		return TransferKey{}, ErrInvalidEncryptionMetadata
	}
	priv, err := ecdh.X25519().NewPrivateKey(recipientPrivateKey)
	if err != nil {
		return TransferKey{}, fmt.Errorf("%w: recipient private key: %v", ErrInvalidTransferKey, err)
	}
	ephPub, err := ecdh.X25519().NewPublicKey(envelope.ephemeralPublicKey)
	if err != nil {
		return TransferKey{}, fmt.Errorf("%w: ephemeral public key: %v", ErrInvalidTransferKey, err)
	}
	shared, err := priv.ECDH(ephPub)
	if err != nil {
		return TransferKey{}, fmt.Errorf("%w: ecdh: %v", ErrInvalidTransferKey, err)
	}
	wrappingKey := deriveAgentWrappingKey(recipientAgentID, shared)
	block, err := aes.NewCipher(wrappingKey[:])
	if err != nil {
		return TransferKey{}, fmt.Errorf("%w: %v", ErrInvalidTransferKey, err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return TransferKey{}, fmt.Errorf("%w: %v", ErrInvalidTransferKey, err)
	}
	plaintext, err := gcm.Open(nil, envelope.nonce, envelope.encryptedKey, []byte(recipientAgentID))
	if err != nil {
		return TransferKey{}, ErrDecryptionFailed
	}
	return TransferKeyFromBytes(plaintext)
}

type agentKeyEnvelopeWire struct {
	Mode               KeyDeliveryMode `json:"mode"`
	RecipientAgentID   string          `json:"recipient_agent_id"`
	Algorithm          string          `json:"alg"`
	EphemeralPublicKey []byte          `json:"ephemeral_public_key"`
	Nonce              []byte          `json:"nonce"`
	EncryptedKey       []byte          `json:"encrypted_key"`
}

func (e AgentKeyEnvelope) MarshalJSON() ([]byte, error) {
	return json.Marshal(agentKeyEnvelopeWire{
		Mode:               e.mode,
		RecipientAgentID:   e.recipientAgentID,
		Algorithm:          e.algorithm,
		EphemeralPublicKey: append([]byte(nil), e.ephemeralPublicKey...),
		Nonce:              append([]byte(nil), e.nonce...),
		EncryptedKey:       append([]byte(nil), e.encryptedKey...),
	})
}

func (e *AgentKeyEnvelope) UnmarshalJSON(data []byte) error {
	var wire agentKeyEnvelopeWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Mode != KeyDeliveryAgentPublicKeyEnvelope || wire.RecipientAgentID == "" || wire.Algorithm != "X25519-SHA256-AES-256-GCM" || len(wire.EphemeralPublicKey) == 0 || len(wire.Nonce) != gcmNonceSize || len(wire.EncryptedKey) == 0 {
		return ErrInvalidEncryptionMetadata
	}
	*e = AgentKeyEnvelope{
		mode:               wire.Mode,
		recipientAgentID:   wire.RecipientAgentID,
		algorithm:          wire.Algorithm,
		ephemeralPublicKey: append([]byte(nil), wire.EphemeralPublicKey...),
		nonce:              append([]byte(nil), wire.Nonce...),
		encryptedKey:       append([]byte(nil), wire.EncryptedKey...),
	}
	return nil
}

func deriveAgentWrappingKey(recipientAgentID string, shared []byte) [TransferKeySize]byte {
	input := make([]byte, 0, len(shared)+len(recipientAgentID)+64)
	input = append(input, []byte("postamat-agent-key-envelope-v1")...)
	input = append(input, 0)
	input = append(input, []byte(recipientAgentID)...)
	input = append(input, 0)
	input = append(input, shared...)
	return sha256.Sum256(input)
}

func NewBrowserKeyFragment(key TransferKey) BrowserKeyFragment {
	return BrowserKeyFragment{Fragment: "postamat_key=" + base64.RawURLEncoding.EncodeToString(key[:])}
}

func (f BrowserKeyFragment) BackendSeesRawKeyFree() bool {
	encoded, err := f.MarshalJSON()
	return err == nil && string(encoded) == "{}"
}

func (f BrowserKeyFragment) MarshalJSON() ([]byte, error) {
	return []byte("{}"), nil
}
