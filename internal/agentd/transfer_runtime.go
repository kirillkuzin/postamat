package agentd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/kirillkuzin/postamat/internal/p2p"
)

var (
	ErrTransferKeyRequired     = errors.New("transfer key is required")
	ErrDestinationPathRequired = errors.New("destination path is required")
	ErrTransferJobMismatch     = errors.New("transfer jobs do not describe the same transfer")
)

type LocalWebRTCTransferOptions struct {
	TransferKey       p2p.TransferKey
	ChunkSize         int
	MaxBufferedAmount uint64
}

type LocalWebRTCTransferRunner struct {
	sendJobs    *JobManager
	receiveJobs *JobManager
	options     LocalWebRTCTransferOptions
}

func NewLocalWebRTCTransferRunner(sendJobs *JobManager, receiveJobs *JobManager, options LocalWebRTCTransferOptions) *LocalWebRTCTransferRunner {
	return &LocalWebRTCTransferRunner{sendJobs: sendJobs, receiveJobs: receiveJobs, options: options}
}

func (r *LocalWebRTCTransferRunner) Run(ctx context.Context, sendJobID string, receiveJobID string) (p2p.Manifest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil || r.sendJobs == nil || r.receiveJobs == nil {
		return p2p.Manifest{}, ErrBackendClientRequired
	}
	if r.options.TransferKey == (p2p.TransferKey{}) {
		return p2p.Manifest{}, ErrTransferKeyRequired
	}
	sendJob, err := r.sendJobs.Get(sendJobID)
	if err != nil {
		return p2p.Manifest{}, err
	}
	receiveJob, err := r.receiveJobs.Get(receiveJobID)
	if err != nil {
		return p2p.Manifest{}, err
	}
	if err := validateRunnableTransferJobs(sendJob, receiveJob); err != nil {
		return p2p.Manifest{}, err
	}

	if _, err := r.sendJobs.MarkConnecting(sendJob.ID); err != nil {
		return p2p.Manifest{}, err
	}
	if _, err := r.receiveJobs.MarkConnecting(receiveJob.ID); err != nil {
		_ = failJobIfMutable(r.sendJobs, sendJob.ID, err.Error())
		return p2p.Manifest{}, err
	}
	if _, err := r.sendJobs.MarkTransferring(sendJob.ID); err != nil {
		_ = failJobIfMutable(r.sendJobs, sendJob.ID, err.Error())
		_ = failJobIfMutable(r.receiveJobs, receiveJob.ID, err.Error())
		return p2p.Manifest{}, err
	}
	if _, err := r.receiveJobs.MarkTransferring(receiveJob.ID); err != nil {
		_ = failJobIfMutable(r.sendJobs, sendJob.ID, err.Error())
		_ = failJobIfMutable(r.receiveJobs, receiveJob.ID, err.Error())
		return p2p.Manifest{}, err
	}

	manifest, err := r.runEncryptedLocalWebRTC(ctx, sendJob, receiveJob)
	if err != nil {
		_ = failJobIfMutable(r.sendJobs, sendJob.ID, err.Error())
		_ = failJobIfMutable(r.receiveJobs, receiveJob.ID, err.Error())
		return p2p.Manifest{}, err
	}
	if _, err := r.receiveJobs.Complete(receiveJob.ID); err != nil {
		_ = failJobIfMutable(r.sendJobs, sendJob.ID, err.Error())
		return p2p.Manifest{}, err
	}
	if _, err := r.sendJobs.Complete(sendJob.ID); err != nil {
		return p2p.Manifest{}, err
	}
	return manifest, nil
}

func (r *LocalWebRTCTransferRunner) runEncryptedLocalWebRTC(ctx context.Context, sendJob Job, receiveJob Job) (p2p.Manifest, error) {
	transferCtx, cancelTransfer := context.WithCancel(ctx)
	defer cancelTransfer()

	source, err := os.Open(sendJob.SourcePath)
	if err != nil {
		return p2p.Manifest{}, fmt.Errorf("open source: %w", err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return p2p.Manifest{}, fmt.Errorf("stat source: %w", err)
	}
	if info.Size() != sendJob.FileSizeBytes {
		return p2p.Manifest{}, fmt.Errorf("source size %d does not match declared transfer size %d", info.Size(), sendJob.FileSizeBytes)
	}

	if _, err := os.Lstat(receiveJob.DestinationPath); err == nil {
		return p2p.Manifest{}, fmt.Errorf("destination exists: %s", receiveJob.DestinationPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return p2p.Manifest{}, fmt.Errorf("stat destination: %w", err)
	}
	partialPath := receiveJob.DestinationPath + ".part"
	destination, err := os.OpenFile(partialPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return p2p.Manifest{}, fmt.Errorf("create partial destination: %w", err)
	}
	partialCommitted := false
	defer func() {
		_ = destination.Close()
		if !partialCommitted {
			_ = os.Remove(partialPath)
		}
	}()

	senderCipher, err := p2p.NewChunkCipher(r.options.TransferKey)
	if err != nil {
		return p2p.Manifest{}, err
	}
	receiverCipher, err := p2p.NewChunkCipher(r.options.TransferKey)
	if err != nil {
		return p2p.Manifest{}, err
	}
	channel, incoming, closePair, err := p2p.NewLocalWebRTCPair(transferCtx, "postamat-transfer")
	if err != nil {
		return p2p.Manifest{}, err
	}
	defer closePair()

	receiver := p2p.NewReceiver(receiveJob.TransferID, destination, p2p.ReceiverOptions{
		Encryption:        receiverCipher,
		RequireEncryption: true,
		OnProgress: func(progress p2p.Progress) {
			_, _ = r.receiveJobs.UpdateProgress(receiveJob.ID, progress.BytesTransferred)
		},
	})
	receiverDone := make(chan error, 1)
	go func() {
		for {
			select {
			case <-transferCtx.Done():
				receiverDone <- transferCtx.Err()
				return
			case message := <-incoming:
				manifest, err := receiver.Accept(message)
				if err != nil {
					receiverDone <- err
					return
				}
				if manifest != nil {
					receiverDone <- nil
					return
				}
			}
		}
	}()

	manifest, err := p2p.StreamReader(transferCtx, sendJob.TransferID, source, channel, p2p.SenderOptions{
		ChunkSize:         r.options.ChunkSize,
		MaxBufferedAmount: r.options.MaxBufferedAmount,
		Encryption:        senderCipher,
		OnProgress: func(progress p2p.Progress) {
			_, _ = r.sendJobs.UpdateProgress(sendJob.ID, progress.BytesTransferred)
		},
	})
	if err != nil {
		return p2p.Manifest{}, err
	}
	select {
	case err := <-receiverDone:
		if err != nil {
			return p2p.Manifest{}, err
		}
	case <-transferCtx.Done():
		return p2p.Manifest{}, fmt.Errorf("%w: %v", p2p.ErrTransferFailed, transferCtx.Err())
	}
	if manifest.TotalBytes != receiveJob.FileSizeBytes {
		return p2p.Manifest{}, fmt.Errorf("manifest total %d does not match declared transfer size %d", manifest.TotalBytes, receiveJob.FileSizeBytes)
	}
	if err := destination.Close(); err != nil {
		return p2p.Manifest{}, fmt.Errorf("close partial destination: %w", err)
	}
	if err := commitDestinationNoReplace(partialPath, receiveJob.DestinationPath); err != nil {
		return p2p.Manifest{}, fmt.Errorf("commit destination: %w", err)
	}
	partialCommitted = true
	return manifest, nil
}

func commitDestinationNoReplace(partialPath string, destinationPath string) error {
	if err := os.Link(partialPath, destinationPath); err != nil {
		return err
	}
	if err := os.Remove(partialPath); err != nil {
		return err
	}
	return nil
}

func validateRunnableTransferJobs(sendJob Job, receiveJob Job) error {
	if sendJob.Direction != JobDirectionSend || receiveJob.Direction != JobDirectionReceive {
		return ErrTransferJobMismatch
	}
	if sendJob.TransferID == "" || receiveJob.TransferID == "" {
		return ErrTransferIDRequired
	}
	if sendJob.TransferID != receiveJob.TransferID || sendJob.FileName != receiveJob.FileName || sendJob.FileSizeBytes != receiveJob.FileSizeBytes {
		return ErrTransferJobMismatch
	}
	if sendJob.Status != JobStatusAccepted || receiveJob.Status != JobStatusAccepted {
		return ErrInvalidJobStatus
	}
	if sendJob.SourcePath == "" {
		return ErrSourcePathRequired
	}
	if receiveJob.DestinationPath == "" {
		return ErrDestinationPathRequired
	}
	return nil
}

func failJobIfMutable(jobs *JobManager, jobID string, reason string) error {
	if jobs == nil || reason == "" {
		return nil
	}
	_, err := jobs.Fail(jobID, reason)
	if errors.Is(err, ErrJobTerminal) {
		return nil
	}
	return err
}
