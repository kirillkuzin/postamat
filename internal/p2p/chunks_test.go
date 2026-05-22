package p2p

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestChunkFrameRoundTrip(t *testing.T) {
	frame := Frame{
		Version:    ProtocolVersion,
		Type:       FrameTypeChunk,
		TransferID: "tr_123",
		Sequence:   2,
		Offset:     10,
		Data:       []byte("hello"),
	}

	encoded, err := EncodeFrame(frame)
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}
	decoded, err := DecodeFrame(encoded)
	if err != nil {
		t.Fatalf("decode frame: %v", err)
	}

	if decoded.Type != FrameTypeChunk || decoded.TransferID != "tr_123" || decoded.Sequence != 2 || decoded.Offset != 10 || string(decoded.Data) != "hello" {
		t.Fatalf("unexpected decoded frame: %#v", decoded)
	}
}

func TestManifestFrameCarriesFinalHashAndChunkCount(t *testing.T) {
	manifest := Manifest{TransferID: "tr_123", TotalBytes: 11, ChunkCount: 3, SHA256Hex: "b94d27b9934d3e08a52e52d7da7dabfadeb6d4141e61ab56f07dbb4255f47c5b"}
	encoded, err := EncodeManifestFrame(manifest)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	decoded, err := DecodeFrame(encoded)
	if err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	if decoded.Type != FrameTypeManifest || decoded.TransferID != manifest.TransferID || decoded.TotalBytes != manifest.TotalBytes || decoded.ChunkCount != manifest.ChunkCount || decoded.SHA256Hex != manifest.SHA256Hex {
		t.Fatalf("unexpected manifest frame: %#v", decoded)
	}
}

func TestAckFrameRoundTripCarriesNextResumePoint(t *testing.T) {
	ack := Ack{TransferID: "tr_123", NextSequence: 3, NextOffset: 196608}
	encoded, err := EncodeAckFrame(ack)
	if err != nil {
		t.Fatalf("encode ack: %v", err)
	}
	decoded, err := DecodeFrame(encoded)
	if err != nil {
		t.Fatalf("decode ack: %v", err)
	}

	if decoded.Type != FrameTypeAck || decoded.TransferID != ack.TransferID || decoded.NextSequence != ack.NextSequence || decoded.NextOffset != ack.NextOffset {
		t.Fatalf("unexpected ack frame: %#v", decoded)
	}
}

func TestResumeManifestFrameRoundTripCarriesDurableResumePoint(t *testing.T) {
	resume := ResumeManifest{TransferID: "tr_123", NextSequence: 8, NextOffset: 524288, TotalBytes: 1048576, SHA256Hex: "b94d27b9934d3e08a52e52d7da7dabfadeb6d4141e61ab56f07dbb4255f47c5b"}
	encoded, err := EncodeResumeManifestFrame(resume)
	if err != nil {
		t.Fatalf("encode resume manifest: %v", err)
	}
	decoded, err := DecodeFrame(encoded)
	if err != nil {
		t.Fatalf("decode resume manifest: %v", err)
	}

	if decoded.Type != FrameTypeResumeManifest || decoded.TransferID != resume.TransferID || decoded.NextSequence != resume.NextSequence || decoded.NextOffset != resume.NextOffset || decoded.TotalBytes != resume.TotalBytes || decoded.SHA256Hex != resume.SHA256Hex {
		t.Fatalf("unexpected resume manifest frame: %#v", decoded)
	}
}

func TestManifestFrameRequiresFullSHA256Digest(t *testing.T) {
	_, err := EncodeManifestFrame(Manifest{TransferID: "tr_123", TotalBytes: 5, ChunkCount: 1, SHA256Hex: "0000"})
	if !errors.Is(err, ErrSHA256Required) {
		t.Fatalf("EncodeManifestFrame error = %v, want ErrSHA256Required", err)
	}

	encoded, err := encodeFrameUnsafe(Frame{Version: ProtocolVersion, Type: FrameTypeManifest, TransferID: "tr_123", TotalBytes: 5, ChunkCount: 1, SHA256Hex: "0000"})
	if err != nil {
		t.Fatalf("encode unsafe manifest: %v", err)
	}
	_, err = DecodeFrame(encoded)
	if !errors.Is(err, ErrSHA256Required) {
		t.Fatalf("DecodeFrame error = %v, want ErrSHA256Required", err)
	}
}

func TestDecodeRejectsMalformedChunkFrames(t *testing.T) {
	cases := []struct {
		name  string
		frame Frame
		want  error
	}{
		{name: "missing transfer", frame: Frame{Version: ProtocolVersion, Type: FrameTypeChunk, Data: []byte("x")}, want: ErrTransferIDRequired},
		{name: "missing chunk data", frame: Frame{Version: ProtocolVersion, Type: FrameTypeChunk, TransferID: "tr_123"}, want: ErrChunkDataRequired},
		{name: "negative offset", frame: Frame{Version: ProtocolVersion, Type: FrameTypeChunk, TransferID: "tr_123", Offset: -1, Data: []byte("x")}, want: ErrNegativeOffset},
		{name: "unsupported version", frame: Frame{Version: 99, Type: FrameTypeChunk, TransferID: "tr_123", Data: []byte("x")}, want: ErrUnsupportedProtocolVersion},
		{name: "ack missing transfer", frame: Frame{Version: ProtocolVersion, Type: FrameTypeAck, NextSequence: 1, NextOffset: 1}, want: ErrTransferIDRequired},
		{name: "ack negative next offset", frame: Frame{Version: ProtocolVersion, Type: FrameTypeAck, TransferID: "tr_123", NextSequence: 1, NextOffset: -1}, want: ErrNegativeNextOffset},
		{name: "resume missing transfer", frame: Frame{Version: ProtocolVersion, Type: FrameTypeResumeManifest, NextOffset: 1, TotalBytes: 2, SHA256Hex: "0000000000000000000000000000000000000000000000000000000000000000"}, want: ErrTransferIDRequired},
		{name: "resume past total", frame: Frame{Version: ProtocolVersion, Type: FrameTypeResumeManifest, TransferID: "tr_123", NextOffset: 3, TotalBytes: 2, SHA256Hex: "0000000000000000000000000000000000000000000000000000000000000000"}, want: ErrResumePastTotalBytes},
		{name: "resume negative total", frame: Frame{Version: ProtocolVersion, Type: FrameTypeResumeManifest, TransferID: "tr_123", NextOffset: 0, TotalBytes: -1, SHA256Hex: "0000000000000000000000000000000000000000000000000000000000000000"}, want: ErrNegativeTotalBytes},
		{name: "resume invalid sha", frame: Frame{Version: ProtocolVersion, Type: FrameTypeResumeManifest, TransferID: "tr_123", NextOffset: 0, TotalBytes: 2, SHA256Hex: "0000"}, want: ErrSHA256Required},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := encodeFrameUnsafe(tc.frame)
			if err != nil {
				t.Fatalf("encode unsafe frame: %v", err)
			}
			_, err = DecodeFrame(encoded)
			if !errors.Is(err, tc.want) {
				t.Fatalf("DecodeFrame error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReceiverExportsAckAndResumeManifestAfterAcceptedChunks(t *testing.T) {
	dc := newFakeDataChannel()
	if _, err := StreamReader(context.Background(), "tr_123", bytes.NewBufferString("hello world"), dc, SenderOptions{ChunkSize: 5, AllowPlaintext: true}); err != nil {
		t.Fatalf("stream reader: %v", err)
	}

	var out bytes.Buffer
	receiver := NewReceiver("tr_123", &out, ReceiverOptions{AllowPlaintext: true})
	for _, msg := range dc.sent[:2] {
		if _, err := receiver.Accept(msg); err != nil {
			t.Fatalf("receiver accept: %v", err)
		}
	}

	ack := receiver.Ack()
	if ack.TransferID != "tr_123" || ack.NextSequence != 2 || ack.NextOffset != 10 {
		t.Fatalf("receiver ack = %#v", ack)
	}
	resume := receiver.ResumeManifest(11)
	if resume.TransferID != "tr_123" || resume.NextSequence != 2 || resume.NextOffset != 10 || resume.TotalBytes != 11 || resume.SHA256Hex == "" {
		t.Fatalf("receiver resume manifest = %#v", resume)
	}
}

func TestStreamReaderSendsChunksManifestAndProgress(t *testing.T) {
	ctx := context.Background()
	dc := newFakeDataChannel()
	var progress []Progress

	manifest, err := StreamReader(ctx, "tr_123", bytes.NewBufferString("hello world"), dc, SenderOptions{
		ChunkSize:         5,
		MaxBufferedAmount: 4096,
		AllowPlaintext:    true,
		OnProgress: func(p Progress) {
			progress = append(progress, p)
		},
	})
	if err != nil {
		t.Fatalf("stream reader: %v", err)
	}

	if manifest.TotalBytes != 11 || manifest.ChunkCount != 3 || manifest.TransferID != "tr_123" {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	if len(dc.sent) != 4 {
		t.Fatalf("sent frame count = %d, want 4", len(dc.sent))
	}
	if len(progress) != 3 || progress[len(progress)-1].BytesTransferred != 11 || progress[len(progress)-1].TotalBytes != 11 {
		t.Fatalf("unexpected progress: %#v", progress)
	}
	last, err := DecodeFrame(dc.sent[len(dc.sent)-1])
	if err != nil {
		t.Fatalf("decode last frame: %v", err)
	}
	if last.Type != FrameTypeManifest || last.SHA256Hex != manifest.SHA256Hex {
		t.Fatalf("last frame is not manifest: %#v", last)
	}
}

func TestStreamReaderWaitsForBackpressureBeforeSend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	dc := newFakeDataChannel()
	dc.buffered = 100
	dc.releaseAfterWait = true

	if _, err := StreamReader(ctx, "tr_123", bytes.NewBufferString("abcdef"), dc, SenderOptions{ChunkSize: 3, MaxBufferedAmount: 10, AllowPlaintext: true}); err != nil {
		t.Fatalf("stream reader: %v", err)
	}
	if dc.waits == 0 {
		t.Fatal("expected sender to wait for buffered amount to drop")
	}
}

func TestStreamReaderBackpressureHonorsContextWhenBufferDoesNotDrain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	dc := newFakeDataChannel()
	dc.buffered = 100
	dc.returnWithoutDrain = true

	_, err := StreamReader(ctx, "tr_123", bytes.NewBufferString("abcdef"), dc, SenderOptions{ChunkSize: 3, MaxBufferedAmount: 10, AllowPlaintext: true})
	if !errors.Is(err, ErrTransferFailed) {
		t.Fatalf("StreamReader error = %v, want ErrTransferFailed", err)
	}
	if dc.waits == 0 {
		t.Fatal("expected sender to attempt backpressure wait")
	}
}

func TestStreamReaderSendFailureReturnsTransferFailed(t *testing.T) {
	dc := newFakeDataChannel()
	dc.sendErr = io.ErrClosedPipe

	_, err := StreamReader(context.Background(), "tr_123", bytes.NewBufferString("hello"), dc, SenderOptions{ChunkSize: 5, AllowPlaintext: true})
	if !errors.Is(err, ErrTransferFailed) {
		t.Fatalf("StreamReader error = %v, want ErrTransferFailed", err)
	}
}

func TestReceiverAcceptsChunksAndVerifiesManifest(t *testing.T) {
	dc := newFakeDataChannel()
	manifest, err := StreamReader(context.Background(), "tr_123", bytes.NewBufferString("hello world"), dc, SenderOptions{ChunkSize: 4, AllowPlaintext: true})
	if err != nil {
		t.Fatalf("stream reader: %v", err)
	}

	var out bytes.Buffer
	receiver := NewReceiver("tr_123", &out, ReceiverOptions{AllowPlaintext: true})
	var completed *Manifest
	for _, msg := range dc.sent {
		completed, err = receiver.Accept(msg)
		if err != nil {
			t.Fatalf("receiver accept: %v", err)
		}
	}
	if completed == nil || *completed != manifest {
		t.Fatalf("completed manifest = %#v, want %#v", completed, manifest)
	}
	if out.String() != "hello world" {
		t.Fatalf("received %q", out.String())
	}
}

func TestReceiverRejectsWrongSequenceAndHashMismatch(t *testing.T) {
	var out bytes.Buffer
	receiver := NewReceiver("tr_123", &out, ReceiverOptions{AllowPlaintext: true})

	badChunk, err := EncodeFrame(Frame{Version: ProtocolVersion, Type: FrameTypeChunk, TransferID: "tr_123", Sequence: 1, Offset: 0, Data: []byte("oops")})
	if err != nil {
		t.Fatalf("encode bad chunk: %v", err)
	}
	if _, err := receiver.Accept(badChunk); !errors.Is(err, ErrUnexpectedSequence) {
		t.Fatalf("unexpected-sequence error = %v", err)
	}

	receiver = NewReceiver("tr_123", &out, ReceiverOptions{AllowPlaintext: true})
	goodChunk, _ := EncodeFrame(Frame{Version: ProtocolVersion, Type: FrameTypeChunk, TransferID: "tr_123", Sequence: 0, Offset: 0, Data: []byte("hello")})
	if _, err := receiver.Accept(goodChunk); err != nil {
		t.Fatalf("accept good chunk: %v", err)
	}
	badManifest, _ := encodeFrameUnsafe(Frame{Version: ProtocolVersion, Type: FrameTypeManifest, TransferID: "tr_123", TotalBytes: 5, ChunkCount: 1, SHA256Hex: "0000000000000000000000000000000000000000000000000000000000000000"})
	if _, err := receiver.Accept(badManifest); !errors.Is(err, ErrManifestMismatch) {
		t.Fatalf("manifest mismatch error = %v", err)
	}
}

func TestReceiverFailsClosedAfterProtocolError(t *testing.T) {
	var out bytes.Buffer
	receiver := NewReceiver("tr_123", &out, ReceiverOptions{AllowPlaintext: true})
	chunk, _ := EncodeFrame(Frame{Version: ProtocolVersion, Type: FrameTypeChunk, TransferID: "tr_123", Sequence: 0, Offset: 0, Data: []byte("hello")})
	if _, err := receiver.Accept(chunk); err != nil {
		t.Fatalf("accept good chunk: %v", err)
	}
	badManifest, _ := encodeFrameUnsafe(Frame{Version: ProtocolVersion, Type: FrameTypeManifest, TransferID: "tr_123", TotalBytes: 5, ChunkCount: 1, SHA256Hex: "0000000000000000000000000000000000000000000000000000000000000000"})
	if _, err := receiver.Accept(badManifest); !errors.Is(err, ErrManifestMismatch) {
		t.Fatalf("manifest mismatch error = %v", err)
	}
	if _, err := receiver.Accept(chunk); !errors.Is(err, ErrTransferFailed) {
		t.Fatalf("post-failure accept error = %v, want ErrTransferFailed", err)
	}
}

type fakeDataChannel struct {
	sent               [][]byte
	buffered           uint64
	waits              int
	sendErr            error
	releaseAfterWait   bool
	returnWithoutDrain bool
}

func newFakeDataChannel() *fakeDataChannel { return &fakeDataChannel{} }

func (f *fakeDataChannel) Send(data []byte) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	copyData := append([]byte(nil), data...)
	f.sent = append(f.sent, copyData)
	f.buffered += uint64(len(copyData))
	return nil
}

func (f *fakeDataChannel) BufferedAmount() uint64 { return f.buffered }

func (f *fakeDataChannel) WaitBufferedAmountLow(ctx context.Context) error {
	f.waits++
	if f.releaseAfterWait {
		f.buffered = 0
		return nil
	}
	if f.returnWithoutDrain {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}
