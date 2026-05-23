package agentd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kirillkuzin/postamat/internal/p2p"
)

var (
	ErrTransferKeyRequired      = errors.New("transfer key is required")
	ErrDestinationPathRequired  = errors.New("destination path is required")
	ErrTransferJobMismatch      = errors.New("transfer jobs do not describe the same transfer")
	ErrUnsafePartialDestination = errors.New("partial destination is not a verified regular file")
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
		if isRetryableRuntimeError(err) {
			_ = markJobRetryableIfMutable(r.sendJobs, sendJob.ID, err.Error())
			_ = markJobRetryableIfMutable(r.receiveJobs, receiveJob.ID, err.Error())
		} else {
			_ = failJobIfMutable(r.sendJobs, sendJob.ID, err.Error())
			_ = failJobIfMutable(r.receiveJobs, receiveJob.ID, err.Error())
		}
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
	resume, resumed, partialInfo, err := prepareRuntimeResume(partialPath, receiveJob.TransferID, receiveJob.FileSizeBytes, r.options.ChunkSize)
	if err != nil {
		return p2p.Manifest{}, err
	}
	if resumed {
		if _, err := source.Seek(resume.NextOffset, io.SeekStart); err != nil {
			return p2p.Manifest{}, fmt.Errorf("seek source for resume: %w", err)
		}
	}
	destination, err := openPartialForAppend(partialPath, resumed, partialInfo)
	if err != nil {
		return p2p.Manifest{}, err
	}
	openedPartialInfo, err := destination.Stat()
	if err != nil {
		_ = destination.Close()
		return p2p.Manifest{}, fmt.Errorf("stat open partial destination: %w", err)
	}
	partialInfo = openedPartialInfo
	partialCommitted := false
	preservePartial := resumed
	defer func() {
		_ = destination.Close()
		if !partialCommitted && !preservePartial && transferCtx.Err() == nil {
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

	receiverOptions := p2p.ReceiverOptions{
		Encryption:        receiverCipher,
		RequireEncryption: true,
		OnProgress: func(progress p2p.Progress) {
			_, _ = r.receiveJobs.UpdateProgress(receiveJob.ID, progress.BytesTransferred)
		},
	}
	var receiver *p2p.Receiver
	if resumed {
		prefix, err := openVerifiedPartialForRead(partialPath, partialInfo)
		if err != nil {
			return p2p.Manifest{}, err
		}
		receiver, err = p2p.NewReceiverFromResume(receiveJob.TransferID, destination, prefix, resume, receiverOptions)
		_ = prefix.Close()
		if err != nil {
			return p2p.Manifest{}, err
		}
	} else {
		receiver = p2p.NewReceiver(receiveJob.TransferID, destination, receiverOptions)
	}
	receiverDone := make(chan error, 1)
	go func() {
		for {
			if err := transferCtx.Err(); err != nil {
				receiverDone <- err
				return
			}
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
		Resume:            resumeIfActive(resumed, resume),
		OnProgress: func(progress p2p.Progress) {
			_, _ = r.sendJobs.UpdateProgress(sendJob.ID, progress.BytesTransferred)
		},
	})
	if err != nil {
		if isRetryableRuntimeError(err) {
			preservePartial = true
			cancelTransfer()
			closePair()
			<-receiverDone
		}
		return p2p.Manifest{}, err
	}
	select {
	case err := <-receiverDone:
		if err != nil {
			return p2p.Manifest{}, err
		}
	case <-transferCtx.Done():
		return p2p.Manifest{}, fmt.Errorf("%w: %w", p2p.ErrTransferFailed, transferCtx.Err())
	}
	if manifest.TotalBytes != receiveJob.FileSizeBytes {
		return p2p.Manifest{}, fmt.Errorf("manifest total %d does not match declared transfer size %d", manifest.TotalBytes, receiveJob.FileSizeBytes)
	}
	if err := destination.Close(); err != nil {
		return p2p.Manifest{}, fmt.Errorf("close partial destination: %w", err)
	}
	if err := commitDestinationNoReplace(partialPath, receiveJob.DestinationPath, partialInfo, manifest); err != nil {
		return p2p.Manifest{}, fmt.Errorf("commit destination: %w", err)
	}
	partialCommitted = true
	return manifest, nil
}

func commitDestinationNoReplace(partialPath string, destinationPath string, expected os.FileInfo, manifest p2p.Manifest) error {
	if expected == nil {
		return ErrUnsafePartialDestination
	}
	source, err := openVerifiedPartialForRead(partialPath, expected)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		_ = destination.Close()
		if !committed {
			_ = os.Remove(destinationPath)
		}
	}()
	digest := sha256.New()
	copied, err := io.Copy(destination, io.TeeReader(source, digest))
	if err != nil {
		return err
	}
	if copied != manifest.TotalBytes || hex.EncodeToString(digest.Sum(nil)) != manifest.SHA256Hex {
		return p2p.ErrManifestMismatch
	}
	if err := destination.Close(); err != nil {
		return err
	}
	committedInfo, err := os.Stat(destinationPath)
	if err != nil {
		return err
	}
	if !committedInfo.Mode().IsRegular() || committedInfo.Size() != manifest.TotalBytes {
		return ErrUnsafePartialDestination
	}
	verified, err := os.Open(destinationPath)
	if err != nil {
		return err
	}
	verifiedDigest := sha256.New()
	verifiedBytes, verifyErr := io.Copy(verifiedDigest, verified)
	closeErr := verified.Close()
	if verifyErr != nil {
		return verifyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if verifiedBytes != manifest.TotalBytes || hex.EncodeToString(verifiedDigest.Sum(nil)) != manifest.SHA256Hex {
		return p2p.ErrManifestMismatch
	}
	committed = true
	if current, err := verifiedRegularPartialInfo(partialPath); err == nil && os.SameFile(expected, current) {
		if err := os.Remove(partialPath); err != nil {
			return err
		}
	}
	return nil
}

func prepareRuntimeResume(partialPath string, transferID string, totalBytes int64, chunkSize int) (p2p.ResumeManifest, bool, os.FileInfo, error) {
	info, err := verifiedRegularPartialInfo(partialPath)
	if errors.Is(err, os.ErrNotExist) {
		return p2p.ResumeManifest{}, false, nil, nil
	}
	if err != nil {
		return p2p.ResumeManifest{}, false, nil, err
	}
	if info.Size() < 0 || info.Size() > totalBytes {
		return p2p.ResumeManifest{}, false, nil, p2p.ErrResumePastTotalBytes
	}
	prefix, err := openVerifiedPartialForRead(partialPath, info)
	if err != nil {
		return p2p.ResumeManifest{}, false, nil, err
	}
	defer prefix.Close()
	digest := sha256.New()
	copied, err := io.Copy(digest, prefix)
	if err != nil {
		return p2p.ResumeManifest{}, false, nil, fmt.Errorf("read partial destination: %w", err)
	}
	if copied != info.Size() {
		return p2p.ResumeManifest{}, false, nil, p2p.ErrUnexpectedOffset
	}
	return p2p.ResumeManifest{TransferID: transferID, NextSequence: sequenceForOffset(info.Size(), runtimeChunkSize(chunkSize)), NextOffset: info.Size(), TotalBytes: totalBytes, SHA256Hex: hex.EncodeToString(digest.Sum(nil))}, true, info, nil
}

func verifiedRegularPartialInfo(partialPath string) (os.FileInfo, error) {
	info, err := os.Lstat(partialPath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, ErrUnsafePartialDestination
	}
	return info, nil
}

func openVerifiedPartialForRead(partialPath string, expected os.FileInfo) (*os.File, error) {
	file, err := os.Open(partialPath)
	if err != nil {
		return nil, fmt.Errorf("open partial destination: %w", err)
	}
	if err := verifyOpenPartial(file, expected); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func openPartialForAppend(partialPath string, resumed bool, expected os.FileInfo) (*os.File, error) {
	if !resumed {
		return os.OpenFile(partialPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	}
	file, err := os.OpenFile(partialPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open partial destination: %w", err)
	}
	if err := verifyOpenPartial(file, expected); err != nil {
		_ = file.Close()
		return nil, err
	}
	actual, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat open partial destination: %w", err)
	}
	if actual.Size() != expected.Size() {
		_ = file.Close()
		return nil, ErrUnsafePartialDestination
	}
	return file, nil
}

func verifyOpenPartial(file *os.File, expected os.FileInfo) error {
	if file == nil || expected == nil {
		return ErrUnsafePartialDestination
	}
	actual, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat open partial destination: %w", err)
	}
	if !actual.Mode().IsRegular() || !os.SameFile(expected, actual) {
		return ErrUnsafePartialDestination
	}
	return nil
}

func sequenceForOffset(offset int64, chunkSize int) uint64 {
	if offset <= 0 {
		return 0
	}
	return uint64((offset + int64(chunkSize) - 1) / int64(chunkSize))
}

func runtimeChunkSize(chunkSize int) int {
	if chunkSize <= 0 {
		return 64 * 1024
	}
	return chunkSize
}

func resumeIfActive(active bool, resume p2p.ResumeManifest) *p2p.ResumeManifest {
	if !active {
		return nil
	}
	return &resume
}

func isRunnableTransferStatus(status JobStatus) bool {
	return status == JobStatusAccepted || status == JobStatusRetryable
}

func isRetryableRuntimeError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	message := err.Error()
	return strings.Contains(message, context.Canceled.Error()) || strings.Contains(message, context.DeadlineExceeded.Error()) || strings.Contains(message, "remote peer closed") || strings.Contains(message, "data channel closed before open") || strings.Contains(message, "read/write on closed pipe") || strings.Contains(message, "non-established state")
}

func markJobRetryableIfMutable(jobs *JobManager, jobID string, reason string) error {
	if jobs == nil || reason == "" {
		return nil
	}
	job, err := jobs.Get(jobID)
	if err != nil {
		return err
	}
	switch job.Status {
	case JobStatusRetryable:
		return nil
	case JobStatusOffered, JobStatusAccepted:
		if job.Status == JobStatusOffered {
			if _, err := jobs.MarkAccepted(jobID); err != nil {
				return err
			}
		}
		if _, err := jobs.MarkConnecting(jobID); err != nil {
			return err
		}
		if _, err := jobs.Interrupt(jobID, reason); err != nil {
			return err
		}
		_, err := jobs.MarkRetryable(jobID)
		return err
	case JobStatusInterrupted:
		_, err = jobs.MarkRetryable(jobID)
		return err
	case JobStatusConnecting, JobStatusTransferring:
		if _, err := jobs.Interrupt(jobID, reason); err != nil {
			return err
		}
		_, err = jobs.MarkRetryable(jobID)
		return err
	default:
		return failJobIfMutable(jobs, jobID, reason)
	}
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
	if !isRunnableTransferStatus(sendJob.Status) || !isRunnableTransferStatus(receiveJob.Status) {
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
