package p2p

import (
	"context"
	"fmt"
	"sync"

	"github.com/pion/webrtc/v4"
)

type PionDataChannel struct {
	dc           *webrtc.DataChannel
	lowThreshold uint64
	low          chan struct{}
}

func NewPionDataChannel(dc *webrtc.DataChannel, lowThreshold uint64) *PionDataChannel {
	adapter := &PionDataChannel{dc: dc, lowThreshold: lowThreshold, low: make(chan struct{}, 1)}
	if dc != nil && lowThreshold > 0 {
		adapter.SetBufferedAmountLowThreshold(lowThreshold)
		dc.OnBufferedAmountLow(func() {
			select {
			case adapter.low <- struct{}{}:
			default:
			}
		})
	}
	return adapter
}

func (p *PionDataChannel) SetBufferedAmountLowThreshold(threshold uint64) {
	if p == nil || p.dc == nil {
		return
	}
	p.lowThreshold = threshold
	p.dc.SetBufferedAmountLowThreshold(threshold)
}

func (p *PionDataChannel) Send(data []byte) error {
	if p == nil || p.dc == nil {
		return fmt.Errorf("%w: missing data channel", ErrTransferFailed)
	}
	if err := p.dc.Send(data); err != nil {
		return fmt.Errorf("%w: %v", ErrTransferFailed, err)
	}
	return nil
}

func (p *PionDataChannel) BufferedAmount() uint64 {
	if p == nil || p.dc == nil {
		return 0
	}
	return p.dc.BufferedAmount()
}

func (p *PionDataChannel) WaitBufferedAmountLow(ctx context.Context) error {
	if p == nil || p.dc == nil || p.lowThreshold == 0 || p.dc.BufferedAmount() <= p.lowThreshold {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.low:
		return nil
	}
}

func NewLocalWebRTCPair(ctx context.Context, label string) (OutboundDataChannel, <-chan []byte, func(), error) {
	if label == "" {
		label = "postamat-transfer"
	}
	config := webrtc.Configuration{}
	senderPeer, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, nil, nil, err
	}
	receiverPeer, err := webrtc.NewPeerConnection(config)
	if err != nil {
		_ = senderPeer.Close()
		return nil, nil, nil, err
	}

	var closeOnce sync.Once
	closePair := func() {
		closeOnce.Do(func() {
			_ = senderPeer.Close()
			_ = receiverPeer.Close()
		})
	}

	received := make(chan []byte, 64)
	receiverReady := make(chan struct{})
	var receiverReadyOnce sync.Once
	receiverPeer.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnOpen(func() {
			receiverReadyOnce.Do(func() { close(receiverReady) })
		})
		dc.OnMessage(func(message webrtc.DataChannelMessage) {
			data := append([]byte(nil), message.Data...)
			select {
			case received <- data:
			case <-ctx.Done():
			}
		})
	})

	senderPeer.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			_ = receiverPeer.AddICECandidate(candidate.ToJSON())
		}
	})
	receiverPeer.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			_ = senderPeer.AddICECandidate(candidate.ToJSON())
		}
	})

	dc, err := senderPeer.CreateDataChannel(label, nil)
	if err != nil {
		closePair()
		return nil, nil, nil, err
	}
	senderOpen := make(chan struct{})
	var senderOpenOnce sync.Once
	dc.OnOpen(func() { senderOpenOnce.Do(func() { close(senderOpen) }) })

	offer, err := senderPeer.CreateOffer(nil)
	if err != nil {
		closePair()
		return nil, nil, nil, err
	}
	if err := senderPeer.SetLocalDescription(offer); err != nil {
		closePair()
		return nil, nil, nil, err
	}
	if err := receiverPeer.SetRemoteDescription(offer); err != nil {
		closePair()
		return nil, nil, nil, err
	}
	answer, err := receiverPeer.CreateAnswer(nil)
	if err != nil {
		closePair()
		return nil, nil, nil, err
	}
	if err := receiverPeer.SetLocalDescription(answer); err != nil {
		closePair()
		return nil, nil, nil, err
	}
	if err := senderPeer.SetRemoteDescription(answer); err != nil {
		closePair()
		return nil, nil, nil, err
	}

	select {
	case <-senderOpen:
	case <-ctx.Done():
		closePair()
		return nil, nil, nil, fmt.Errorf("%w: sender data channel open: %v", ErrTransferFailed, ctx.Err())
	}
	select {
	case <-receiverReady:
	case <-ctx.Done():
		closePair()
		return nil, nil, nil, fmt.Errorf("%w: receiver data channel open: %v", ErrTransferFailed, ctx.Err())
	}

	return NewPionDataChannel(dc, 64*1024), received, closePair, nil
}
