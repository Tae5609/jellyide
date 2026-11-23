package metadata

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
)

// TagWriter writes and reads tags on audio files. The production
// implementation shells out to update_metadata.py; tests substitute a stub
// so no Python or real audio files are needed.
type TagWriter interface {
	// WriteFields sets the named fields on the file at path. A nil value
	// clears the tag.
	WriteFields(path string, fields map[string]interface{}) error
	// ReadFields reads the current tags of the file at path. T16 uses it
	// for the apply-time re-read: before writing, the file is re-read and
	// compared with the DB snapshot so an external change in a field being
	// edited is reported as a conflict instead of being silently lost.
	ReadFields(path string) (map[string]interface{}, error)
	// EmbedCover stores the cover image at coverPath into the file at path.
	EmbedCover(path, coverPath string) error
	// ExtractCover writes the cover embedded in the file at path to
	// outPath. It reports false when the file has no embedded art.
	ExtractCover(path, outPath string) (bool, error)
}

// NewPythonTagWriter returns the production TagWriter (update_metadata.py).
func NewPythonTagWriter() TagWriter { return pythonTagWriter{} }

// pythonTagWriter is the production TagWriter. It always invokes the
// helper as an argument list (never a shell) with `--` before the file
// path so the path cannot be parsed as an option.
type pythonTagWriter struct{}

func (pythonTagWriter) WriteFields(path string, fields map[string]interface{}) error {
	payload, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("encode fields: %w", err)
	}
	return runHelper(append([]string{"--fields-json", string(payload)}, "--", path)...)
}

// ReadFields runs the helper in --read mode, which prints the normalized
// tag map (all canonical fields, missing values null) as JSON.
func (pythonTagWriter) ReadFields(path string) (map[string]interface{}, error) {
	cmd := exec.Command("python3", "update_metadata.py", "--read", "--", path) //nolint:gosec // G204 - python3 and the script name are fixed; path is a file path rooted in the music dir, after `--`
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("update_metadata.py --read: %w. Output: %s", err, string(out))
	}
	fields := map[string]interface{}{}
	if err := json.Unmarshal(out, &fields); err != nil {
		return nil, fmt.Errorf("parse --read output: %w", err)
	}
	return fields, nil
}

func (pythonTagWriter) EmbedCover(path, coverPath string) error {
	return runHelper("--cover", coverPath, "--", path)
}

func (pythonTagWriter) ExtractCover(path, outPath string) (bool, error) {
	cmd := exec.Command("python3", "update_metadata.py", "--extract-cover", outPath, "--", path) //nolint:gosec // G204 - python3 and the script name are fixed; path is a file path rooted in the music dir, after `--`
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
		return false, nil // exit code 2: no embedded cover
	}
	return false, fmt.Errorf("update_metadata.py: %w. Output: %s", err, string(out))
}

func runHelper(args ...string) error {
	cmd := exec.Command("python3", append([]string{"update_metadata.py"}, args...)...) //nolint:gosec // G204 - python3 and the script name are fixed; all values are after `--`
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("update_metadata.py: %w. Output: %s", err, string(out))
	}
	return nil
}
