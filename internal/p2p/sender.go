package p2p

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

const defaultChunkSize = 64 * 1024

// OutboundDataChannel is the small DataChannel surface needed by the sender
// loop. It is implemented by both test fakes and the Pion adapter in webrtc.go.
type OutboundDataChannel interface {
	Send([]byte) error
	BufferedAmount() uint64
	WaitBufferedAmountLow(context.Context) error
}

type bufferedAmountThresholdSetter interface {
	SetBufferedAmountLowThreshold(uint64)
}

type SenderOptions struct {
	ChunkSize         int
	MaxBufferedAmount uint64
	OnProgress        func(Progress)
	Encryption        *ChunkCipher
	AllowPlaintext    bool
	Resume            *ResumeManifest
}

func StreamReader(ctx context.Context, transferID string, source io.Reader, channel OutboundDataChannel, options SenderOptions) (Manifest, error) {
	if transferID == "" {
		return Manifest{}, ErrTransferIDRequired
	}
	if source == nil || channel == nil {
		return Manifest{}, fmt.Errorf("%w: missing transfer endpoint", ErrTransferFailed)
	}
	if options.Encryption == nil && !options.AllowPlaintext {
		return Manifest{}, ErrEncryptionRequired
	}
	chunkSize := options.ChunkSize
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	buffer := make([]byte, chunkSize)
	hash := sha256.New()
	var total int64
	var sequence uint64
	if options.Resume != nil {
		if err := prepareSenderResume(source, transferID, options.Resume, hash); err != nil {
			return Manifest{}, err
		}
		total = options.Resume.NextOffset
		sequence = options.Resume.NextSequence
	}

	for {
		if err := ctx.Err(); err != nil {
			return Manifest{}, fmt.Errorf("%w: %w", ErrTransferFailed, err)
		}
		n, readErr := source.Read(buffer)
		if n > 0 {
			if err := sendChunk(ctx, transferID, channel, options, hash, sequence, total, buffer[:n]); err != nil {
				return Manifest{}, err
			}
			total += int64(n)
			sequence++
			if options.OnProgress != nil {
				options.OnProgress(Progress{TransferID: transferID, BytesTransferred: total, TotalBytes: total, ChunksTransferred: sequence})
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return Manifest{}, fmt.Errorf("%w: %v", ErrTransferFailed, readErr)
		}
	}

	if total == 0 && sequence == 0 && options.Encryption != nil {
		if err := sendChunk(ctx, transferID, channel, options, hash, sequence, total, nil); err != nil {
			return Manifest{}, err
		}
		sequence++
		if options.OnProgress != nil {
			options.OnProgress(Progress{TransferID: transferID, BytesTransferred: total, TotalBytes: total, ChunksTransferred: sequence})
		}
	}

	manifest := Manifest{TransferID: transferID, TotalBytes: total, ChunkCount: sequence, SHA256Hex: hex.EncodeToString(hash.Sum(nil))}
	if options.Resume != nil && options.Resume.TotalBytes != total {
		return Manifest{}, ErrManifestMismatch
	}
	encoded, err := EncodeManifestFrame(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if err := channel.Send(encoded); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrTransferFailed, err)
	}
	return manifest, nil
}

func prepareSenderResume(source io.Reader, transferID string, resume *ResumeManifest, digest io.Writer) error {
	if resume == nil {
		return nil
	}
	if resume.TransferID != transferID {
		return ErrUnexpectedTransferID
	}
	if resume.NextOffset < 0 || resume.NextOffset > resume.TotalBytes {
		return ErrResumePastTotalBytes
	}
	if seeker, ok := source.(io.Seeker); ok {
		if _, err := seeker.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("%w: %v", ErrTransferFailed, err)
		}
	}
	copied, err := io.CopyN(digest, source, resume.NextOffset)
	if err != nil && err != io.EOF {
		return fmt.Errorf("%w: %v", ErrTransferFailed, err)
	}
	if copied != resume.NextOffset {
		return ErrUnexpectedOffset
	}
	if hexDigest, ok := digest.(interface{ Sum([]byte) []byte }); ok {
		if hex.EncodeToString(hexDigest.Sum(nil)) != resume.SHA256Hex {
			return ErrResumeDigestMismatch
		}
	}
	if seeker, ok := source.(io.Seeker); ok {
		if _, err := seeker.Seek(resume.NextOffset, io.SeekStart); err != nil {
			return fmt.Errorf("%w: %v", ErrTransferFailed, err)
		}
	}
	return nil
}

func sendChunk(ctx context.Context, transferID string, channel OutboundDataChannel, options SenderOptions, digest io.Writer, sequence uint64, offset int64, plaintext []byte) error {
	chunk := append([]byte(nil), plaintext...)
	frameData := chunk
	var encryption *EncryptionMetadata
	var err error
	if options.Encryption != nil {
		frameData, encryption, err = options.Encryption.EncryptChunk(transferID, sequence, offset, chunk)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrTransferFailed, err)
		}
	}
	frame := Frame{Version: ProtocolVersion, Type: FrameTypeChunk, TransferID: transferID, Sequence: sequence, Offset: offset, Data: frameData, Encryption: encryption}
	encoded, err := EncodeFrame(frame)
	if err != nil {
		return err
	}
	if err := waitForBackpressure(ctx, channel, options.MaxBufferedAmount); err != nil {
		return fmt.Errorf("%w: %w", ErrTransferFailed, err)
	}
	if err := channel.Send(encoded); err != nil {
		return fmt.Errorf("%w: %v", ErrTransferFailed, err)
	}
	_, _ = digest.Write(chunk)
	return nil
}

func waitForBackpressure(ctx context.Context, channel OutboundDataChannel, maxBuffered uint64) error {
	if maxBuffered == 0 {
		return nil
	}
	if setter, ok := channel.(bufferedAmountThresholdSetter); ok {
		setter.SetBufferedAmountLowThreshold(maxBuffered)
	}
	for channel.BufferedAmount() > maxBuffered {
		if err := ctx.Err(); err != nil {
			return err
		}
		before := channel.BufferedAmount()
		if err := channel.WaitBufferedAmountLow(ctx); err != nil {
			return err
		}
		if after := channel.BufferedAmount(); after >= before && after > maxBuffered {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	return nil
}
