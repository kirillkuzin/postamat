package p2p

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

type ReceiverOptions struct {
	OnProgress        func(Progress)
	Encryption        *ChunkCipher
	RequireEncryption bool
	AllowPlaintext    bool
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

func NewReceiverFromResume(transferID string, destination io.Writer, durablePrefix io.Reader, resume ResumeManifest, options ReceiverOptions) (*Receiver, error) {
	if transferID == "" || destination == nil || durablePrefix == nil {
		return nil, fmt.Errorf("%w: missing receiver endpoint", ErrTransferFailed)
	}
	if resume.TransferID != transferID {
		return nil, ErrUnexpectedTransferID
	}
	if resume.NextOffset < 0 || resume.NextOffset > resume.TotalBytes {
		return nil, ErrResumePastTotalBytes
	}
	receiver := NewReceiver(transferID, destination, options)
	copied, err := io.CopyN(receiver.hash, durablePrefix, resume.NextOffset)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("%w: %v", ErrTransferFailed, err)
	}
	if copied != resume.NextOffset {
		return nil, ErrUnexpectedOffset
	}
	if hex.EncodeToString(receiver.hash.Sum(nil)) != resume.SHA256Hex {
		return nil, ErrResumeDigestMismatch
	}
	receiver.nextSeq = resume.NextSequence
	receiver.offset = resume.NextOffset
	return receiver, nil
}

func (r *Receiver) Ack() Ack {
	if r == nil {
		return Ack{}
	}
	return Ack{TransferID: r.transferID, NextSequence: r.nextSeq, NextOffset: r.offset}
}

func (r *Receiver) ResumeManifest(totalBytes int64) ResumeManifest {
	if r == nil {
		return ResumeManifest{}
	}
	return ResumeManifest{TransferID: r.transferID, NextSequence: r.nextSeq, NextOffset: r.offset, TotalBytes: totalBytes, SHA256Hex: hex.EncodeToString(r.hash.Sum(nil))}
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
		if !r.options.AllowPlaintext && r.nextSeq == 0 {
			r.failed = true
			return nil, ErrEncryptionRequired
		}
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
	data := frame.Data
	if frame.Encryption != nil {
		plaintext, err := r.options.Encryption.DecryptChunk(frame.TransferID, frame.Sequence, frame.Offset, frame.Encryption, frame.Data)
		if err != nil {
			return err
		}
		data = plaintext
	} else if r.options.RequireEncryption || !r.options.AllowPlaintext {
		return ErrEncryptionRequired
	}
	n, err := r.writer.Write(data)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTransferFailed, err)
	}
	if n != len(data) {
		return fmt.Errorf("%w: %w", ErrTransferFailed, io.ErrShortWrite)
	}
	_, _ = r.hash.Write(data)
	r.offset += int64(len(data))
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
