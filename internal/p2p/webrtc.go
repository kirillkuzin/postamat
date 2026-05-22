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

type SessionDescription = webrtc.SessionDescription
type ICECandidate = webrtc.ICECandidateInit

type RemoteWebRTCPeer struct {
	mu         sync.Mutex
	pc         *webrtc.PeerConnection
	dc         *webrtc.DataChannel
	incoming   chan []byte
	open       chan struct{}
	done       chan struct{}
	openOnce   sync.Once
	closeOnce  sync.Once
	pendingICE []webrtc.ICECandidateInit
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

func NewRemoteWebRTCOfferPeer(label string, onICECandidate func(ICECandidate)) (*RemoteWebRTCPeer, error) {
	if label == "" {
		label = "postamat-transfer"
	}
	peer, err := newRemoteWebRTCPeer(onICECandidate)
	if err != nil {
		return nil, err
	}
	dc, err := peer.pc.CreateDataChannel(label, nil)
	if err != nil {
		peer.Close()
		return nil, err
	}
	peer.setDataChannel(dc)
	return peer, nil
}

func NewRemoteWebRTCAnswerPeer(onICECandidate func(ICECandidate)) (*RemoteWebRTCPeer, error) {
	peer, err := newRemoteWebRTCPeer(onICECandidate)
	if err != nil {
		return nil, err
	}
	peer.pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		peer.setDataChannel(dc)
	})
	return peer, nil
}

func newRemoteWebRTCPeer(onICECandidate func(ICECandidate)) (*RemoteWebRTCPeer, error) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	peer := &RemoteWebRTCPeer{
		pc:       pc,
		incoming: make(chan []byte, 64),
		open:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		switch state {
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateDisconnected:
			peer.Close()
		}
	})
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil && onICECandidate != nil {
			onICECandidate(candidate.ToJSON())
		}
	})
	return peer, nil
}

func (p *RemoteWebRTCPeer) setDataChannel(dc *webrtc.DataChannel) {
	p.mu.Lock()
	p.dc = dc
	p.mu.Unlock()
	dc.OnOpen(func() {
		p.openOnce.Do(func() { close(p.open) })
	})
	dc.OnClose(func() { p.Close() })
	dc.OnError(func(error) { p.Close() })
	dc.OnMessage(func(message webrtc.DataChannelMessage) {
		data := append([]byte(nil), message.Data...)
		select {
		case p.incoming <- data:
		case <-p.done:
		}
	})
}

func (p *RemoteWebRTCPeer) CreateOffer() (SessionDescription, error) {
	if p == nil || p.pc == nil {
		return webrtc.SessionDescription{}, fmt.Errorf("%w: missing peer connection", ErrTransferFailed)
	}
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	if err := p.pc.SetLocalDescription(offer); err != nil {
		return webrtc.SessionDescription{}, err
	}
	return offer, nil
}

func (p *RemoteWebRTCPeer) AcceptOfferCreateAnswer(offer SessionDescription) (SessionDescription, error) {
	if p == nil || p.pc == nil {
		return webrtc.SessionDescription{}, fmt.Errorf("%w: missing peer connection", ErrTransferFailed)
	}
	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return webrtc.SessionDescription{}, err
	}
	if err := p.flushPendingICE(); err != nil {
		return webrtc.SessionDescription{}, err
	}
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return webrtc.SessionDescription{}, err
	}
	return answer, nil
}

func (p *RemoteWebRTCPeer) AcceptAnswer(answer SessionDescription) error {
	if p == nil || p.pc == nil {
		return fmt.Errorf("%w: missing peer connection", ErrTransferFailed)
	}
	if err := p.pc.SetRemoteDescription(answer); err != nil {
		return err
	}
	return p.flushPendingICE()
}

const maxRemotePendingICECandidates = 64

func (p *RemoteWebRTCPeer) AddICECandidate(candidate ICECandidate) error {
	if p == nil || p.pc == nil {
		return fmt.Errorf("%w: missing peer connection", ErrTransferFailed)
	}
	p.mu.Lock()
	if p.pc.RemoteDescription() == nil {
		if len(p.pendingICE) >= maxRemotePendingICECandidates {
			p.mu.Unlock()
			p.Close()
			return fmt.Errorf("%w: too many pending ICE candidates", ErrTransferFailed)
		}
		p.pendingICE = append(p.pendingICE, candidate)
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()
	return p.pc.AddICECandidate(candidate)
}

func (p *RemoteWebRTCPeer) flushPendingICE() error {
	p.mu.Lock()
	pending := append([]webrtc.ICECandidateInit(nil), p.pendingICE...)
	p.pendingICE = nil
	p.mu.Unlock()
	for _, candidate := range pending {
		if err := p.pc.AddICECandidate(candidate); err != nil {
			return err
		}
	}
	return nil
}

func (p *RemoteWebRTCPeer) WaitOutboundDataChannel(ctx context.Context, lowThreshold uint64) (OutboundDataChannel, error) {
	dc, err := p.waitDataChannel(ctx)
	if err != nil {
		return nil, err
	}
	return NewPionDataChannel(dc, lowThreshold), nil
}

func (p *RemoteWebRTCPeer) WaitIncomingMessages(ctx context.Context) (<-chan []byte, error) {
	if _, err := p.waitDataChannel(ctx); err != nil {
		return nil, err
	}
	return p.incoming, nil
}

func (p *RemoteWebRTCPeer) waitDataChannel(ctx context.Context) (*webrtc.DataChannel, error) {
	if p == nil || p.pc == nil {
		return nil, fmt.Errorf("%w: missing peer connection", ErrTransferFailed)
	}
	select {
	case <-p.open:
	case <-p.done:
		return nil, fmt.Errorf("%w: data channel closed before open", ErrTransferFailed)
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: data channel open: %v", ErrTransferFailed, ctx.Err())
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dc == nil {
		return nil, fmt.Errorf("%w: missing data channel", ErrTransferFailed)
	}
	return p.dc, nil
}

func (p *RemoteWebRTCPeer) Done() <-chan struct{} {
	if p == nil || p.done == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return p.done
}

func (p *RemoteWebRTCPeer) Close() {
	if p == nil {
		return
	}
	p.closeOnce.Do(func() {
		close(p.done)
		if p.pc != nil {
			_ = p.pc.Close()
		}
	})
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
