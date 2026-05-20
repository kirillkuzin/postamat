package agentd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrUnsafeFileName  = errors.New("unsafe file name")
	ErrInboxRootUnsafe = errors.New("inbox root must not be accessible by group or others")
)

type InboxOffer struct {
	TransferID    string
	FromAgentID   string
	FileName      string
	FileSizeBytes int64
}

type InboxEntry struct {
	TransferID      string    `json:"transfer_id"`
	FromAgentID     string    `json:"from_agent_id"`
	FileName        string    `json:"file_name"`
	FileSizeBytes   int64     `json:"file_size_bytes"`
	DestinationPath string    `json:"destination_path"`
	MetadataPath    string    `json:"metadata_path"`
	ReservedAt      time.Time `json:"reserved_at"`
	sequence        int64
}

type InboxMetadata struct {
	TransferID      string    `json:"transfer_id"`
	FromAgentID     string    `json:"from_agent_id"`
	FileName        string    `json:"file_name"`
	FileSizeBytes   int64     `json:"file_size_bytes"`
	DestinationPath string    `json:"destination_path"`
	ReservedAt      time.Time `json:"reserved_at"`
}

type Inbox struct {
	mu       sync.RWMutex
	root     string
	now      func() time.Time
	entries  []InboxEntry
	sequence int64
}

func NewInbox(root string, now func() time.Time) *Inbox {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Inbox{root: root, now: now}
}

func (i *Inbox) Reserve(offer InboxOffer) (InboxEntry, error) {
	if offer.TransferID == "" {
		return InboxEntry{}, ErrTransferIDRequired
	}
	if offer.FromAgentID == "" {
		return InboxEntry{}, ErrFromAgentRequired
	}
	if offer.FileName == "" {
		return InboxEntry{}, ErrFileNameRequired
	}
	if offer.FileSizeBytes < 0 {
		return InboxEntry{}, ErrFileSizeNegative
	}
	cleanName, err := safeBaseName(offer.FileName)
	if err != nil {
		return InboxEntry{}, err
	}
	if err := i.prepareRoot(); err != nil {
		return InboxEntry{}, err
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	destination := i.uniqueDestination(cleanName)
	i.sequence++
	entry := InboxEntry{TransferID: offer.TransferID, FromAgentID: offer.FromAgentID, FileName: cleanName, FileSizeBytes: offer.FileSizeBytes, DestinationPath: destination, MetadataPath: destination + ".postamat.json", ReservedAt: i.now(), sequence: i.sequence}
	metadata := InboxMetadata{TransferID: entry.TransferID, FromAgentID: entry.FromAgentID, FileName: entry.FileName, FileSizeBytes: entry.FileSizeBytes, DestinationPath: entry.DestinationPath, ReservedAt: entry.ReservedAt}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return InboxEntry{}, err
	}
	metadataFile, err := os.OpenFile(entry.MetadataPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return InboxEntry{}, err
	}
	if _, err := metadataFile.Write(append(data, '\n')); err != nil {
		_ = metadataFile.Close()
		return InboxEntry{}, err
	}
	if err := metadataFile.Close(); err != nil {
		return InboxEntry{}, err
	}
	i.entries = append(i.entries, entry)
	return cloneInboxEntry(entry), nil
}

func (i *Inbox) List() []InboxEntry {
	i.mu.RLock()
	defer i.mu.RUnlock()
	entries := make([]InboxEntry, len(i.entries))
	for idx, entry := range i.entries {
		entries[idx] = cloneInboxEntry(entry)
	}
	return entries
}

func (i *Inbox) uniqueDestination(fileName string) string {
	candidate := filepath.Join(i.root, fileName)
	if !pathExists(candidate) && !pathExists(candidate+".postamat.json") && !i.destinationReserved(candidate) {
		return candidate
	}
	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)
	for n := 1; ; n++ {
		candidate = filepath.Join(i.root, base+" ("+strconvItoa(n)+")"+ext)
		if !pathExists(candidate) && !pathExists(candidate+".postamat.json") && !i.destinationReserved(candidate) {
			return candidate
		}
	}
}

func (i *Inbox) prepareRoot() error {
	cleanRoot := filepath.Clean(i.root)
	if cleanRoot == string(filepath.Separator) || cleanRoot == filepath.Clean(os.TempDir()) {
		return ErrInboxRootUnsafe
	}
	if err := rejectSymlinkComponents(cleanRoot); err != nil {
		return err
	}
	if err := os.MkdirAll(i.root, 0o700); err != nil {
		return err
	}
	if err := rejectSymlinkComponents(cleanRoot); err != nil {
		return err
	}
	if err := os.Chmod(i.root, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(i.root)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return ErrInboxRootUnsafe
	}
	if info.Mode().Perm()&0o077 != 0 {
		return ErrInboxRootUnsafe
	}
	return nil
}

func rejectSymlinkComponents(path string) error {
	cleaned := filepath.Clean(path)
	current := "."
	if filepath.IsAbs(cleaned) {
		current = string(filepath.Separator)
	}
	for _, part := range strings.Split(strings.Trim(cleaned, string(filepath.Separator)), string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrInboxRootUnsafe
		}
	}
	return nil
}

func (i *Inbox) destinationReserved(path string) bool {
	for _, entry := range i.entries {
		if entry.DestinationPath == path {
			return true
		}
	}
	return false
}

func safeBaseName(name string) (string, error) {
	if strings.Contains(name, "/") || strings.Contains(name, `\`) || name == "." || name == ".." {
		return "", ErrUnsafeFileName
	}
	cleaned := filepath.Clean(name)
	if cleaned != name || filepath.Base(name) != name {
		return "", ErrUnsafeFileName
	}
	return name, nil
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func cloneInboxEntry(entry InboxEntry) InboxEntry {
	return entry
}

func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	idx := len(buf)
	for n > 0 {
		idx--
		buf[idx] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[idx:])
}
