package agentd

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/kirillkuzin/postamat/internal/p2p"
)

type JobDirection string

type JobStatus string

type JobEventType string

const (
	JobDirectionSend    JobDirection = "send"
	JobDirectionReceive JobDirection = "receive"

	JobStatusQueued       JobStatus = "queued"
	JobStatusOffered      JobStatus = "offered"
	JobStatusAccepted     JobStatus = "accepted"
	JobStatusConnecting   JobStatus = "connecting"
	JobStatusTransferring JobStatus = "transferring"
	JobStatusCompleted    JobStatus = "completed"
	JobStatusFailed       JobStatus = "failed"
	JobStatusCancelled    JobStatus = "cancelled"

	JobEventCreated       JobEventType = "job.created"
	JobEventTransferBound JobEventType = "job.transfer_bound"
	JobEventStatusChanged JobEventType = "job.status_changed"
	JobEventProgress      JobEventType = "job.progress"
)

var (
	ErrJobNotFound           = errors.New("job not found")
	ErrJobTerminal           = errors.New("terminal job cannot transition")
	ErrInvalidJobStatus      = errors.New("invalid job status transition")
	ErrSourcePathRequired    = errors.New("source path is required")
	ErrTargetAgentRequired   = errors.New("target agent id is required")
	ErrBrowserTargetConflict = errors.New("browser-link transfer must not include target agent id")
	ErrFromAgentRequired     = errors.New("from agent id is required")
	ErrFileNameRequired      = errors.New("file name is required")
	ErrFileSizeNegative      = errors.New("file size must be non-negative")
	ErrTransferIDRequired    = errors.New("transfer id is required")
	ErrDuplicateTransferID   = errors.New("transfer id is already attached to another job")
	ErrProgressOutOfRange    = errors.New("progress is outside file size")
	ErrFailureReasonRequired = errors.New("failure reason is required")
)

type Job struct {
	ID              string          `json:"id"`
	Direction       JobDirection    `json:"direction"`
	Status          JobStatus       `json:"status"`
	TransferID      string          `json:"transfer_id,omitempty"`
	AgentTicket     string          `json:"-"`
	TransferKey     p2p.TransferKey `json:"-"`
	HasTransferKey  bool            `json:"-"`
	SourcePath      string          `json:"source_path,omitempty"`
	DestinationPath string          `json:"destination_path,omitempty"`
	FromAgentID     string          `json:"from_agent_id,omitempty"`
	ToAgentID       string          `json:"to_agent_id,omitempty"`
	BrowserLink     bool            `json:"browser_link,omitempty"`
	FileName        string          `json:"file_name"`
	FileSizeBytes   int64           `json:"file_size_bytes"`
	ProgressBytes   int64           `json:"progress_bytes"`
	FailureReason   string          `json:"failure_reason,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
	FailedAt        *time.Time      `json:"failed_at,omitempty"`
	CancelledAt     *time.Time      `json:"cancelled_at,omitempty"`
	sequence        int64
}

type JobEvent struct {
	Type          JobEventType `json:"type"`
	JobID         string       `json:"job_id"`
	TransferID    string       `json:"transfer_id,omitempty"`
	Status        JobStatus    `json:"status,omitempty"`
	ProgressBytes int64        `json:"progress_bytes,omitempty"`
	Message       string       `json:"message,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	sequence      int64
}

type CreateSendJobInput struct {
	SourcePath    string
	ToAgentID     string
	BrowserLink   bool
	FileName      string
	FileSizeBytes int64
}

type CreateReceiveJobInput struct {
	TransferID      string
	FromAgentID     string
	FileName        string
	FileSizeBytes   int64
	DestinationPath string
}

type JobManager struct {
	mu       sync.RWMutex
	now      func() time.Time
	jobs     map[string]Job
	events   map[string][]JobEvent
	sequence int64
}

func NewJobManager(now func() time.Time) *JobManager {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &JobManager{now: now, jobs: make(map[string]Job), events: make(map[string][]JobEvent)}
}

func (m *JobManager) CreateSendJob(input CreateSendJobInput) (Job, error) {
	if input.SourcePath == "" {
		return Job{}, ErrSourcePathRequired
	}
	if input.BrowserLink && input.ToAgentID != "" {
		return Job{}, ErrBrowserTargetConflict
	}
	if !input.BrowserLink && input.ToAgentID == "" {
		return Job{}, ErrTargetAgentRequired
	}
	if input.FileName == "" {
		return Job{}, ErrFileNameRequired
	}
	if input.FileSizeBytes < 0 {
		return Job{}, ErrFileSizeNegative
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	job := Job{ID: newJobID(), Direction: JobDirectionSend, Status: JobStatusQueued, SourcePath: input.SourcePath, ToAgentID: input.ToAgentID, BrowserLink: input.BrowserLink, FileName: input.FileName, FileSizeBytes: input.FileSizeBytes, CreatedAt: now, UpdatedAt: now, sequence: m.nextSequence()}
	m.jobs[job.ID] = cloneJob(job)
	m.appendEventLocked(job, JobEvent{Type: JobEventCreated, Status: job.Status})
	return cloneJob(job), nil
}

func (m *JobManager) CreateReceiveJob(input CreateReceiveJobInput) (Job, error) {
	if input.TransferID == "" {
		return Job{}, ErrTransferIDRequired
	}
	if input.FromAgentID == "" {
		return Job{}, ErrFromAgentRequired
	}
	if input.FileName == "" {
		return Job{}, ErrFileNameRequired
	}
	if input.FileSizeBytes < 0 {
		return Job{}, ErrFileSizeNegative
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.transferIDExistsLocked(input.TransferID, "") {
		return Job{}, ErrDuplicateTransferID
	}
	now := m.now()
	job := Job{ID: newJobID(), Direction: JobDirectionReceive, Status: JobStatusOffered, TransferID: input.TransferID, FromAgentID: input.FromAgentID, FileName: input.FileName, FileSizeBytes: input.FileSizeBytes, DestinationPath: input.DestinationPath, CreatedAt: now, UpdatedAt: now, sequence: m.nextSequence()}
	m.jobs[job.ID] = cloneJob(job)
	m.appendEventLocked(job, JobEvent{Type: JobEventCreated, Status: job.Status})
	return cloneJob(job), nil
}

func (m *JobManager) AttachTransfer(jobID string, transferID string, agentTicket string) (Job, error) {
	if transferID == "" {
		return Job{}, ErrTransferIDRequired
	}
	return m.update(jobID, func(job *Job, now time.Time) error {
		if job.isTerminal() {
			return ErrJobTerminal
		}
		if m.transferIDExistsLocked(transferID, job.ID) {
			return ErrDuplicateTransferID
		}
		job.TransferID = transferID
		job.AgentTicket = agentTicket
		job.UpdatedAt = now
		return nil
	}, JobEvent{Type: JobEventTransferBound, TransferID: transferID})
}

func (m *JobManager) AttachTransferKey(jobID string, key p2p.TransferKey) (Job, error) {
	if key == (p2p.TransferKey{}) {
		return Job{}, ErrTransferKeyRequired
	}
	return m.update(jobID, func(job *Job, now time.Time) error {
		if job.isTerminal() {
			return ErrJobTerminal
		}
		job.TransferKey = key
		job.HasTransferKey = true
		job.UpdatedAt = now
		return nil
	}, JobEvent{Type: JobEventTransferBound})
}

func (m *JobManager) MarkOffered(jobID string) (Job, error) {
	return m.transition(jobID, JobStatusQueued, JobStatusOffered)
}

func (m *JobManager) MarkAccepted(jobID string) (Job, error) {
	return m.transition(jobID, JobStatusOffered, JobStatusAccepted)
}

func (m *JobManager) MarkConnecting(jobID string) (Job, error) {
	return m.transition(jobID, JobStatusAccepted, JobStatusConnecting)
}

func (m *JobManager) MarkTransferring(jobID string) (Job, error) {
	return m.transition(jobID, JobStatusConnecting, JobStatusTransferring)
}

func (m *JobManager) UpdateProgress(jobID string, progressBytes int64) (Job, error) {
	return m.update(jobID, func(job *Job, now time.Time) error {
		if job.isTerminal() {
			return ErrJobTerminal
		}
		if progressBytes < 0 || progressBytes > job.FileSizeBytes {
			return ErrProgressOutOfRange
		}
		job.ProgressBytes = progressBytes
		job.UpdatedAt = now
		return nil
	}, JobEvent{Type: JobEventProgress, ProgressBytes: progressBytes})
}

func (m *JobManager) Complete(jobID string) (Job, error) {
	return m.update(jobID, func(job *Job, now time.Time) error {
		if job.isTerminal() {
			return ErrJobTerminal
		}
		if job.Status != JobStatusTransferring {
			return ErrInvalidJobStatus
		}
		job.Status = JobStatusCompleted
		job.ProgressBytes = job.FileSizeBytes
		job.CompletedAt = cloneTime(now)
		job.UpdatedAt = now
		return nil
	}, JobEvent{Type: JobEventStatusChanged, Status: JobStatusCompleted})
}

func (m *JobManager) Fail(jobID string, reason string) (Job, error) {
	if reason == "" {
		return Job{}, ErrFailureReasonRequired
	}
	return m.update(jobID, func(job *Job, now time.Time) error {
		if job.isTerminal() {
			return ErrJobTerminal
		}
		job.Status = JobStatusFailed
		job.FailureReason = reason
		job.FailedAt = cloneTime(now)
		job.UpdatedAt = now
		return nil
	}, JobEvent{Type: JobEventStatusChanged, Status: JobStatusFailed, Message: reason})
}

func (m *JobManager) Cancel(jobID string) (Job, error) {
	return m.update(jobID, func(job *Job, now time.Time) error {
		if job.isTerminal() {
			return ErrJobTerminal
		}
		job.Status = JobStatusCancelled
		job.CancelledAt = cloneTime(now)
		job.UpdatedAt = now
		return nil
	}, JobEvent{Type: JobEventStatusChanged, Status: JobStatusCancelled})
}

func (m *JobManager) Get(jobID string) (Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, ok := m.jobs[jobID]
	if !ok {
		return Job{}, ErrJobNotFound
	}
	return cloneJob(job), nil
}

func (m *JobManager) List() []Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	jobs := make([]Job, 0, len(m.jobs))
	for _, job := range m.jobs {
		jobs = append(jobs, cloneJob(job))
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].sequence < jobs[j].sequence })
	return jobs
}

func (m *JobManager) Inbox() []Job {
	jobs := m.List()
	inbox := jobs[:0]
	for _, job := range jobs {
		if job.Direction == JobDirectionReceive {
			inbox = append(inbox, job)
		}
	}
	return inbox
}

func (m *JobManager) Events(jobID string) ([]JobEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.jobs[jobID]; !ok {
		return nil, ErrJobNotFound
	}
	events := append([]JobEvent(nil), m.events[jobID]...)
	sort.Slice(events, func(i, j int) bool { return events[i].sequence < events[j].sequence })
	return events, nil
}

func (m *JobManager) FindByTransferID(transferID string) (Job, bool) {
	if transferID == "" {
		return Job{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, job := range m.jobs {
		if job.TransferID == transferID {
			return cloneJob(job), true
		}
	}
	return Job{}, false
}

func (m *JobManager) transition(jobID string, from JobStatus, to JobStatus) (Job, error) {
	return m.update(jobID, func(job *Job, now time.Time) error {
		if job.isTerminal() {
			return ErrJobTerminal
		}
		if job.Status != from {
			return ErrInvalidJobStatus
		}
		job.Status = to
		job.UpdatedAt = now
		return nil
	}, JobEvent{Type: JobEventStatusChanged, Status: to})
}

func (m *JobManager) update(jobID string, mutate func(*Job, time.Time) error, event JobEvent) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[jobID]
	if !ok {
		return Job{}, ErrJobNotFound
	}
	now := m.now()
	if err := mutate(&job, now); err != nil {
		return Job{}, err
	}
	m.jobs[jobID] = cloneJob(job)
	m.appendEventLocked(job, event)
	return cloneJob(job), nil
}

func (m *JobManager) transferIDExistsLocked(transferID string, exceptJobID string) bool {
	for _, job := range m.jobs {
		if job.ID != exceptJobID && job.TransferID == transferID {
			return true
		}
	}
	return false
}

func (m *JobManager) appendEventLocked(job Job, event JobEvent) {
	if event.JobID == "" {
		event.JobID = job.ID
	}
	if event.TransferID == "" {
		event.TransferID = job.TransferID
	}
	if event.Status == "" {
		event.Status = job.Status
	}
	if event.ProgressBytes == 0 {
		event.ProgressBytes = job.ProgressBytes
	}
	event.CreatedAt = m.now()
	event.sequence = m.nextSequence()
	m.events[job.ID] = append(m.events[job.ID], event)
}

func (m *JobManager) nextSequence() int64 {
	m.sequence++
	return m.sequence
}

func (j Job) isTerminal() bool {
	return j.Status == JobStatusCompleted || j.Status == JobStatusFailed || j.Status == JobStatusCancelled
}

func cloneJob(job Job) Job {
	if job.CompletedAt != nil {
		completedAt := *job.CompletedAt
		job.CompletedAt = &completedAt
	}
	if job.FailedAt != nil {
		failedAt := *job.FailedAt
		job.FailedAt = &failedAt
	}
	if job.CancelledAt != nil {
		cancelledAt := *job.CancelledAt
		job.CancelledAt = &cancelledAt
	}
	return job
}

func cloneTime(at time.Time) *time.Time {
	cloned := at
	return &cloned
}

func newJobID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(err)
	}
	return "job_" + hex.EncodeToString(buf[:])
}
