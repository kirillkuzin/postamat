package agentd

import (
	"errors"
	"testing"
	"time"
)

func TestJobManagerCreatesSendJobWithQueuedStatus(t *testing.T) {
	now := time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC)
	manager := NewJobManager(func() time.Time { return now })

	job, err := manager.CreateSendJob(CreateSendJobInput{
		SourcePath:    "/tmp/report.pdf",
		ToAgentID:     "agent-b",
		FileName:      "report.pdf",
		FileSizeBytes: 42,
	})
	if err != nil {
		t.Fatalf("CreateSendJob returned error: %v", err)
	}

	if job.ID == "" {
		t.Fatal("expected generated job id")
	}
	if job.Direction != JobDirectionSend {
		t.Fatalf("direction = %q, want %q", job.Direction, JobDirectionSend)
	}
	if job.Status != JobStatusQueued {
		t.Fatalf("status = %q, want %q", job.Status, JobStatusQueued)
	}
	if job.SourcePath != "/tmp/report.pdf" || job.ToAgentID != "agent-b" || job.FileName != "report.pdf" || job.FileSizeBytes != 42 {
		t.Fatalf("job metadata mismatch: %+v", job)
	}
	if !job.CreatedAt.Equal(now) || !job.UpdatedAt.Equal(now) {
		t.Fatalf("timestamps not initialized from clock: %+v", job)
	}

	stored, err := manager.Get(job.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	stored.FileName = "mutated"
	again, err := manager.Get(job.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if again.FileName != "report.pdf" {
		t.Fatalf("manager returned mutable job copy: %+v", again)
	}
}

func TestJobManagerTracksTransferAndProgressLifecycle(t *testing.T) {
	clock := newStepClock(time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC))
	manager := NewJobManager(clock.Now)
	job, err := manager.CreateSendJob(CreateSendJobInput{SourcePath: "/tmp/a.bin", ToAgentID: "agent-b", FileName: "a.bin", FileSizeBytes: 100})
	if err != nil {
		t.Fatalf("CreateSendJob returned error: %v", err)
	}

	if _, err := manager.AttachTransfer(job.ID, "tr_123", "at_ticket"); err != nil {
		t.Fatalf("AttachTransfer returned error: %v", err)
	}
	if _, err := manager.MarkOffered(job.ID); err != nil {
		t.Fatalf("MarkOffered returned error: %v", err)
	}
	if _, err := manager.MarkAccepted(job.ID); err != nil {
		t.Fatalf("MarkAccepted returned error: %v", err)
	}
	if _, err := manager.MarkConnecting(job.ID); err != nil {
		t.Fatalf("MarkConnecting returned error: %v", err)
	}
	if _, err := manager.MarkTransferring(job.ID); err != nil {
		t.Fatalf("MarkTransferring returned error: %v", err)
	}
	updated, err := manager.UpdateProgress(job.ID, 40)
	if err != nil {
		t.Fatalf("UpdateProgress returned error: %v", err)
	}
	if updated.TransferID != "tr_123" || updated.AgentTicket != "at_ticket" {
		t.Fatalf("transfer credentials not attached: %+v", updated)
	}
	if updated.ProgressBytes != 40 {
		t.Fatalf("progress = %d, want 40", updated.ProgressBytes)
	}
	completed, err := manager.Complete(job.ID)
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if completed.Status != JobStatusCompleted || completed.ProgressBytes != 100 || completed.CompletedAt == nil {
		t.Fatalf("completion not reflected: %+v", completed)
	}
	if _, err := manager.Cancel(job.ID); !errors.Is(err, ErrJobTerminal) {
		t.Fatalf("cancel completed job error = %v, want ErrJobTerminal", err)
	}
}

func TestJobManagerRejectsInvalidProgressAndAllowsCancel(t *testing.T) {
	manager := NewJobManager(func() time.Time { return time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC) })
	job, err := manager.CreateReceiveJob(CreateReceiveJobInput{TransferID: "tr_1", FromAgentID: "agent-a", FileName: "payload.bin", FileSizeBytes: 10})
	if err != nil {
		t.Fatalf("CreateReceiveJob returned error: %v", err)
	}
	if job.Direction != JobDirectionReceive || job.Status != JobStatusOffered {
		t.Fatalf("receive job should start offered: %+v", job)
	}
	if _, err := manager.UpdateProgress(job.ID, 11); !errors.Is(err, ErrProgressOutOfRange) {
		t.Fatalf("progress beyond file size error = %v, want ErrProgressOutOfRange", err)
	}
	cancelled, err := manager.Cancel(job.ID)
	if err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
	if cancelled.Status != JobStatusCancelled || cancelled.CancelledAt == nil {
		t.Fatalf("cancel not reflected: %+v", cancelled)
	}
	if _, err := manager.MarkAccepted(job.ID); !errors.Is(err, ErrJobTerminal) {
		t.Fatalf("transition after cancel error = %v, want ErrJobTerminal", err)
	}
}

func TestJobManagerRejectsCompletionBeforeTransferring(t *testing.T) {
	manager := NewJobManager(nil)
	job, err := manager.CreateReceiveJob(CreateReceiveJobInput{TransferID: "tr_1", FromAgentID: "agent-a", FileName: "payload.bin", FileSizeBytes: 10})
	if err != nil {
		t.Fatalf("CreateReceiveJob returned error: %v", err)
	}
	if _, err := manager.Complete(job.ID); !errors.Is(err, ErrInvalidJobStatus) {
		t.Fatalf("Complete offered job error = %v, want ErrInvalidJobStatus", err)
	}
	after, err := manager.Get(job.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if after.Status != JobStatusOffered || after.CompletedAt != nil {
		t.Fatalf("invalid completion mutated job: %+v", after)
	}
}

func TestJobManagerListsJobsAndFindsByTransferID(t *testing.T) {
	manager := NewJobManager(func() time.Time { return time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC) })
	send, err := manager.CreateSendJob(CreateSendJobInput{SourcePath: "/tmp/a", ToAgentID: "agent-b", FileName: "a", FileSizeBytes: 1})
	if err != nil {
		t.Fatalf("CreateSendJob returned error: %v", err)
	}
	receive, err := manager.CreateReceiveJob(CreateReceiveJobInput{TransferID: "tr_2", FromAgentID: "agent-a", FileName: "b", FileSizeBytes: 2})
	if err != nil {
		t.Fatalf("CreateReceiveJob returned error: %v", err)
	}
	if _, err := manager.AttachTransfer(send.ID, "tr_1", "ticket"); err != nil {
		t.Fatalf("AttachTransfer returned error: %v", err)
	}
	jobs := manager.List()
	if len(jobs) != 2 || jobs[0].ID != send.ID || jobs[1].ID != receive.ID {
		t.Fatalf("jobs not listed in creation order: %+v", jobs)
	}
	found, ok := manager.FindByTransferID("tr_2")
	if !ok || found.ID != receive.ID {
		t.Fatalf("FindByTransferID returned (%+v, %v), want receive job", found, ok)
	}
	if found, ok := manager.FindByTransferID(""); ok {
		t.Fatalf("empty transfer id matched job: %+v", found)
	}
}

func TestJobManagerRejectsDuplicateTransferIDAcrossJobs(t *testing.T) {
	manager := NewJobManager(nil)
	first, err := manager.CreateSendJob(CreateSendJobInput{SourcePath: "/tmp/a", ToAgentID: "agent-b", FileName: "a", FileSizeBytes: 1})
	if err != nil {
		t.Fatalf("CreateSendJob returned error: %v", err)
	}
	second, err := manager.CreateSendJob(CreateSendJobInput{SourcePath: "/tmp/b", ToAgentID: "agent-c", FileName: "b", FileSizeBytes: 1})
	if err != nil {
		t.Fatalf("CreateSendJob returned error: %v", err)
	}
	if _, err := manager.AttachTransfer(first.ID, "tr_same", "ticket-a"); err != nil {
		t.Fatalf("AttachTransfer returned error: %v", err)
	}
	if _, err := manager.AttachTransfer(second.ID, "tr_same", "ticket-b"); !errors.Is(err, ErrDuplicateTransferID) {
		t.Fatalf("duplicate transfer id error = %v, want ErrDuplicateTransferID", err)
	}
}

func TestJobManagerValidation(t *testing.T) {
	manager := NewJobManager(nil)
	if _, err := manager.CreateSendJob(CreateSendJobInput{ToAgentID: "agent-b", FileName: "a", FileSizeBytes: 1}); !errors.Is(err, ErrSourcePathRequired) {
		t.Fatalf("missing source path error = %v", err)
	}
	if _, err := manager.CreateSendJob(CreateSendJobInput{SourcePath: "/tmp/a", FileName: "a", FileSizeBytes: 1}); !errors.Is(err, ErrTargetAgentRequired) {
		t.Fatalf("missing target error = %v", err)
	}
	if _, err := manager.CreateReceiveJob(CreateReceiveJobInput{TransferID: "tr", FromAgentID: "agent-a", FileSizeBytes: 1}); !errors.Is(err, ErrFileNameRequired) {
		t.Fatalf("missing file name error = %v", err)
	}
	if _, err := manager.CreateReceiveJob(CreateReceiveJobInput{TransferID: "tr", FromAgentID: "agent-a", FileName: "a", FileSizeBytes: -1}); !errors.Is(err, ErrFileSizeNegative) {
		t.Fatalf("negative file size error = %v", err)
	}
}

type stepClock struct {
	now time.Time
}

func newStepClock(start time.Time) *stepClock {
	return &stepClock{now: start}
}

func (c *stepClock) Now() time.Time {
	current := c.now
	c.now = c.now.Add(time.Second)
	return current
}
