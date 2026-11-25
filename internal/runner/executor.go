package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cjdupless/plugo-notebook/internal/notebook"
)

const maxOutputBytes = 1024 * 1024 // 1 MiB per stream cap

var (
	diagLineRe  = regexp.MustCompile(`(?:^|[^\w])plugo_cell_(\d+)_([a-zA-Z0-9_-]+)\.go:(\d+)(?::(\d+))?:\s*(.*)$`)
	stackLineRe = regexp.MustCompile(`(?:^|[^\w])plugo_cell_(\d+)_([a-zA-Z0-9_-]+)\.go:(\d+)(\s+.*)?$`)
)

// Executor compiles and runs composed Go cells using the local Go toolchain.
type Executor struct {
	timeout   time.Duration
	goVersion string
	goBinary  string
	workDir   string

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// NewExecutor creates a Go cell executor with the given default execution timeout.
func NewExecutor(timeout time.Duration, workDir string) *Executor {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		goBin = "go"
	}
	return &Executor{
		timeout:   timeout,
		goVersion: runtime.Version(),
		goBinary:  goBin,
		workDir:   workDir,
		cancels:   make(map[string]context.CancelFunc),
	}
}

// GoVersion returns the Go toolchain version string.
func (e *Executor) GoVersion() string {
	return e.goVersion
}

// Cancel aborts an in-flight execution for runKey, returning true if one was active.
func (e *Executor) Cancel(runKey string) bool {
	e.mu.Lock()
	cancel, ok := e.cancels[runKey]
	e.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// Execute compiles and runs targetCellID with shared declarations from preceding cells.
func (e *Executor) Execute(ctx context.Context, runKey string, cells []notebook.Cell, targetCellID string) (*notebook.ExecOutput, []notebook.Symbol, error) {
	start := time.Now()

	composed, err := Compose(cells, targetCellID)
	if err != nil {
		return nil, nil, err
	}

	runCtx, cancel := context.WithTimeout(ctx, e.timeout)
	if runKey != "" {
		e.mu.Lock()
		if prev, exists := e.cancels[runKey]; exists {
			prev()
		}
		e.cancels[runKey] = cancel
		e.mu.Unlock()
		defer func() {
			e.mu.Lock()
			delete(e.cancels, runKey)
			e.mu.Unlock()
			cancel()
		}()
	} else {
		defer cancel()
	}

	tmpDir, err := os.MkdirTemp("", "plugo-run-*")
	if err != nil {
		return nil, composed.Scope, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	goMod := fmt.Sprintf("module plugo_notebook\n\ngo %s\n", goModVersion())
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return nil, composed.Scope, fmt.Errorf("write go.mod: %w", err)
	}
	mainFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(mainFile, []byte(composed.Source), 0o644); err != nil {
		return nil, composed.Scope, fmt.Errorf("write main.go: %w", err)
	}

	binName := "plugo_cell"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(tmpDir, binName)

	// Step 1: Compile with `go build`.
	buildCmd := exec.CommandContext(runCtx, e.goBinary, "build", "-o", binPath, "main.go")
	buildCmd.Dir = tmpDir
	var buildStderr limitedBuffer
	buildStderr.limit = maxOutputBytes
	buildCmd.Stdout = &buildStderr
	buildCmd.Stderr = &buildStderr

	if err := buildCmd.Run(); err != nil {
		duration := time.Since(start).Milliseconds()
		status := "error"
		exitCode := exitCodeOf(err)
		rawErr := buildStderr.String()
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			status = "timeout"
			rawErr = fmt.Sprintf("Compilation timed out after %s.\n%s", e.timeout, rawErr)
		} else if errors.Is(runCtx.Err(), context.Canceled) {
			status = "canceled"
			rawErr = "Execution canceled."
		}

		diags, cleanedStderr := parseDiagnostics(rawErr)
		return &notebook.ExecOutput{
			Status:          status,
			Stderr:          cleanedStderr,
			ExitCode:        exitCode,
			DurationMs:      duration,
			CompileError:    status == "error",
			Diagnostics:     diags,
			AutoImports:     composed.AutoImports,
			GeneratedSource: composed.DisplayCode,
			RanAt:           time.Now().UTC().Format(time.RFC3339),
		}, composed.Scope, nil
	}

	// Step 2: Run the compiled binary.
	runCmd := exec.CommandContext(runCtx, binPath)
	if e.workDir != "" {
		runCmd.Dir = e.workDir
	} else {
		runCmd.Dir = tmpDir
	}
	var stdoutBuf, stderrBuf limitedBuffer
	stdoutBuf.limit = maxOutputBytes
	stderrBuf.limit = maxOutputBytes
	runCmd.Stdout = &stdoutBuf
	runCmd.Stderr = &stderrBuf

	runErr := runCmd.Run()
	duration := time.Since(start).Milliseconds()

	status := "ok"
	exitCode := 0
	stderrText := stderrBuf.String()
	var diags []notebook.Diagnostic

	if runErr != nil {
		exitCode = exitCodeOf(runErr)
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			status = "timeout"
			if stderrText != "" && !strings.HasSuffix(stderrText, "\n") {
				stderrText += "\n"
			}
			stderrText += fmt.Sprintf("Execution timed out after %s.", e.timeout)
		} else if errors.Is(runCtx.Err(), context.Canceled) {
			status = "canceled"
			if stderrText != "" && !strings.HasSuffix(stderrText, "\n") {
				stderrText += "\n"
			}
			stderrText += "Execution canceled."
		} else {
			status = "error"
		}
		diags, stderrText = parseDiagnostics(stderrText)
	}

	stdoutText := stdoutBuf.String()
	if stdoutBuf.truncated {
		stdoutText += "\n... [output truncated at 1 MiB]"
	}
	if stderrBuf.truncated {
		stderrText += "\n... [stderr truncated at 1 MiB]"
	}

	return &notebook.ExecOutput{
		Status:          status,
		Stdout:          stdoutText,
		Stderr:          stderrText,
		ExitCode:        exitCode,
		DurationMs:      duration,
		Diagnostics:     diags,
		AutoImports:     composed.AutoImports,
		GeneratedSource: composed.DisplayCode,
		RanAt:           time.Now().UTC().Format(time.RFC3339),
	}, composed.Scope, nil
}

func goModVersion() string {
	v := strings.TrimPrefix(runtime.Version(), "go")
	parts := strings.Split(v, ".")
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return "1.24"
}

func exitCodeOf(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func parseDiagnostics(raw string) ([]notebook.Diagnostic, string) {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	var diags []notebook.Diagnostic
	cleaned := make([]string, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "# plugo_notebook" {
			continue
		}
		if m := diagLineRe.FindStringSubmatch(line); m != nil {
			cellIdx, _ := strconv.Atoi(m[1])
			cellID := m[2]
			lineNum, _ := strconv.Atoi(m[3])
			colNum := 0
			if m[4] != "" {
				colNum, _ = strconv.Atoi(m[4])
			}
			msg := strings.TrimSpace(m[5])
			diags = append(diags, notebook.Diagnostic{
				CellID:    cellID,
				CellIndex: cellIdx,
				Line:      lineNum,
				Column:    colNum,
				Message:   msg,
				Raw:       trimmed,
			})
			if colNum > 0 {
				cleaned = append(cleaned, fmt.Sprintf("Cell %d, Line %d:%d: %s", cellIdx, lineNum, colNum, msg))
			} else {
				cleaned = append(cleaned, fmt.Sprintf("Cell %d, Line %d: %s", cellIdx, lineNum, msg))
			}
		} else if m := stackLineRe.FindStringSubmatch(line); m != nil {
			cellIdx, _ := strconv.Atoi(m[1])
			cellID := m[2]
			lineNum, _ := strconv.Atoi(m[3])
			suffix := strings.TrimSpace(m[4])
			diags = append(diags, notebook.Diagnostic{
				CellID:    cellID,
				CellIndex: cellIdx,
				Line:      lineNum,
				Message:   "runtime trace",
				Raw:       trimmed,
			})
			if suffix != "" {
				cleaned = append(cleaned, fmt.Sprintf("\tCell %d, Line %d (%s)", cellIdx, lineNum, suffix))
			} else {
				cleaned = append(cleaned, fmt.Sprintf("\tCell %d, Line %d", cellIdx, lineNum))
			}
		} else {
			cleaned = append(cleaned, line)
		}
	}

	return diags, strings.TrimSpace(strings.Join(cleaned, "\n"))
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.limit <= 0 {
		return l.buf.Write(p)
	}
	remaining := l.limit - l.buf.Len()
	if remaining <= 0 {
		l.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		l.truncated = true
		_, _ = l.buf.Write(p[:remaining])
		return len(p), nil
	}
	return l.buf.Write(p)
}

func (l *limitedBuffer) String() string {
	return l.buf.String()
}
