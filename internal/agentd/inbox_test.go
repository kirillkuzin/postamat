package agentd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInboxReservesSafeDestinationAndWritesMetadata(t *testing.T) {
	root := t.TempDir()
	inbox := NewInbox(root, func() time.Time { return time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC) })

	entry, err := inbox.Reserve(InboxOffer{TransferID: "tr_1", FromAgentID: "agent-a", FileName: "report.pdf", FileSizeBytes: 42})
	if err != nil {
		t.Fatalf("Reserve returned error: %v", err)
	}
	if entry.DestinationPath != filepath.Join(root, "report.pdf") {
		t.Fatalf("destination path = %q", entry.DestinationPath)
	}
	if _, err := os.Stat(entry.MetadataPath); err != nil {
		t.Fatalf("metadata file not written: %v", err)
	}
	var metadata InboxMetadata
	data, err := os.ReadFile(entry.MetadataPath)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if metadata.TransferID != "tr_1" || metadata.FromAgentID != "agent-a" || metadata.FileName != "report.pdf" || metadata.FileSizeBytes != 42 {
		t.Fatalf("metadata mismatch: %+v", metadata)
	}
}

func TestInboxRejectsTraversalAndSanitizesPathSeparators(t *testing.T) {
	root := t.TempDir()
	inbox := NewInbox(root, nil)
	if _, err := inbox.Reserve(InboxOffer{TransferID: "tr_1", FromAgentID: "agent-a", FileName: "../secret.txt", FileSizeBytes: 1}); !errors.Is(err, ErrUnsafeFileName) {
		t.Fatalf("traversal error = %v, want ErrUnsafeFileName", err)
	}
	if _, err := inbox.Reserve(InboxOffer{TransferID: "tr_2", FromAgentID: "agent-a", FileName: `nested\\secret.txt`, FileSizeBytes: 1}); !errors.Is(err, ErrUnsafeFileName) {
		t.Fatalf("backslash traversal error = %v, want ErrUnsafeFileName", err)
	}
}

func TestInboxAvoidsDuplicateFilenames(t *testing.T) {
	root := t.TempDir()
	inbox := NewInbox(root, nil)
	first, err := inbox.Reserve(InboxOffer{TransferID: "tr_1", FromAgentID: "agent-a", FileName: "report.pdf", FileSizeBytes: 1})
	if err != nil {
		t.Fatalf("first reserve returned error: %v", err)
	}
	if err := os.WriteFile(first.DestinationPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("write existing destination: %v", err)
	}
	second, err := inbox.Reserve(InboxOffer{TransferID: "tr_2", FromAgentID: "agent-a", FileName: "report.pdf", FileSizeBytes: 1})
	if err != nil {
		t.Fatalf("second reserve returned error: %v", err)
	}
	if filepath.Base(second.DestinationPath) != "report (1).pdf" {
		t.Fatalf("duplicate destination = %q", second.DestinationPath)
	}
}

func TestInboxAvoidsExistingMetadataPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.pdf.postamat.json"), []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale metadata: %v", err)
	}
	inbox := NewInbox(root, nil)
	entry, err := inbox.Reserve(InboxOffer{TransferID: "tr_1", FromAgentID: "agent-a", FileName: "report.pdf", FileSizeBytes: 1})
	if err != nil {
		t.Fatalf("Reserve returned error: %v", err)
	}
	if filepath.Base(entry.DestinationPath) != "report (1).pdf" {
		t.Fatalf("destination = %q, want report (1).pdf because metadata path exists", entry.DestinationPath)
	}
	data, err := os.ReadFile(filepath.Join(root, "report.pdf.postamat.json"))
	if err != nil {
		t.Fatalf("read stale metadata: %v", err)
	}
	if string(data) != "stale" {
		t.Fatalf("stale metadata overwritten: %q", data)
	}
}

func TestInboxCreatesMetadataExclusively(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(filepath.Join(root, "target"), filepath.Join(root, "report.pdf.postamat.json")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	inbox := NewInbox(root, nil)
	entry, err := inbox.Reserve(InboxOffer{TransferID: "tr_1", FromAgentID: "agent-a", FileName: "report.pdf", FileSizeBytes: 1})
	if err != nil {
		t.Fatalf("Reserve returned error: %v", err)
	}
	if filepath.Base(entry.DestinationPath) != "report (1).pdf" {
		t.Fatalf("destination = %q, want report (1).pdf because metadata symlink exists", entry.DestinationPath)
	}
	if _, err := os.Stat(filepath.Join(root, "target")); !os.IsNotExist(err) {
		t.Fatalf("symlink target was created/overwritten, stat err = %v", err)
	}
}

func TestInboxRejectsSymlinkRoot(t *testing.T) {
	parent := t.TempDir()
	realRoot := filepath.Join(parent, "real-inbox")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatalf("mkdir real root: %v", err)
	}
	linkedRoot := filepath.Join(parent, "linked-inbox")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatalf("create root symlink: %v", err)
	}

	inbox := NewInbox(linkedRoot, nil)
	if _, err := inbox.Reserve(InboxOffer{TransferID: "tr_1", FromAgentID: "agent-a", FileName: "report.pdf", FileSizeBytes: 1}); !errors.Is(err, ErrInboxRootUnsafe) {
		t.Fatalf("symlink root error = %v, want ErrInboxRootUnsafe", err)
	}
	if _, err := os.Stat(filepath.Join(realRoot, "report.pdf.postamat.json")); !os.IsNotExist(err) {
		t.Fatalf("metadata escaped into symlink target, stat err = %v", err)
	}
}

func TestInboxListReturnsEntriesInReservationOrder(t *testing.T) {
	inbox := NewInbox(t.TempDir(), func() time.Time { return time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC) })
	first, err := inbox.Reserve(InboxOffer{TransferID: "tr_1", FromAgentID: "agent-a", FileName: "a.txt", FileSizeBytes: 1})
	if err != nil {
		t.Fatalf("Reserve returned error: %v", err)
	}
	second, err := inbox.Reserve(InboxOffer{TransferID: "tr_2", FromAgentID: "agent-b", FileName: "b.txt", FileSizeBytes: 2})
	if err != nil {
		t.Fatalf("Reserve returned error: %v", err)
	}
	entries := inbox.List()
	if len(entries) != 2 || entries[0].TransferID != first.TransferID || entries[1].TransferID != second.TransferID {
		t.Fatalf("entries not listed in reservation order: %+v", entries)
	}
}
