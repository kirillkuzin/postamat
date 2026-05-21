package p2p

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

const ProtocolVersion = 1

type FrameType string

const (
	FrameTypeChunk    FrameType = "chunk"
	FrameTypeManifest FrameType = "manifest"
)

var (
	ErrUnsupportedProtocolVersion = errors.New("unsupported chunk protocol version")
	ErrFrameTypeRequired          = errors.New("frame type is required")
	ErrUnsupportedFrameType       = errors.New("unsupported frame type")
	ErrTransferIDRequired         = errors.New("transfer id is required")
	ErrChunkDataRequired          = errors.New("chunk data is required")
	ErrNegativeOffset             = errors.New("chunk offset must be non-negative")
	ErrNegativeTotalBytes         = errors.New("total bytes must be non-negative")
	ErrInvalidChunkCount          = errors.New("chunk count must be positive")
	ErrSHA256Required             = errors.New("sha256 is required")
	ErrUnexpectedTransferID       = errors.New("unexpected transfer id")
	ErrUnexpectedSequence         = errors.New("unexpected chunk sequence")
	ErrUnexpectedOffset           = errors.New("unexpected chunk offset")
	ErrManifestMismatch           = errors.New("manifest does not match received bytes")
	ErrTransferComplete           = errors.New("transfer is already complete")
	ErrTransferFailed             = errors.New("transfer failed")
)

type Frame struct {
	Version    int                 `json:"v"`
	Type       FrameType           `json:"type"`
	TransferID string              `json:"transfer_id"`
	Sequence   uint64              `json:"seq,omitempty"`
	Offset     int64               `json:"offset,omitempty"`
	Data       []byte              `json:"data,omitempty"`
	Encryption *EncryptionMetadata `json:"enc,omitempty"`
	TotalBytes int64               `json:"total_bytes,omitempty"`
	ChunkCount uint64              `json:"chunk_count,omitempty"`
	SHA256Hex  string              `json:"sha256,omitempty"`
}

type Manifest struct {
	TransferID string
	TotalBytes int64
	ChunkCount uint64
	SHA256Hex  string
}

type Progress struct {
	TransferID        string
	BytesTransferred  int64
	TotalBytes        int64
	ChunksTransferred uint64
}

func EncodeFrame(frame Frame) ([]byte, error) {
	if frame.Version == 0 {
		frame.Version = ProtocolVersion
	}
	if err := validateFrame(frame); err != nil {
		return nil, err
	}
	return encodeFrameUnsafe(frame)
}

func EncodeManifestFrame(manifest Manifest) ([]byte, error) {
	return EncodeFrame(Frame{Version: ProtocolVersion, Type: FrameTypeManifest, TransferID: manifest.TransferID, TotalBytes: manifest.TotalBytes, ChunkCount: manifest.ChunkCount, SHA256Hex: manifest.SHA256Hex})
}

func DecodeFrame(encoded []byte) (Frame, error) {
	var frame Frame
	if err := json.Unmarshal(encoded, &frame); err != nil {
		return Frame{}, err
	}
	if err := validateFrame(frame); err != nil {
		return Frame{}, err
	}
	return frame, nil
}

func encodeFrameUnsafe(frame Frame) ([]byte, error) {
	return json.Marshal(frame)
}

func validateFrame(frame Frame) error {
	if frame.Version != ProtocolVersion {
		return ErrUnsupportedProtocolVersion
	}
	if frame.Type == "" {
		return ErrFrameTypeRequired
	}
	if frame.TransferID == "" {
		return ErrTransferIDRequired
	}
	switch frame.Type {
	case FrameTypeChunk:
		if frame.Offset < 0 {
			return ErrNegativeOffset
		}
		if len(frame.Data) == 0 {
			return ErrChunkDataRequired
		}
		if frame.Encryption != nil {
			if err := validateEncryptionMetadata(frame.Encryption); err != nil {
				return err
			}
		}
	case FrameTypeManifest:
		if frame.TotalBytes < 0 {
			return ErrNegativeTotalBytes
		}
		if frame.ChunkCount == 0 && frame.TotalBytes > 0 {
			return ErrInvalidChunkCount
		}
		if frame.SHA256Hex == "" {
			return ErrSHA256Required
		}
		digest, err := hex.DecodeString(frame.SHA256Hex)
		if err != nil || len(digest) != sha256.Size {
			return ErrSHA256Required
		}
	default:
		return ErrUnsupportedFrameType
	}
	return nil
}
