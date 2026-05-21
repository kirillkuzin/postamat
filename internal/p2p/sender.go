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
}

func StreamReader(ctx context.Context, transferID string, source io.Reader, channel OutboundDataChannel, options SenderOptions) (Manifest, error) {
	if transferID == "" {
		return Manifest{}, ErrTransferIDRequired
	}
	if source == nil || channel == nil {
		return Manifest{}, fmt.Errorf("%w: missing transfer endpoint", ErrTransferFailed)
	}
	chunkSize := options.ChunkSize
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	buffer := make([]byte, chunkSize)
	hash := sha256.New()
	var total int64
	var sequence uint64

	for {
		if err := ctx.Err(); err != nil {
			return Manifest{}, fmt.Errorf("%w: %v", ErrTransferFailed, err)
		}
		n, readErr := source.Read(buffer)
		if n > 0 {
			chunk := append([]byte(nil), buffer[:n]...)
			frame := Frame{Version: ProtocolVersion, Type: FrameTypeChunk, TransferID: transferID, Sequence: sequence, Offset: total, Data: chunk}
			encoded, err := EncodeFrame(frame)
			if err != nil {
				return Manifest{}, err
			}
			if err := waitForBackpressure(ctx, channel, options.MaxBufferedAmount); err != nil {
				return Manifest{}, fmt.Errorf("%w: %v", ErrTransferFailed, err)
			}
			if err := channel.Send(encoded); err != nil {
				return Manifest{}, fmt.Errorf("%w: %v", ErrTransferFailed, err)
			}
			_, _ = hash.Write(chunk)
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

	manifest := Manifest{TransferID: transferID, TotalBytes: total, ChunkCount: sequence, SHA256Hex: hex.EncodeToString(hash.Sum(nil))}
	encoded, err := EncodeManifestFrame(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if err := channel.Send(encoded); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrTransferFailed, err)
	}
	return manifest, nil
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
