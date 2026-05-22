package p2p

import (
	"bytes"
	"context"
	"errors"
	"sync"
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

func TestRemoteWebRTCPeersTransferWithBufferedICE(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var sender *RemoteWebRTCPeer
	var receiver *RemoteWebRTCPeer
	var earlySenderICE []ICECandidate
	var earlyReceiverICE []ICECandidate
	var iceMu sync.Mutex
	var err error
	sender, err = NewRemoteWebRTCOfferPeer("postamat-transfer", func(candidate ICECandidate) {
		iceMu.Lock()
		defer iceMu.Unlock()
		if receiver == nil {
			earlySenderICE = append(earlySenderICE, candidate)
			return
		}
		_ = receiver.AddICECandidate(candidate)
	})
	if err != nil {
		t.Fatalf("NewRemoteWebRTCOfferPeer: %v", err)
	}
	defer sender.Close()
	receiver, err = NewRemoteWebRTCAnswerPeer(func(candidate ICECandidate) {
		iceMu.Lock()
		defer iceMu.Unlock()
		if sender == nil {
			earlyReceiverICE = append(earlyReceiverICE, candidate)
			return
		}
		_ = sender.AddICECandidate(candidate)
	})
	if err != nil {
		t.Fatalf("NewRemoteWebRTCAnswerPeer: %v", err)
	}
	defer receiver.Close()

	offer, err := sender.CreateOffer()
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	iceMu.Lock()
	bufferedSenderICE := append([]ICECandidate(nil), earlySenderICE...)
	iceMu.Unlock()
	for _, candidate := range bufferedSenderICE {
		if err := receiver.AddICECandidate(candidate); err != nil {
			t.Fatalf("buffer receiver ICE candidate before remote description: %v", err)
		}
	}
	answer, err := receiver.AcceptOfferCreateAnswer(offer)
	if err != nil {
		t.Fatalf("AcceptOfferCreateAnswer: %v", err)
	}
	iceMu.Lock()
	bufferedReceiverICE := append([]ICECandidate(nil), earlyReceiverICE...)
	iceMu.Unlock()
	for _, candidate := range bufferedReceiverICE {
		if err := sender.AddICECandidate(candidate); err != nil {
			t.Fatalf("buffer sender ICE candidate before remote description: %v", err)
		}
	}
	if err := sender.AcceptAnswer(answer); err != nil {
		t.Fatalf("AcceptAnswer: %v", err)
	}

	senderChannel, err := sender.WaitOutboundDataChannel(ctx, 64*1024)
	if err != nil {
		t.Fatalf("WaitOutboundDataChannel: %v", err)
	}
	incoming, err := receiver.WaitIncomingMessages(ctx)
	if err != nil {
		t.Fatalf("WaitIncomingMessages: %v", err)
	}

	key := mustTestTransferKey(t)
	var received bytes.Buffer
	receiverRuntime := NewReceiver("tr_remote", &received, ReceiverOptions{Encryption: mustTestChunkCipher(t, key), RequireEncryption: true})
	done := make(chan error, 1)
	go func() {
		for message := range incoming {
			manifest, err := receiverRuntime.Accept(message)
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

	if _, err := StreamReader(ctx, "tr_remote", bytes.NewBufferString("remote webrtc payload"), senderChannel, SenderOptions{ChunkSize: 5, Encryption: mustTestChunkCipher(t, key)}); err != nil {
		t.Fatalf("StreamReader: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("receive remote webrtc: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for remote transfer: %v", ctx.Err())
	}
	if received.String() != "remote webrtc payload" {
		t.Fatalf("received %q", received.String())
	}
}
