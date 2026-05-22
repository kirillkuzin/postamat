package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

type LocalRouter struct {
	ctx     context.Context
	jobs    *JobManager
	backend *BackendLoop
}

type createLocalTransferRequest struct {
	SourcePath    string `json:"source_path"`
	ToAgentID     string `json:"to_agent_id"`
	BrowserLink   bool   `json:"browser_link"`
	FileName      string `json:"file_name"`
	FileSizeBytes int64  `json:"file_size_bytes"`
}

type jobResponse struct {
	ID              string `json:"id"`
	Direction       string `json:"direction"`
	Status          string `json:"status"`
	TransferID      string `json:"transfer_id,omitempty"`
	PublicToken     string `json:"public_token,omitempty"`
	BrowserURL      string `json:"browser_url,omitempty"`
	SourcePath      string `json:"source_path,omitempty"`
	DestinationPath string `json:"destination_path,omitempty"`
	FromAgentID     string `json:"from_agent_id,omitempty"`
	ToAgentID       string `json:"to_agent_id,omitempty"`
	BrowserLink     bool   `json:"browser_link,omitempty"`
	FileName        string `json:"file_name"`
	FileSizeBytes   int64  `json:"file_size_bytes"`
	ProgressBytes   int64  `json:"progress_bytes"`
	FailureReason   string `json:"failure_reason,omitempty"`
}

func NewLocalRouter(jobs *JobManager) http.Handler {
	return NewLocalRouterWithBackendContext(context.Background(), jobs, nil)
}

func NewLocalRouterWithBackend(jobs *JobManager, backend *BackendLoop) http.Handler {
	return NewLocalRouterWithBackendContext(context.Background(), jobs, backend)
}

func NewLocalRouterWithBackendContext(ctx context.Context, jobs *JobManager, backend *BackendLoop) http.Handler {
	if ctx == nil {
		ctx = context.Background()
	}
	if jobs == nil {
		if backend != nil && backend.jobs != nil {
			jobs = backend.jobs
		} else {
			jobs = NewJobManager(nil)
		}
	}
	return &LocalRouter{ctx: ctx, jobs: jobs, backend: backend}
}

func (r *LocalRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	path := req.URL.EscapedPath()
	switch {
	case path == "/local/v1/transfers":
		r.handleTransfers(w, req)
	case strings.HasPrefix(path, "/local/v1/transfers/"):
		r.handleTransfer(w, req)
	case path == "/local/v1/inbox":
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
		input := CreateSendJobInput{SourcePath: payload.SourcePath, ToAgentID: payload.ToAgentID, BrowserLink: payload.BrowserLink, FileName: payload.FileName, FileSizeBytes: payload.FileSizeBytes}
		job, err := r.createSendJob(req, input)
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

func (r *LocalRouter) createSendJob(req *http.Request, input CreateSendJobInput) (Job, error) {
	if r.backend != nil {
		job, err := r.backend.CreateSendTransfer(req.Context(), input)
		if err != nil {
			return Job{}, err
		}
		go func() {
			if err := r.backend.RunTransfer(r.ctx, job); err != nil && r.ctx.Err() == nil {
				_, _ = r.jobs.Fail(job.ID, err.Error())
			}
		}()
		return job, nil
	}
	return r.jobs.CreateSendJob(input)
}

func (r *LocalRouter) handleTransfer(w http.ResponseWriter, req *http.Request) {
	parts := strings.Split(strings.TrimPrefix(req.URL.EscapedPath(), "/local/v1/transfers/"), "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
		http.NotFound(w, req)
		return
	}
	jobID, err := url.PathUnescape(parts[0])
	if err != nil || jobID == "" {
		http.NotFound(w, req)
		return
	}
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
	job, err := r.jobByIDOrTransferID(jobID)
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
	job, err := r.jobByIDOrTransferID(jobID)
	if err != nil {
		writeLocalError(w, statusForJobError(err), err.Error())
		return
	}
	if r.backend != nil {
		job, err = r.backend.CancelTransfer(req.Context(), job.ID)
	} else {
		job, err = r.jobs.Cancel(job.ID)
	}
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
	job, err := r.jobByIDOrTransferID(jobID)
	if err != nil {
		writeLocalError(w, statusForJobError(err), err.Error())
		return
	}
	events, err := r.jobs.Events(job.ID)
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

func (r *LocalRouter) jobByIDOrTransferID(id string) (Job, error) {
	job, err := r.jobs.Get(id)
	if err == nil {
		return job, nil
	}
	if !errors.Is(err, ErrJobNotFound) {
		return Job{}, err
	}
	found, ok := r.jobs.FindByTransferID(id)
	if !ok {
		return Job{}, ErrJobNotFound
	}
	return found, nil
}

func newJobResponse(job Job) jobResponse {
	return jobResponse{ID: job.ID, Direction: string(job.Direction), Status: string(job.Status), TransferID: job.TransferID, PublicToken: job.PublicToken, BrowserURL: job.BrowserURL, SourcePath: job.SourcePath, DestinationPath: job.DestinationPath, FromAgentID: job.FromAgentID, ToAgentID: job.ToAgentID, BrowserLink: job.BrowserLink, FileName: job.FileName, FileSizeBytes: job.FileSizeBytes, ProgressBytes: job.ProgressBytes, FailureReason: job.FailureReason}
}

func statusForJobError(err error) int {
	switch {
	case errors.Is(err, ErrJobNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrSourcePathRequired), errors.Is(err, ErrTargetAgentRequired), errors.Is(err, ErrBrowserTargetConflict), errors.Is(err, ErrFromAgentRequired), errors.Is(err, ErrFileNameRequired), errors.Is(err, ErrFileSizeNegative), errors.Is(err, ErrTransferIDRequired), errors.Is(err, ErrDuplicateTransferID), errors.Is(err, ErrProgressOutOfRange), errors.Is(err, ErrJobTerminal), errors.Is(err, ErrInvalidJobStatus), errors.Is(err, ErrFailureReasonRequired):
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
