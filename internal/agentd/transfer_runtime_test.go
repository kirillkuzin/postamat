package agentd

import (
	"context"
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
