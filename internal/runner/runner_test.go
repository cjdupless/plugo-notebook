package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cjdupless/plugo-notebook/internal/notebook"
)

func TestExecuteSharedPackageAndAutoImports(t *testing.T) {
	exec := NewExecutor(15*time.Second, t.TempDir())

	cells := []notebook.Cell{
		{
			ID:   "c1",
			Type: notebook.CellTypeCode,
			Source: `
const Greeting = "Hello"

type Person struct {
	Name string
}

func (p Person) Say() string {
	return fmt.Sprintf("%s, %s!", Greeting, strings.ToUpper(p.Name))
}

fmt.Println("cell1 only")
`,
		},
		{
			ID:   "c2",
			Type: notebook.CellTypeMarkdown,
			Source: "# Just markdown",
		},
		{
			ID:   "c3",
			Type: notebook.CellTypeCode,
			Source: `
p := Person{Name: "plugo"}
fmt.Println(p.Say())
`,
		},
	}

	out, scope, err := exec.Execute(context.Background(), "nb:c3", cells, "c3")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if out.Status != "ok" {
		t.Fatalf("expected status ok, got %q (stderr: %s)", out.Status, out.Stderr)
	}
	if strings.TrimSpace(out.Stdout) != "Hello, PLUGO!" {
		t.Fatalf("unexpected stdout: %q", out.Stdout)
	}
	// Cell 1's main statement ("cell1 only") must not run when executing Cell 3.
	if strings.Contains(out.Stdout, "cell1 only") {
		t.Fatalf("cell 1 main body leaked into cell 3 output: %q", out.Stdout)
	}
	if len(scope) < 3 {
		t.Fatalf("expected at least 3 shared symbols in scope, got %d: %+v", len(scope), scope)
	}
}

func TestUnusedImportPruningAcrossCells(t *testing.T) {
	exec := NewExecutor(15*time.Second, t.TempDir())

	cells := []notebook.Cell{
		{
			ID:   "c1",
			Type: notebook.CellTypeCode,
			Source: `import "time"
fmt.Println(time.Now().Year())
`,
		},
		{
			ID:   "c2",
			Type: notebook.CellTypeCode,
			Source: `fmt.Println("no time used here")`,
		},
	}

	out, _, err := exec.Execute(context.Background(), "nb:c2", cells, "c2")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if out.Status != "ok" {
		t.Fatalf("expected ok, got %q: %s", out.Status, out.Stderr)
	}
	if strings.TrimSpace(out.Stdout) != "no time used here" {
		t.Fatalf("unexpected stdout: %q", out.Stdout)
	}
}

func TestOverrideSharedFuncAndLocalVarOrder(t *testing.T) {
	exec := NewExecutor(15*time.Second, t.TempDir())

	cells := []notebook.Cell{
		{
			ID:   "c1",
			Type: notebook.CellTypeCode,
			Source: `var base = 10
func Double(n int) int { return n * 2 }`,
		},
		{
			ID:   "c2",
			Type: notebook.CellTypeCode,
			Source: `func Double(n int) int { return n * 3 }
a := base + 1
var b = a + 2
fmt.Println(Double(b))`,
		},
	}

	out, _, err := exec.Execute(context.Background(), "nb:c2", cells, "c2")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if out.Status != "ok" {
		t.Fatalf("expected ok, got %q: %s", out.Status, out.Stderr)
	}
	if strings.TrimSpace(out.Stdout) != "39" {
		t.Fatalf("expected 39, got %q", out.Stdout)
	}
}

func TestFullPackageMainCell(t *testing.T) {
	exec := NewExecutor(15*time.Second, t.TempDir())

	cells := []notebook.Cell{
		{
			ID:   "c1",
			Type: notebook.CellTypeCode,
			Source: `package main

import "fmt"

func helper() string { return "from full pkg" }

func main() {
	fmt.Println(helper())
}
`,
		},
	}

	out, _, err := exec.Execute(context.Background(), "nb:c1", cells, "c1")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if out.Status != "ok" || strings.TrimSpace(out.Stdout) != "from full pkg" {
		t.Fatalf("unexpected output: status=%q stdout=%q stderr=%q", out.Status, out.Stdout, out.Stderr)
	}
}

func TestCompileErrorLineMapping(t *testing.T) {
	exec := NewExecutor(15*time.Second, t.TempDir())

	cells := []notebook.Cell{
		{
			ID:   "c1",
			Type: notebook.CellTypeCode,
			Source: `x := 1
y := 2
fmt.Println(x + y + missingVar)
`,
		},
	}

	out, _, err := exec.Execute(context.Background(), "nb:c1", cells, "c1")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if out.Status != "error" || !out.CompileError {
		t.Fatalf("expected compile error, got %+v", out)
	}
	if len(out.Diagnostics) == 0 {
		t.Fatalf("expected diagnostics, stderr was: %s", out.Stderr)
	}
	d := out.Diagnostics[0]
	if d.CellID != "c1" || d.CellIndex != 1 || d.Line != 3 {
		t.Fatalf("expected diagnostic at cell c1 index 1 line 3, got %+v", d)
	}
}

func TestExecutionTimeout(t *testing.T) {
	exec := NewExecutor(1200*time.Millisecond, t.TempDir())

	cells := []notebook.Cell{
		{
			ID:     "c1",
			Type:   notebook.CellTypeCode,
			Source: `time.Sleep(5 * time.Second)`,
		},
	}

	out, _, err := exec.Execute(context.Background(), "nb:c1", cells, "c1")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if out.Status != "timeout" {
		t.Fatalf("expected timeout status, got %q (stderr: %s)", out.Status, out.Stderr)
	}
}

func TestFormatCellAndExportGo(t *testing.T) {
	raw := `type Item struct{
Name string
}
func (i Item) Upper() string{return strings.ToUpper(i.Name)}
it:=Item{Name:"plugo"}
fmt.Println(it.Upper())`

	formatted, err := FormatCell(raw)
	if err != nil {
		t.Fatalf("FormatCell failed: %v", err)
	}
	if !strings.Contains(formatted, "type Item struct {\n\tName string\n}") {
		t.Fatalf("unexpected formatted output:\n%s", formatted)
	}
	if !strings.Contains(formatted, `func (i Item) Upper() string { return strings.ToUpper(i.Name) }`) {
		t.Fatalf("method not formatted properly:\n%s", formatted)
	}
	if !strings.Contains(formatted, `it := Item{Name: "plugo"}`) {
		t.Fatalf("statement not formatted properly:\n%s", formatted)
	}

	exported, err := ExportGo("Demo", []notebook.Cell{
		{ID: "c1", Type: notebook.CellTypeCode, Source: formatted},
	})
	if err != nil {
		t.Fatalf("ExportGo failed: %v", err)
	}
	if !strings.Contains(exported, "package main") || !strings.Contains(exported, `"strings"`) || !strings.Contains(exported, `"fmt"`) {
		t.Fatalf("unexpected ExportGo output:\n%s", exported)
	}
}
