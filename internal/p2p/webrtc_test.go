package p2p

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestLocalWebRTCPairTransfersSmallFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sender, incoming, closePair, err := NewLocalWebRTCPair(ctx, "postamat-transfer")
	if err != nil {
		t.Fatalf("create local webrtc pair: %v", err)
	}
	defer closePair()

	key := mustTestTransferKey(t)
	var received bytes.Buffer
	receiver := NewReceiver("tr_local", &received, ReceiverOptions{Encryption: mustTestChunkCipher(t, key)})
	done := make(chan error, 1)
	go func() {
		for msg := range incoming {
			manifest, err := receiver.Accept(msg)
			if err != nil {
				done <- err
				return
			}
			if manifest != nil {
				done <- nil
				return
			}
		}
		done <- ErrTransferFailed
	}()

	manifest, err := StreamReader(ctx, "tr_local", bytes.NewBufferString("web rtc payload"), sender, SenderOptions{ChunkSize: 4, MaxBufferedAmount: 64, Encryption: mustTestChunkCipher(t, key)})
	if err != nil {
		t.Fatalf("stream over local webrtc: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("receive over local webrtc: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for local webrtc transfer: %v", ctx.Err())
	}
	if received.String() != "web rtc payload" {
		t.Fatalf("received %q", received.String())
	}
	if manifest.TotalBytes != int64(len("web rtc payload")) {
		t.Fatalf("manifest total = %d", manifest.TotalBytes)
	}
}

func TestLocalWebRTCPairFailureDoesNotHang(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sender, _, closePair, err := NewLocalWebRTCPair(ctx, "postamat-transfer")
	if err != nil {
		t.Fatalf("create local webrtc pair: %v", err)
	}
	closePair()

	_, err = StreamReader(ctx, "tr_local", bytes.NewBuffer(make([]byte, 128)), sender, SenderOptions{ChunkSize: 64, MaxBufferedAmount: 64, Encryption: mustTestChunkCipher(t, mustTestTransferKey(t))})
	if !errors.Is(err, ErrTransferFailed) {
		t.Fatalf("stream after local peer failure error = %v, want ErrTransferFailed", err)
	}
}
