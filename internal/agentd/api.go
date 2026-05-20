package agentd

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type LocalRouter struct {
	jobs *JobManager
}

type createLocalTransferRequest struct {
	SourcePath    string `json:"source_path"`
	ToAgentID     string `json:"to_agent_id"`
	FileName      string `json:"file_name"`
	FileSizeBytes int64  `json:"file_size_bytes"`
}

type jobResponse struct {
	ID              string `json:"id"`
	Direction       string `json:"direction"`
	Status          string `json:"status"`
	TransferID      string `json:"transfer_id,omitempty"`
	SourcePath      string `json:"source_path,omitempty"`
	DestinationPath string `json:"destination_path,omitempty"`
	FromAgentID     string `json:"from_agent_id,omitempty"`
	ToAgentID       string `json:"to_agent_id,omitempty"`
	FileName        string `json:"file_name"`
	FileSizeBytes   int64  `json:"file_size_bytes"`
	ProgressBytes   int64  `json:"progress_bytes"`
	FailureReason   string `json:"failure_reason,omitempty"`
}

func NewLocalRouter(jobs *JobManager) http.Handler {
	if jobs == nil {
		jobs = NewJobManager(nil)
	}
	return &LocalRouter{jobs: jobs}
}

func (r *LocalRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch {
	case req.URL.Path == "/local/v1/transfers":
		r.handleTransfers(w, req)
	case strings.HasPrefix(req.URL.Path, "/local/v1/transfers/"):
		r.handleTransfer(w, req)
	case req.URL.Path == "/local/v1/inbox":
		r.handleInbox(w, req)
	default:
		http.NotFound(w, req)
	}
}

func (r *LocalRouter) handleTransfers(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodPost:
		var payload createLocalTransferRequest
		decoder := json.NewDecoder(req.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			writeLocalError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		job, err := r.jobs.CreateSendJob(CreateSendJobInput{SourcePath: payload.SourcePath, ToAgentID: payload.ToAgentID, FileName: payload.FileName, FileSizeBytes: payload.FileSizeBytes})
		if err != nil {
			writeLocalError(w, statusForJobError(err), err.Error())
			return
		}
		writeLocalJSON(w, http.StatusCreated, newJobResponse(job))
	case http.MethodGet:
		jobs := r.jobs.List()
		response := struct {
			Jobs []jobResponse `json:"jobs"`
		}{Jobs: make([]jobResponse, 0, len(jobs))}
		for _, job := range jobs {
			response.Jobs = append(response.Jobs, newJobResponse(job))
		}
		writeLocalJSON(w, http.StatusOK, response)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (r *LocalRouter) handleTransfer(w http.ResponseWriter, req *http.Request) {
	parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/local/v1/transfers/"), "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
		http.NotFound(w, req)
		return
	}
	jobID := parts[0]
	if len(parts) == 2 {
		switch parts[1] {
		case "cancel":
			r.handleCancelTransfer(w, req, jobID)
		case "events":
			r.handleTransferEvents(w, req, jobID)
		default:
			http.NotFound(w, req)
		}
		return
	}

	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	job, err := r.jobs.Get(jobID)
	if err != nil {
		writeLocalError(w, statusForJobError(err), err.Error())
		return
	}
	writeLocalJSON(w, http.StatusOK, newJobResponse(job))
}

func (r *LocalRouter) handleCancelTransfer(w http.ResponseWriter, req *http.Request, jobID string) {
	if req.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	job, err := r.jobs.Cancel(jobID)
	if err != nil {
		writeLocalError(w, statusForJobError(err), err.Error())
		return
	}
	writeLocalJSON(w, http.StatusOK, newJobResponse(job))
}

func (r *LocalRouter) handleTransferEvents(w http.ResponseWriter, req *http.Request, jobID string) {
	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	events, err := r.jobs.Events(jobID)
	if err != nil {
		writeLocalError(w, statusForJobError(err), err.Error())
		return
	}
	writeLocalJSON(w, http.StatusOK, struct {
		Events []JobEvent `json:"events"`
	}{Events: events})
}

func (r *LocalRouter) handleInbox(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	jobs := r.jobs.Inbox()
	response := struct {
		Jobs []jobResponse `json:"jobs"`
	}{Jobs: make([]jobResponse, 0, len(jobs))}
	for _, job := range jobs {
		response.Jobs = append(response.Jobs, newJobResponse(job))
	}
	writeLocalJSON(w, http.StatusOK, response)
}

func newJobResponse(job Job) jobResponse {
	return jobResponse{ID: job.ID, Direction: string(job.Direction), Status: string(job.Status), TransferID: job.TransferID, SourcePath: job.SourcePath, DestinationPath: job.DestinationPath, FromAgentID: job.FromAgentID, ToAgentID: job.ToAgentID, FileName: job.FileName, FileSizeBytes: job.FileSizeBytes, ProgressBytes: job.ProgressBytes, FailureReason: job.FailureReason}
}

func statusForJobError(err error) int {
	switch {
	case errors.Is(err, ErrJobNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrSourcePathRequired), errors.Is(err, ErrTargetAgentRequired), errors.Is(err, ErrFromAgentRequired), errors.Is(err, ErrFileNameRequired), errors.Is(err, ErrFileSizeNegative), errors.Is(err, ErrTransferIDRequired), errors.Is(err, ErrDuplicateTransferID), errors.Is(err, ErrProgressOutOfRange), errors.Is(err, ErrJobTerminal), errors.Is(err, ErrInvalidJobStatus), errors.Is(err, ErrFailureReasonRequired):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func writeLocalJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeLocalError(w http.ResponseWriter, status int, message string) {
	writeLocalJSON(w, status, map[string]string{"error": message})
}
