package notebook

import "time"

// CellType identifies whether a notebook cell contains Go code or Markdown.
type CellType string

const (
	CellTypeCode     CellType = "code"
	CellTypeMarkdown CellType = "markdown"
)

// Diagnostic represents a compiler or parse diagnostic mapped back to a specific cell and line.
type Diagnostic struct {
	CellID    string `json:"cellId,omitempty"`
	CellIndex int    `json:"cellIndex,omitempty"` // 1-based code/notebook cell number
	Line      int    `json:"line,omitempty"`
	Column    int    `json:"column,omitempty"`
	Message   string `json:"message"`
	Raw       string `json:"raw,omitempty"`
}

// Symbol describes a shared Go package-level declaration defined in a cell.
type Symbol struct {
	Kind      string `json:"kind"` // import, type, func, method, var, const
	Name      string `json:"name"`
	Signature string `json:"signature,omitempty"`
	CellID    string `json:"cellId"`
	CellIndex int    `json:"cellIndex"` // 1-based index in notebook
	Line      int    `json:"line"`
}

// ExecOutput stores the result of running a Go code cell.
type ExecOutput struct {
	Status          string       `json:"status"` // ok, error, timeout, canceled
	Stdout          string       `json:"stdout,omitempty"`
	Stderr          string       `json:"stderr,omitempty"`
	ExitCode        int          `json:"exitCode"`
	DurationMs      int64        `json:"durationMs"`
	CompileError    bool         `json:"compileError,omitempty"`
	Diagnostics     []Diagnostic `json:"diagnostics,omitempty"`
	AutoImports     []string     `json:"autoImports,omitempty"`
	GeneratedSource string       `json:"generatedSource,omitempty"`
	RanAt           string       `json:"ranAt,omitempty"`
}

// Cell is a single Markdown or Go code block within a Plugo notebook.
type Cell struct {
	ID        string      `json:"id"`
	Type      CellType    `json:"type"`
	Source    string      `json:"source"`
	ExecCount int         `json:"execCount,omitempty"`
	Output    *ExecOutput `json:"output,omitempty"`
}

// Notebook represents a saved Plugo notebook (.plugo).
type Notebook struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Cells     []Cell    `json:"cells"`
}

// Summary is lightweight metadata used for listing notebooks in the sidebar.
type Summary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updatedAt"`
	CellCount int       `json:"cellCount"`
}
