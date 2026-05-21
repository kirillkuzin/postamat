package p2p

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

type ReceiverOptions struct {
	OnProgress func(Progress)
}

type Receiver struct {
	transferID string
	writer     io.Writer
	options    ReceiverOptions
	hash       hashWriter
	nextSeq    uint64
	offset     int64
	complete   bool
	failed     bool
}

type hashWriter interface {
	io.Writer
	Sum([]byte) []byte
}

func NewReceiver(transferID string, destination io.Writer, options ReceiverOptions) *Receiver {
	return &Receiver{transferID: transferID, writer: destination, options: options, hash: sha256.New()}
}

func (r *Receiver) Accept(encoded []byte) (*Manifest, error) {
	if r == nil || r.writer == nil || r.transferID == "" {
		return nil, fmt.Errorf("%w: missing receiver endpoint", ErrTransferFailed)
	}
	if r.complete {
		return nil, ErrTransferComplete
	}
	if r.failed {
		return nil, ErrTransferFailed
	}
	frame, err := DecodeFrame(encoded)
	if err != nil {
		r.failed = true
		return nil, err
	}
	if frame.TransferID != r.transferID {
		r.failed = true
		return nil, ErrUnexpectedTransferID
	}

	switch frame.Type {
	case FrameTypeChunk:
		if err := r.acceptChunk(frame); err != nil {
			r.failed = true
			return nil, err
		}
		return nil, nil
	case FrameTypeManifest:
		manifest, err := r.acceptManifest(frame)
		if err != nil {
			r.failed = true
			return nil, err
		}
		return manifest, nil
	default:
		r.failed = true
		return nil, ErrUnsupportedFrameType
	}
}

func (r *Receiver) acceptChunk(frame Frame) error {
	if frame.Sequence != r.nextSeq {
		return ErrUnexpectedSequence
	}
	if frame.Offset != r.offset {
		return ErrUnexpectedOffset
	}
	n, err := r.writer.Write(frame.Data)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTransferFailed, err)
	}
	if n != len(frame.Data) {
		return fmt.Errorf("%w: %w", ErrTransferFailed, io.ErrShortWrite)
	}
	_, _ = r.hash.Write(frame.Data)
	r.offset += int64(len(frame.Data))
	r.nextSeq++
	if r.options.OnProgress != nil {
		r.options.OnProgress(Progress{TransferID: r.transferID, BytesTransferred: r.offset, TotalBytes: r.offset, ChunksTransferred: r.nextSeq})
	}
	return nil
}

func (r *Receiver) acceptManifest(frame Frame) (*Manifest, error) {
	if frame.TotalBytes != r.offset || frame.ChunkCount != r.nextSeq || frame.SHA256Hex != hex.EncodeToString(r.hash.Sum(nil)) {
		return nil, ErrManifestMismatch
	}
	r.complete = true
	manifest := Manifest{TransferID: frame.TransferID, TotalBytes: frame.TotalBytes, ChunkCount: frame.ChunkCount, SHA256Hex: frame.SHA256Hex}
	return &manifest, nil
}
