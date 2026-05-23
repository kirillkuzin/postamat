package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type agentdClient struct {
	socketPath string
	httpClient *http.Client
}

const maxRPCContentLength = 1 << 20

type createAgentdJobRequest struct {
	SourcePath    string `json:"source_path"`
	ToAgentID     string `json:"to_agent_id,omitempty"`
	BrowserLink   bool   `json:"browser_link,omitempty"`
	FileName      string `json:"file_name"`
	FileSizeBytes int64  `json:"file_size_bytes"`
}

func runCLI(ctx context.Context, opts Options) error {
	stdin, stdout, stderr := cliStreams(opts)
	args := opts.Args
	if len(args) == 0 {
		printUsage(stderr)
		return errors.New("postamat command is required")
	}

	switch args[0] {
	case "send":
		return runSend(ctx, opts, stdout, args[1:])
	case "share":
		return runShare(ctx, opts, stdout, args[1:])
	case "status":
		return runStatus(ctx, opts, stdout, args[1:])
	case "cancel":
		return runCancel(ctx, opts, stdout, args[1:])
	case "resume":
		return runResume(ctx, opts, stdout, args[1:])
	case "list":
		return newAgentdClient(localSocketPath(opts)).writeRequest(ctx, stdout, http.MethodGet, "/local/v1/transfers", nil)
	case "inbox", "list-inbox":
		return newAgentdClient(localSocketPath(opts)).writeRequest(ctx, stdout, http.MethodGet, "/local/v1/inbox", nil)
	case "agentd":
		return runAgentd(ctx, opts)
	case "server":
		return runServer(ctx, opts)
	case "mcp":
		return runMCP(ctx, opts, stdin, stdout)
	case "help", "--help", "-h":
		printUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown postamat command %q", args[0])
	}
}

func runSend(ctx context.Context, opts Options, stdout io.Writer, args []string) error {
	filePath, toAgent, err := parseSendArgs(args)
	if err != nil {
		return err
	}
	payload, err := createRequestFromFile(filePath)
	if err != nil {
		return err
	}
	payload.ToAgentID = toAgent
	return newAgentdClient(localSocketPath(opts)).writeRequest(ctx, stdout, http.MethodPost, "/local/v1/transfers", payload)
}

func runShare(ctx context.Context, opts Options, stdout io.Writer, args []string) error {
	filePath, err := parseShareArgs(args)
	if err != nil {
		return err
	}
	payload, err := createRequestFromFile(filePath)
	if err != nil {
		return err
	}
	payload.BrowserLink = true
	return newAgentdClient(localSocketPath(opts)).writeRequest(ctx, stdout, http.MethodPost, "/local/v1/transfers", payload)
}

func parseSendArgs(args []string) (string, string, error) {
	var filePath string
	var toAgent string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--to-agent":
			if i+1 >= len(args) || args[i+1] == "" {
				return "", "", errors.New("send requires --to-agent")
			}
			toAgent = args[i+1]
			i++
		default:
			if strings.HasPrefix(args[i], "--to-agent=") {
				toAgent = strings.TrimPrefix(args[i], "--to-agent=")
				continue
			}
			if strings.HasPrefix(args[i], "-") {
				return "", "", fmt.Errorf("unknown send flag %q", args[i])
			}
			if filePath != "" {
				return "", "", errors.New("send requires exactly one file")
			}
			filePath = args[i]
		}
	}
	if filePath == "" {
		return "", "", errors.New("send requires exactly one file")
	}
	if toAgent == "" {
		return "", "", errors.New("send requires --to-agent")
	}
	return filePath, toAgent, nil
}

func parseShareArgs(args []string) (string, error) {
	var filePath string
	var browserLink bool
	for _, arg := range args {
		switch arg {
		case "--browser-link":
			browserLink = true
		default:
			if strings.HasPrefix(arg, "-") {
				return "", fmt.Errorf("unknown share flag %q", arg)
			}
			if filePath != "" {
				return "", errors.New("share requires exactly one file")
			}
			filePath = arg
		}
	}
	if filePath == "" {
		return "", errors.New("share requires exactly one file")
	}
	if !browserLink {
		return "", errors.New("share requires --browser-link")
	}
	return filePath, nil
}

func runStatus(ctx context.Context, opts Options, stdout io.Writer, args []string) error {
	if len(args) != 1 || args[0] == "" {
		return errors.New("status requires transfer/job id")
	}
	return newAgentdClient(localSocketPath(opts)).writeRequest(ctx, stdout, http.MethodGet, "/local/v1/transfers/"+url.PathEscape(args[0]), nil)
}

func runCancel(ctx context.Context, opts Options, stdout io.Writer, args []string) error {
	if len(args) != 1 || args[0] == "" {
		return errors.New("cancel requires transfer/job id")
	}
	return newAgentdClient(localSocketPath(opts)).writeRequest(ctx, stdout, http.MethodPost, "/local/v1/transfers/"+url.PathEscape(args[0])+"/cancel", nil)
}

func runResume(ctx context.Context, opts Options, stdout io.Writer, args []string) error {
	if len(args) != 1 || args[0] == "" {
		return errors.New("resume requires transfer/job id")
	}
	return newAgentdClient(localSocketPath(opts)).writeRequest(ctx, stdout, http.MethodPost, "/local/v1/transfers/"+url.PathEscape(args[0])+"/resume", nil)
}

func createRequestFromFile(path string) (createAgentdJobRequest, error) {
	info, err := os.Stat(path)
	if err != nil {
		return createAgentdJobRequest{}, err
	}
	if info.IsDir() {
		return createAgentdJobRequest{}, fmt.Errorf("%s is a directory", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return createAgentdJobRequest{}, err
	}
	return createAgentdJobRequest{SourcePath: abs, FileName: info.Name(), FileSizeBytes: info.Size()}, nil
}

func newAgentdClient(socketPath string) *agentdClient {
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		dialer := net.Dialer{Timeout: 5 * time.Second}
		return dialer.DialContext(ctx, "unix", socketPath)
	}}
	return &agentdClient{socketPath: socketPath, httpClient: &http.Client{Transport: transport, Timeout: 30 * time.Second}}
}

func (c *agentdClient) writeRequest(ctx context.Context, stdout io.Writer, method string, path string, payload any) error {
	status, data, err := c.do(ctx, method, path, payload)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("agentd %s %s returned %d: %s", method, path, status, strings.TrimSpace(string(data)))
	}
	_, err = stdout.Write(data)
	return err
}

func (c *agentdClient) do(ctx context.Context, method string, path string, payload any) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://agentd.local"+path, body)
	if err != nil {
		return 0, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("agentd socket %s: %w", c.socketPath, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, data, nil
}

func cliStreams(opts Options) (io.Reader, io.Writer, io.Writer) {
	stdin := opts.Stdin
	stdout := opts.Stdout
	stderr := opts.Stderr
	if stdin == nil {
		stdin = os.Stdin
	}
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	return stdin, stdout, stderr
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: postamat send <file> --to-agent <agent_id> | share <file> --browser-link | status <id> | cancel <id> | resume <id> | list | inbox | agentd | server | mcp")
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func runMCP(ctx context.Context, opts Options, stdin io.Reader, stdout io.Writer) error {
	client := newAgentdClient(localSocketPath(opts))
	reader := bufio.NewReader(stdin)
	for {
		message, framed, err := readRPCMessage(reader)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(message)) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(message, &req); err != nil {
			writeRPC(stdout, rpcResponse{JSONRPC: "2.0", ID: nil, Error: &rpcError{Code: -32700, Message: "parse error"}}, framed)
			continue
		}
		if req.ID == nil && (strings.HasPrefix(req.Method, "notifications/") || req.Method == "initialized") {
			continue
		}
		writeRPC(stdout, handleMCPRequest(ctx, client, req), framed)
	}
}

func readRPCMessage(reader *bufio.Reader) ([]byte, bool, error) {
	peek, err := reader.Peek(1)
	if err != nil {
		return nil, false, err
	}
	if peek[0] != 'C' && peek[0] != 'c' {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) && len(line) > 0 {
			return line, false, nil
		}
		return line, false, err
	}

	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, true, err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		name, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return nil, true, fmt.Errorf("invalid RPC header %q", trimmed)
		}
		if strings.EqualFold(name, "Content-Length") {
			length, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || length < 0 {
				return nil, true, fmt.Errorf("invalid Content-Length %q", value)
			}
			if length > maxRPCContentLength {
				return nil, true, fmt.Errorf("Content-Length %d exceeds %d byte limit", length, maxRPCContentLength)
			}
			contentLength = length
		}
	}
	if contentLength < 0 {
		return nil, true, errors.New("missing Content-Length")
	}
	message := make([]byte, contentLength)
	_, err = io.ReadFull(reader, message)
	return message, true, err
}

func handleMCPRequest(ctx context.Context, client *agentdClient, req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{"protocolVersion": "2024-11-05", "serverInfo": map[string]any{"name": "postamat", "version": "0.0.0"}, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}}
	case "tools/list":
		resp.Result = map[string]any{"tools": []map[string]any{
			toolSchema("create", "Create an agent or browser-link transfer through local postamat agentd."),
			toolSchema("status", "Read transfer/job status from local postamat agentd."),
			toolSchema("cancel", "Cancel a transfer/job through local postamat agentd."),
			toolSchema("resume", "Resume a retryable agent-to-agent transfer through local postamat agentd."),
			toolSchema("list", "List local transfer jobs."),
			toolSchema("list_inbox", "List received inbox jobs."),
		}}
	case "tools/call":
		result, err := callMCPTool(ctx, client, req.Params)
		if err != nil {
			resp.Result = map[string]any{"content": []map[string]string{{"type": "text", "text": err.Error()}}, "isError": true}
		} else {
			resp.Result = map[string]any{"content": []map[string]string{{"type": "text", "text": string(result)}}}
		}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found"}
	}
	return resp
}

func toolSchema(name string, description string) map[string]any {
	properties := map[string]any{}
	required := []string{}
	switch name {
	case "create":
		properties = map[string]any{
			"source_path":  map[string]any{"type": "string", "description": "Local file path visible to postamat mcp/agentd."},
			"to_agent_id":  map[string]any{"type": "string", "description": "Target agent id for agent-to-agent transfers."},
			"browser_link": map[string]any{"type": "boolean", "description": "Create a browser-link transfer instead of targeting an agent."},
		}
		required = []string{"source_path"}
	case "status", "cancel", "resume":
		properties = map[string]any{"id": map[string]any{"type": "string", "description": "Transfer id or local job id."}}
		required = []string{"id"}
	}
	return map[string]any{"name": name, "description": description, "inputSchema": map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}}
}

func callMCPTool(ctx context.Context, client *agentdClient, params json.RawMessage) ([]byte, error) {
	var payload struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return nil, err
	}
	switch payload.Name {
	case "create":
		var args struct {
			SourcePath  string `json:"source_path"`
			ToAgentID   string `json:"to_agent_id"`
			BrowserLink bool   `json:"browser_link"`
		}
		if err := json.Unmarshal(payload.Arguments, &args); err != nil {
			return nil, err
		}
		req, err := createRequestFromFile(args.SourcePath)
		if err != nil {
			return nil, err
		}
		req.ToAgentID = args.ToAgentID
		req.BrowserLink = args.BrowserLink
		return client.checkedDo(ctx, http.MethodPost, "/local/v1/transfers", req)
	case "status", "cancel", "resume":
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(payload.Arguments, &args); err != nil {
			return nil, err
		}
		path := "/local/v1/transfers/" + url.PathEscape(args.ID)
		method := http.MethodGet
		if payload.Name == "cancel" {
			method = http.MethodPost
			path += "/cancel"
		} else if payload.Name == "resume" {
			method = http.MethodPost
			path += "/resume"
		}
		return client.checkedDo(ctx, method, path, nil)
	case "list":
		return client.checkedDo(ctx, http.MethodGet, "/local/v1/transfers", nil)
	case "list_inbox":
		return client.checkedDo(ctx, http.MethodGet, "/local/v1/inbox", nil)
	default:
		return nil, fmt.Errorf("unknown tool %q", payload.Name)
	}
}

func (c *agentdClient) checkedDo(ctx context.Context, method string, path string, payload any) ([]byte, error) {
	status, data, err := c.do(ctx, method, path, payload)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("agentd returned %d: %s", status, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func writeRPC(w io.Writer, resp rpcResponse, framed bool) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	if framed {
		_, _ = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(data))
		_, _ = w.Write(data)
		return
	}
	_, _ = w.Write(append(data, '\n'))
}
