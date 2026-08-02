package observe

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

func testOptions(directory string) Options {
	now := time.Now().UTC()
	return Options{
		Directory: directory, Source: "engine", MaxFileBytes: 1024,
		MaxFiles: 2, Retention: time.Hour,
		Clock: func() time.Time { return now },
	}
}

func TestRecorderHashesSensitiveTargetFields(t *testing.T) {
	directory := t.TempDir()
	recorder, err := New(testOptions(directory))
	if err != nil {
		t.Fatal(err)
	}
	committed := true
	err = recorder.Record(Event{
		EventType: "decision", Target: &model.Target{NetworkProfileID: "home-wifi", Hostname: "Secret.Example.", Port: 443, Transport: model.TransportTCP},
		SelectedPath: model.PathDirect, ReasonCode: "direct_candidate_won", Committed: &committed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	line := readOnlyJSONL(t, directory)
	if strings.Contains(line, "Secret.Example") || strings.Contains(line, "home-wifi") {
		t.Fatalf("record contains cleartext identity: %s", line)
	}
	var event storedEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatal(err)
	}
	if event.Target == nil || len(event.Target.HostnameHash) != 64 || len(event.Target.NetworkProfileHash) != 64 || event.Target.Hostname != "" {
		t.Fatalf("stored target = %+v", event.Target)
	}
	if event.SchemaVersion != 1 || !event.CommittedValue() {
		t.Fatalf("stored event = %+v", event)
	}
}

func (e storedEvent) CommittedValue() bool { return e.Committed != nil && *e.Committed }

func TestRecorderCleartextRequiresExplicitOption(t *testing.T) {
	opts := testOptions(t.TempDir())
	opts.IncludeCleartextHostname = true
	recorder, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	target := model.Target{Hostname: "visible.example", Transport: model.TransportTCP}
	if err := recorder.Record(Event{EventType: "diagnostic", Target: &target}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if line := readOnlyJSONL(t, opts.Directory); !strings.Contains(line, "visible.example") {
		t.Fatalf("line = %s", line)
	}
}

func TestRecorderPauseDropsEventsAndResumeContinues(t *testing.T) {
	directory := t.TempDir()
	recorder, err := New(testOptions(directory))
	if err != nil {
		t.Fatal(err)
	}
	if err := Pause(directory); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Record(Event{EventType: "paused"}); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(directory)
	if err != nil {
		t.Fatal(err)
	}
	if status.Files != 0 || !status.Paused {
		t.Fatalf("paused status = %+v", status)
	}
	if err := Resume(directory); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Record(Event{EventType: "resumed"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	status, err = Inspect(directory)
	if err != nil {
		t.Fatal(err)
	}
	if status.Files != 1 || status.Paused {
		t.Fatalf("resumed status = %+v", status)
	}
}

func TestRecorderRotatesAndCapsFiles(t *testing.T) {
	directory := t.TempDir()
	opts := testOptions(directory)
	opts.MaxFileBytes = 1024
	recorder, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if err := recorder.Record(Event{EventType: "diagnostic", FailureClass: strings.Repeat("x", 300)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(directory)
	if err != nil {
		t.Fatal(err)
	}
	if status.Files != 2 {
		t.Fatalf("file count = %d, want 2", status.Files)
	}
}

func TestRecorderRotatesWhenRetentionAgeIsReached(t *testing.T) {
	directory := t.TempDir()
	now := time.Now().UTC()
	opts := testOptions(directory)
	opts.Clock = func() time.Time { return now }
	recorder, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Record(Event{EventType: "first"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if err := recorder.Record(Event{EventType: "second"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(directory)
	if err != nil {
		t.Fatal(err)
	}
	if status.Files != 1 {
		t.Fatalf("file count after retention rotation = %d, want 1", status.Files)
	}
}

func TestClearRequiresPauseAndExportExcludesSalt(t *testing.T) {
	directory := t.TempDir()
	recorder, err := New(testOptions(directory))
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Record(Event{EventType: "decision"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Clear(directory); err == nil {
		t.Fatal("Clear() without pause error = nil")
	}

	export := filepath.Join(t.TempDir(), "export")
	if err := Export(directory, export); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(export, ".salt")); !os.IsNotExist(err) {
		t.Fatalf("exported salt stat error = %v", err)
	}
	if status, err := Inspect(export); err != nil || status.Files != 1 {
		t.Fatalf("export status = %+v, err = %v", status, err)
	}
	if err := Pause(directory); err != nil {
		t.Fatal(err)
	}
	if err := Clear(directory); err != nil {
		t.Fatal(err)
	}
	if status, err := Inspect(directory); err != nil || status.Files != 0 || !status.Paused {
		t.Fatalf("cleared status = %+v, err = %v", status, err)
	}
}

func TestClearDoesNotDeleteUnmanagedJSONL(t *testing.T) {
	directory := t.TempDir()
	unmanagedDirectory := filepath.Join(directory, "unrelated")
	if err := os.Mkdir(unmanagedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	unmanaged := filepath.Join(unmanagedDirectory, "keep.jsonl")
	if err := os.WriteFile(unmanaged, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Pause(directory); err != nil {
		t.Fatal(err)
	}
	if err := Clear(directory); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(unmanaged); err != nil || string(data) != "keep\n" {
		t.Fatalf("unmanaged file data=%q err=%v", data, err)
	}
}

func TestNewRejectsUnsafeOptions(t *testing.T) {
	for _, opts := range []Options{
		{Directory: ".", Source: "engine", MaxFileBytes: 1024, MaxFiles: 1, Retention: time.Hour},
		{Directory: t.TempDir(), Source: "../engine", MaxFileBytes: 1024, MaxFiles: 1, Retention: time.Hour},
	} {
		if _, err := New(opts); err == nil {
			t.Fatalf("New(%+v) error = nil", opts)
		}
	}
}

func TestRecordRejectsEventLargerThanFileLimit(t *testing.T) {
	recorder, err := New(testOptions(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	if err := recorder.Record(Event{EventType: "diagnostic", FailureClass: strings.Repeat("x", 2048)}); err == nil {
		t.Fatal("oversized Record() error = nil")
	}
}

func TestExportRejectsDestinationInsideObservationDirectory(t *testing.T) {
	directory := t.TempDir()
	if err := Export(directory, filepath.Join(directory, "export")); err == nil {
		t.Fatal("nested Export() error = nil")
	}
}

func readOnlyJSONL(t *testing.T, directory string) string {
	t.Helper()
	var result string
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		if scanner.Scan() {
			result = scanner.Text()
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Fatal("no JSONL record found")
	}
	return result
}
