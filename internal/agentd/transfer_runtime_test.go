package agentd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kirillkuzin/postamat/internal/p2p"
)

func TestLocalWebRTCTransferWritesVerifiedInboxFileAndCompletesJobs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload := []byte("encrypted daemon-to-daemon payload")
	sourcePath := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(sourcePath, payload, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	sendJobs := NewJobManager(nil)
	receiveJobs := NewJobManager(nil)
	inbox := NewInbox(t.TempDir(), nil)
	inboxEntry, err := inbox.Reserve(InboxOffer{TransferID: "tr_mvp", FromAgentID: "agent-a", FileName: "payload.txt", FileSizeBytes: int64(len(payload))})
	if err != nil {
		t.Fatalf("Reserve inbox: %v", err)
	}

	sendJob, err := sendJobs.CreateSendJob(CreateSendJobInput{SourcePath: sourcePath, ToAgentID: "agent-b", FileName: "payload.txt", FileSizeBytes: int64(len(payload))})
	if err != nil {
		t.Fatalf("CreateSendJob: %v", err)
	}
	sendJob, err = sendJobs.AttachTransfer(sendJob.ID, "tr_mvp", "ticket-a")
	if err != nil {
		t.Fatalf("AttachTransfer: %v", err)
	}
	if sendJob, err = sendJobs.MarkOffered(sendJob.ID); err != nil {
		t.Fatalf("MarkOffered: %v", err)
	}
	if sendJob, err = sendJobs.MarkAccepted(sendJob.ID); err != nil {
		t.Fatalf("MarkAccepted send: %v", err)
	}

	receiveJob, err := receiveJobs.CreateReceiveJob(CreateReceiveJobInput{TransferID: "tr_mvp", FromAgentID: "agent-a", FileName: "payload.txt", FileSizeBytes: int64(len(payload)), DestinationPath: inboxEntry.DestinationPath})
	if err != nil {
		t.Fatalf("CreateReceiveJob: %v", err)
	}
	if receiveJob, err = receiveJobs.MarkAccepted(receiveJob.ID); err != nil {
		t.Fatalf("MarkAccepted receive: %v", err)
	}

	key, err := p2p.NewRandomTransferKey()
	if err != nil {
		t.Fatalf("NewRandomTransferKey: %v", err)
	}
	runner := NewLocalWebRTCTransferRunner(sendJobs, receiveJobs, LocalWebRTCTransferOptions{TransferKey: key, ChunkSize: 7})

	manifest, err := runner.Run(ctx, sendJob.ID, receiveJob.ID)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if manifest.TransferID != "tr_mvp" || manifest.TotalBytes != int64(len(payload)) {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}

	written, err := os.ReadFile(inboxEntry.DestinationPath)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(written) != string(payload) {
		t.Fatalf("destination payload = %q", string(written))
	}
	if _, err := os.Stat(inboxEntry.DestinationPath + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary partial file should be removed after verified rename, stat err=%v", err)
	}
	if _, err := os.Stat(inboxEntry.MetadataPath); err != nil {
		t.Fatalf("metadata should remain next to verified file: %v", err)
	}

	finalSend, err := sendJobs.Get(sendJob.ID)
	if err != nil {
		t.Fatalf("Get send: %v", err)
	}
	finalReceive, err := receiveJobs.Get(receiveJob.ID)
	if err != nil {
		t.Fatalf("Get receive: %v", err)
	}
	if finalSend.Status != JobStatusCompleted || finalSend.ProgressBytes != int64(len(payload)) {
		t.Fatalf("send job not completed with full progress: %+v", finalSend)
	}
	if finalReceive.Status != JobStatusCompleted || finalReceive.ProgressBytes != int64(len(payload)) {
		t.Fatalf("receive job not completed with full progress: %+v", finalReceive)
	}
}

func TestLocalWebRTCTransferFailsBothJobsWhenDestinationAlreadyExists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sourcePath := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(sourcePath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	destinationPath := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(destinationPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("write destination: %v", err)
	}

	sendJobs := NewJobManager(nil)
	receiveJobs := NewJobManager(nil)
	sendJob := acceptedSendJobForTest(t, sendJobs, sourcePath, "tr_collision", int64(len("payload")))
	receiveJob := acceptedReceiveJobForTest(t, receiveJobs, destinationPath, "tr_collision", int64(len("payload")))
	key, err := p2p.NewRandomTransferKey()
	if err != nil {
		t.Fatalf("NewRandomTransferKey: %v", err)
	}

	runner := NewLocalWebRTCTransferRunner(sendJobs, receiveJobs, LocalWebRTCTransferOptions{TransferKey: key})
	_, err = runner.Run(ctx, sendJob.ID, receiveJob.ID)
	if err == nil {
		t.Fatal("Run succeeded with pre-existing destination file")
	}

	finalSend, err := sendJobs.Get(sendJob.ID)
	if err != nil {
		t.Fatalf("Get send: %v", err)
	}
	finalReceive, err := receiveJobs.Get(receiveJob.ID)
	if err != nil {
		t.Fatalf("Get receive: %v", err)
	}
	if finalSend.Status != JobStatusFailed || finalReceive.Status != JobStatusFailed {
		t.Fatalf("jobs should fail together, send=%+v receive=%+v", finalSend, finalReceive)
	}
	written, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(written) != "existing" {
		t.Fatalf("existing destination was overwritten: %q", string(written))
	}
}

func TestLocalWebRTCTransferRejectsSourceLargerThanDeclaredSize(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sourcePath := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(sourcePath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	destinationPath := filepath.Join(t.TempDir(), "payload.txt")
	sendJobs := NewJobManager(nil)
	receiveJobs := NewJobManager(nil)
	sendJob := acceptedSendJobForTest(t, sendJobs, sourcePath, "tr_oversize", 1)
	receiveJob := acceptedReceiveJobForTest(t, receiveJobs, destinationPath, "tr_oversize", 1)
	key, err := p2p.NewRandomTransferKey()
	if err != nil {
		t.Fatalf("NewRandomTransferKey: %v", err)
	}

	runner := NewLocalWebRTCTransferRunner(sendJobs, receiveJobs, LocalWebRTCTransferOptions{TransferKey: key})
	_, err = runner.Run(ctx, sendJob.ID, receiveJob.ID)
	if err == nil {
		t.Fatal("Run succeeded with source larger than declared transfer size")
	}
	if _, err := os.Stat(destinationPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination should not be committed after size mismatch, stat err=%v", err)
	}
	finalSend, err := sendJobs.Get(sendJob.ID)
	if err != nil {
		t.Fatalf("Get send: %v", err)
	}
	finalReceive, err := receiveJobs.Get(receiveJob.ID)
	if err != nil {
		t.Fatalf("Get receive: %v", err)
	}
	if finalSend.Status != JobStatusFailed || finalReceive.Status != JobStatusFailed {
		t.Fatalf("jobs should fail together, send=%+v receive=%+v", finalSend, finalReceive)
	}
}

func TestLocalWebRTCTransferPreservesPartialAndMarksRetryableWhenInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	payload := bytes.Repeat([]byte("0123456789abcdef"), 64*1024)
	sourcePath := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(sourcePath, payload, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	destinationPath := filepath.Join(t.TempDir(), "payload.bin")
	sendJobs := NewJobManager(nil)
	receiveJobs := NewJobManager(nil)
	sendJob := acceptedSendJobForTest(t, sendJobs, sourcePath, "tr_interrupt", int64(len(payload)))
	receiveJob := acceptedReceiveJobForTest(t, receiveJobs, destinationPath, "tr_interrupt", int64(len(payload)))
	key, err := p2p.NewRandomTransferKey()
	if err != nil {
		t.Fatalf("NewRandomTransferKey: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		partialPath := destinationPath + ".part"
		for {
			info, err := os.Stat(partialPath)
			if err == nil && info.Size() > 0 {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()

	runner := NewLocalWebRTCTransferRunner(sendJobs, receiveJobs, LocalWebRTCTransferOptions{TransferKey: key, ChunkSize: 1024})
	_, err = runner.Run(ctx, sendJob.ID, receiveJob.ID)
	<-done
	if err == nil {
		t.Fatal("Run succeeded after interrupted context")
	}
	partial, statErr := os.Stat(destinationPath + ".part")
	if statErr != nil {
		t.Fatalf("partial file should survive retryable interruption: %v", statErr)
	}
	if partial.Size() <= 0 || partial.Size() >= int64(len(payload)) {
		t.Fatalf("partial size = %d, want partial prefix smaller than payload", partial.Size())
	}
	if _, err := os.Stat(destinationPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination should not be committed after interruption, stat err=%v", err)
	}
	finalSend, err := sendJobs.Get(sendJob.ID)
	if err != nil {
		t.Fatalf("Get send: %v", err)
	}
	finalReceive, err := receiveJobs.Get(receiveJob.ID)
	if err != nil {
		t.Fatalf("Get receive: %v", err)
	}
	if finalSend.Status != JobStatusRetryable || finalReceive.Status != JobStatusRetryable {
		t.Fatalf("interrupted jobs should be retryable, send=%+v receive=%+v", finalSend, finalReceive)
	}

	resumeCtx, resumeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer resumeCancel()
	manifest, err := runner.Run(resumeCtx, sendJob.ID, receiveJob.ID)
	if err != nil {
		t.Fatalf("retryable Run returned error: %v", err)
	}
	if manifest.TotalBytes != int64(len(payload)) {
		t.Fatalf("resumed manifest = %+v", manifest)
	}
	written, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatalf("read resumed destination: %v", err)
	}
	if string(written) != string(payload) {
		t.Fatalf("resumed payload mismatch")
	}
	if _, err := os.Stat(destinationPath + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial should be removed after retryable completion, stat err=%v", err)
	}
}

func TestLocalWebRTCTransferResumesRetryablePartialFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload := []byte("chunk-0000|chunk-0001|chunk-0002|chunk-0003")
	sourcePath := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(sourcePath, payload, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	destinationPath := filepath.Join(t.TempDir(), "payload.txt")
	chunkSize := 11
	if err := os.WriteFile(destinationPath+".part", payload[:chunkSize], 0o600); err != nil {
		t.Fatalf("write existing partial: %v", err)
	}

	sendJobs := NewJobManager(nil)
	receiveJobs := NewJobManager(nil)
	sendJob := retryableSendJobForTest(t, sendJobs, sourcePath, "tr_resume", int64(len(payload)), int64(chunkSize))
	receiveJob := retryableReceiveJobForTest(t, receiveJobs, destinationPath, "tr_resume", int64(len(payload)), int64(chunkSize))
	key, err := p2p.NewRandomTransferKey()
	if err != nil {
		t.Fatalf("NewRandomTransferKey: %v", err)
	}

	runner := NewLocalWebRTCTransferRunner(sendJobs, receiveJobs, LocalWebRTCTransferOptions{TransferKey: key, ChunkSize: chunkSize})
	manifest, err := runner.Run(ctx, sendJob.ID, receiveJob.ID)
	if err != nil {
		t.Fatalf("Run retryable resume returned error: %v", err)
	}
	if manifest.TotalBytes != int64(len(payload)) {
		t.Fatalf("manifest = %+v", manifest)
	}
	written, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(written) != string(payload) {
		t.Fatalf("resumed destination payload = %q, want %q", string(written), string(payload))
	}
	if _, err := os.Stat(destinationPath + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial should be removed after resumed commit, stat err=%v", err)
	}
}

func TestLocalWebRTCTransferRejectsSymlinkPartialFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload := []byte("payload")
	sourcePath := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(sourcePath, payload, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	destinationPath := filepath.Join(t.TempDir(), "payload.txt")
	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	outsideBefore := []byte("outside must not be appended")
	if err := os.WriteFile(outsidePath, outsideBefore, 0o600); err != nil {
		t.Fatalf("write outside target: %v", err)
	}
	if err := os.Symlink(outsidePath, destinationPath+".part"); err != nil {
		t.Fatalf("create symlink partial: %v", err)
	}

	sendJobs := NewJobManager(nil)
	receiveJobs := NewJobManager(nil)
	sendJob := retryableSendJobForTest(t, sendJobs, sourcePath, "tr_symlink", int64(len(payload)), 1)
	receiveJob := retryableReceiveJobForTest(t, receiveJobs, destinationPath, "tr_symlink", int64(len(payload)), 1)
	key, err := p2p.NewRandomTransferKey()
	if err != nil {
		t.Fatalf("NewRandomTransferKey: %v", err)
	}

	runner := NewLocalWebRTCTransferRunner(sendJobs, receiveJobs, LocalWebRTCTransferOptions{TransferKey: key, ChunkSize: 4})
	_, err = runner.Run(ctx, sendJob.ID, receiveJob.ID)
	if !errors.Is(err, ErrUnsafePartialDestination) {
		t.Fatalf("Run error = %v, want ErrUnsafePartialDestination", err)
	}
	outsideAfter, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("read outside target: %v", err)
	}
	if string(outsideAfter) != string(outsideBefore) {
		t.Fatalf("symlink target was modified: %q", string(outsideAfter))
	}
	if _, err := os.Lstat(destinationPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination should not be committed from symlink partial, lstat err=%v", err)
	}
}

func TestCommitDestinationReverifiesPartialBytesAgainstManifest(t *testing.T) {
	dir := t.TempDir()
	partialPath := filepath.Join(dir, "payload.txt.part")
	destinationPath := filepath.Join(dir, "payload.txt")
	if err := os.WriteFile(partialPath, []byte("corrupt"), 0o600); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	info, err := verifiedRegularPartialInfo(partialPath)
	if err != nil {
		t.Fatalf("verifiedRegularPartialInfo: %v", err)
	}
	manifest := p2p.Manifest{TransferID: "tr_commit", TotalBytes: int64(len("correct")), ChunkCount: 1, SHA256Hex: sha256HexForTest([]byte("correct"))}

	err = commitDestinationNoReplace(partialPath, destinationPath, info, manifest)
	if !errors.Is(err, p2p.ErrManifestMismatch) {
		t.Fatalf("commitDestinationNoReplace error = %v, want ErrManifestMismatch", err)
	}
	if _, err := os.Lstat(destinationPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination should be removed after manifest mismatch, lstat err=%v", err)
	}
}

func acceptedSendJobForTest(t *testing.T, jobs *JobManager, sourcePath string, transferID string, size int64) Job {
	t.Helper()
	job, err := jobs.CreateSendJob(CreateSendJobInput{SourcePath: sourcePath, ToAgentID: "agent-b", FileName: filepath.Base(sourcePath), FileSizeBytes: size})
	if err != nil {
		t.Fatalf("CreateSendJob: %v", err)
	}
	job, err = jobs.AttachTransfer(job.ID, transferID, "ticket-a")
	if err != nil {
		t.Fatalf("AttachTransfer: %v", err)
	}
	job, err = jobs.MarkOffered(job.ID)
	if err != nil {
		t.Fatalf("MarkOffered: %v", err)
	}
	job, err = jobs.MarkAccepted(job.ID)
	if err != nil {
		t.Fatalf("MarkAccepted: %v", err)
	}
	return job
}

func acceptedReceiveJobForTest(t *testing.T, jobs *JobManager, destinationPath string, transferID string, size int64) Job {
	t.Helper()
	job, err := jobs.CreateReceiveJob(CreateReceiveJobInput{TransferID: transferID, FromAgentID: "agent-a", FileName: filepath.Base(destinationPath), FileSizeBytes: size, DestinationPath: destinationPath})
	if err != nil {
		t.Fatalf("CreateReceiveJob: %v", err)
	}
	job, err = jobs.MarkAccepted(job.ID)
	if err != nil {
		t.Fatalf("MarkAccepted: %v", err)
	}
	return job
}

func retryableSendJobForTest(t *testing.T, jobs *JobManager, sourcePath string, transferID string, size int64, progress int64) Job {
	t.Helper()
	job := acceptedSendJobForTest(t, jobs, sourcePath, transferID, size)
	job = markRetryableJobForTest(t, jobs, job.ID, progress)
	return job
}

func retryableReceiveJobForTest(t *testing.T, jobs *JobManager, destinationPath string, transferID string, size int64, progress int64) Job {
	t.Helper()
	job := acceptedReceiveJobForTest(t, jobs, destinationPath, transferID, size)
	job = markRetryableJobForTest(t, jobs, job.ID, progress)
	return job
}

func markRetryableJobForTest(t *testing.T, jobs *JobManager, jobID string, progress int64) Job {
	t.Helper()
	job, err := jobs.MarkConnecting(jobID)
	if err != nil {
		t.Fatalf("MarkConnecting: %v", err)
	}
	job, err = jobs.MarkTransferring(job.ID)
	if err != nil {
		t.Fatalf("MarkTransferring: %v", err)
	}
	job, err = jobs.UpdateProgress(job.ID, progress)
	if err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
	job, err = jobs.Interrupt(job.ID, "test interruption")
	if err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	job, err = jobs.MarkRetryable(job.ID)
	if err != nil {
		t.Fatalf("MarkRetryable: %v", err)
	}
	return job
}

func sha256HexForTest(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
