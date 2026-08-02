package observe

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Status struct {
	Paused bool  `json:"paused"`
	Files  int   `json:"files"`
	Bytes  int64 `json:"bytes"`
}

func Inspect(directory string) (Status, error) {
	var status Status
	if _, err := os.Stat(filepath.Join(directory, ".paused")); err == nil {
		status.Paused = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return status, err
	}
	for _, source := range managedSources {
		err := walkManagedJSONL(directory, source, func(_ string, entry fs.DirEntry) error {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			status.Files++
			status.Bytes += info.Size()
			return nil
		})
		if err != nil {
			return status, err
		}
	}
	return status, nil
}

func Pause(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(directory, ".paused"), os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	return file.Close()
}

func Resume(directory string) error {
	err := os.Remove(filepath.Join(directory, ".paused"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Clear removes observation JSONL files only. The caller must pause first.
func Clear(directory string) error {
	status, err := Inspect(directory)
	if err != nil {
		return err
	}
	if !status.Paused {
		return errors.New("observation recording must be paused before clear")
	}
	for _, source := range managedSources {
		if err := walkManagedJSONL(directory, source, func(path string, _ fs.DirEntry) error { return os.Remove(path) }); err != nil {
			return err
		}
	}
	return nil
}

// Export copies already-redacted JSONL files into a newly created directory.
// Salt and control files are deliberately excluded.
func Export(directory, destination string) error {
	if destination == "" || filepath.Clean(destination) == "." {
		return errors.New("explicit export destination is required")
	}
	sourceAbsolute, err := filepath.Abs(directory)
	if err != nil {
		return fmt.Errorf("resolve observation directory: %w", err)
	}
	destinationAbsolute, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve export destination: %w", err)
	}
	relativeToSource, err := filepath.Rel(sourceAbsolute, destinationAbsolute)
	if err != nil {
		return fmt.Errorf("compare export paths: %w", err)
	}
	if relativeToSource == "." || (!strings.HasPrefix(relativeToSource, ".."+string(filepath.Separator)) && relativeToSource != "..") {
		return errors.New("export destination must be outside the observation directory")
	}
	if err := os.Mkdir(destinationAbsolute, 0o700); err != nil {
		return fmt.Errorf("create export directory: %w", err)
	}
	for _, source := range managedSources {
		err := walkManagedJSONL(directory, source, func(path string, _ fs.DirEntry) error {
			target := filepath.Join(destinationAbsolute, source, filepath.Base(path))
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			return copyFile(path, target)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func walkManagedJSONL(directory, source string, visit func(string, fs.DirEntry) error) error {
	root := filepath.Join(directory, source)
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || filepath.Ext(entry.Name()) != ".jsonl" {
			return nil
		}
		return visit(path, entry)
	})
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	return errors.Join(copyErr, closeErr)
}
