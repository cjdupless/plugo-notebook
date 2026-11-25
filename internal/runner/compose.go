package runner

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"sort"
	"strings"

	"github.com/cjdupless/plugo-notebook/internal/notebook"
)

// ComposedProgram is the runnable Go source generated for executing a target cell
// together with all shared declarations from earlier cells in the notebook.
type ComposedProgram struct {
	Source      string
	DisplayCode string // clean formatted code without //line directives (for "Inspect Go" drawer)
	AutoImports []string
	Scope       []notebook.Symbol
}

// CellFileTag builds the synthetic filename used in //line directives so compiler
// errors and panics map cleanly back to the exact cell and line number.
func CellFileTag(cellIndex int, cellID string) string {
	return fmt.Sprintf("plugo_cell_%d_%s.go", cellIndex, cellID)
}

// Compose builds a complete, runnable `package main` Go file for executing targetCellID.
func Compose(cells []notebook.Cell, targetCellID string) (*ComposedProgram, error) {
	targetIdx := -1
	for i, c := range cells {
		if c.ID == targetCellID {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return nil, fmt.Errorf("cell %q not found", targetCellID)
	}
	if cells[targetIdx].Type != notebook.CellTypeCode {
		return nil, fmt.Errorf("cell %q is not a Go code cell", targetCellID)
	}

	var parsedCells []*ParsedCell
	var targetParsed *ParsedCell

	for i := 0; i <= targetIdx; i++ {
		c := cells[i]
		if c.Type != notebook.CellTypeCode {
			continue
		}
		pc := ParseCell(c.ID, i+1, c.Source)
		parsedCells = append(parsedCells, pc)
		if i == targetIdx {
			targetParsed = pc
		}
	}

	decls, explicitImports, blankImports, dotImports, scope := mergeSharedState(parsedCells)
	stmts := targetParsed.MainStmts

	finalImports, autoImports := resolveImports(decls, stmts, explicitImports, blankImports, dotImports, targetCellID)

	source := emitProgram(finalImports, decls, stmts, true)
	display := emitProgram(finalImports, decls, stmts, false)
	if formatted, err := format.Source([]byte(display)); err == nil {
		display = string(formatted)
	}

	return &ComposedProgram{
		Source:      source,
		DisplayCode: display,
		AutoImports: autoImports,
		Scope:       scope,
	}, nil
}

// InspectScope returns all shared package-level symbols defined across all Go cells
// in the notebook (with later definitions superseding earlier ones of the same name).
func InspectScope(cells []notebook.Cell) []notebook.Symbol {
	var parsed []*ParsedCell
	for i, c := range cells {
		if c.Type != notebook.CellTypeCode {
			continue
		}
		parsed = append(parsed, ParseCell(c.ID, i+1, c.Source))
	}
	_, _, _, _, scope := mergeSharedState(parsed)
	return scope
}

// ExportGo exports the entire notebook as a single clean, gofmt-formatted Go file.
func ExportGo(title string, cells []notebook.Cell) (string, error) {
	var parsed []*ParsedCell
	for i, c := range cells {
		if c.Type != notebook.CellTypeCode {
			continue
		}
		parsed = append(parsed, ParseCell(c.ID, i+1, c.Source))
	}

	decls, explicitImports, blankImports, dotImports, _ := mergeSharedState(parsed)

	var allStmts []StmtChunk
	for _, pc := range parsed {
		if len(pc.MainStmts) == 0 {
			continue
		}
		allStmts = append(allStmts, StmtChunk{
			Source: fmt.Sprintf("// --- Cell %d ---", pc.CellIndex),
		})
		allStmts = append(allStmts, pc.MainStmts...)
	}

	finalImports, _ := resolveImports(decls, allStmts, explicitImports, blankImports, dotImports, "")

	var b strings.Builder
	if strings.TrimSpace(title) != "" {
		fmt.Fprintf(&b, "// Exported from Plugo notebook: %s\n", strings.TrimSpace(title))
	}
	b.WriteString(emitProgram(finalImports, decls, allStmts, false))

	if formatted, err := format.Source([]byte(b.String())); err == nil {
		return string(formatted), nil
	}
	return b.String(), nil
}

func mergeSharedState(parsedCells []*ParsedCell) (
	decls []TopDecl,
	explicitImports map[string]ImportDecl,
	blankImports []ImportDecl,
	dotImports []ImportDecl,
	scope []notebook.Symbol,
) {
	explicitImports = make(map[string]ImportDecl)
	seenBlank := make(map[string]bool)

	// Track which declaration index currently owns each symbol key so a later cell
	// cleanly replaces an earlier definition of the same func/method/type/var/const.
	ownerByKey := make(map[string]int)
	replaced := make(map[int]bool)

	var importSymbols []notebook.Symbol
	seenImportSym := make(map[string]int)

	for _, pc := range parsedCells {
		for _, imp := range pc.Imports {
			switch imp.Alias {
			case "_":
				if !seenBlank[imp.Path] {
					seenBlank[imp.Path] = true
					blankImports = append(blankImports, imp)
				}
			case ".":
				dotImports = append(dotImports, imp)
			default:
				explicitImports[imp.PkgName] = imp
				sym := notebook.Symbol{
					Kind:      "import",
					Name:      imp.PkgName,
					Signature: fmt.Sprintf("import %q", imp.Path),
					CellID:    imp.CellID,
					CellIndex: imp.CellIndex,
					Line:      imp.Line,
				}
				if imp.Alias != "" {
					sym.Signature = fmt.Sprintf("import %s %q", imp.Alias, imp.Path)
				}
				if idx, ok := seenImportSym[imp.PkgName]; ok {
					importSymbols[idx] = sym
				} else {
					seenImportSym[imp.PkgName] = len(importSymbols)
					importSymbols = append(importSymbols, sym)
				}
			}
		}

		for _, d := range pc.Decls {
			for _, k := range d.Keys {
				if prevIdx, exists := ownerByKey[k]; exists {
					// If an earlier decl shares a key with a newer decl in a different cell
					// (or redeclared later in the same cell), supersede the earlier decl.
					replaced[prevIdx] = true
				}
			}
			idx := len(decls)
			decls = append(decls, d)
			for _, k := range d.Keys {
				ownerByKey[k] = idx
			}
		}
	}

	keptDecls := make([]TopDecl, 0, len(decls))
	scope = append(scope, importSymbols...)
	for i, d := range decls {
		if replaced[i] {
			continue
		}
		keptDecls = append(keptDecls, d)
		scope = append(scope, d.Symbols...)
	}

	return keptDecls, explicitImports, blankImports, dotImports, scope
}

func resolveImports(
	decls []TopDecl,
	stmts []StmtChunk,
	explicitImports map[string]ImportDecl,
	blankImports []ImportDecl,
	dotImports []ImportDecl,
	targetCellID string,
) ([]ImportDecl, []string) {
	// Parse the combined code without imports to accurately identify every unresolved
	// selector base (`pkg.Ident`) that needs an import.
	draft := emitProgram(nil, decls, stmts, false)
	referencedPkgs := findReferencedPackages(draft)

	var finalImports []ImportDecl
	var autoImports []string
	addedPaths := make(map[string]bool)

	for _, b := range blankImports {
		if !addedPaths["_:"+b.Path] {
			addedPaths["_:"+b.Path] = true
			finalImports = append(finalImports, b)
		}
	}
	for _, d := range dotImports {
		if targetCellID == "" || d.CellID == targetCellID {
			if !addedPaths[".:"+d.Path] {
				addedPaths[".:"+d.Path] = true
				finalImports = append(finalImports, d)
			}
		}
	}

	pkgNames := make([]string, 0, len(referencedPkgs))
	for name := range referencedPkgs {
		pkgNames = append(pkgNames, name)
	}
	sort.Strings(pkgNames)

	for _, name := range pkgNames {
		if imp, ok := explicitImports[name]; ok {
			key := imp.Alias + ":" + imp.Path
			if !addedPaths[key] {
				addedPaths[key] = true
				finalImports = append(finalImports, imp)
			}
			continue
		}
		if stdPath, ok := StdlibPackages[name]; ok {
			key := ":" + stdPath
			if !addedPaths[key] {
				addedPaths[key] = true
				finalImports = append(finalImports, ImportDecl{
					Path:    stdPath,
					PkgName: name,
				})
				autoImports = append(autoImports, stdPath)
			}
		}
	}

	sort.Slice(finalImports, func(i, j int) bool {
		if finalImports[i].Path == finalImports[j].Path {
			return finalImports[i].Alias < finalImports[j].Alias
		}
		return finalImports[i].Path < finalImports[j].Path
	})
	sort.Strings(autoImports)

	return finalImports, autoImports
}

func emitProgram(imports []ImportDecl, decls []TopDecl, stmts []StmtChunk, withLineDirectives bool) string {
	var b strings.Builder
	b.WriteString("package main\n\n")

	if len(imports) > 0 {
		b.WriteString("import (\n")
		for _, imp := range imports {
			if imp.Alias != "" {
				fmt.Fprintf(&b, "\t%s %q\n", imp.Alias, imp.Path)
			} else {
				fmt.Fprintf(&b, "\t%q\n", imp.Path)
			}
		}
		b.WriteString(")\n\n")
	}

	for _, d := range decls {
		if withLineDirectives && d.CellID != "" && d.StartLine > 0 {
			fmt.Fprintf(&b, "//line %s:%d\n", CellFileTag(d.CellIndex, d.CellID), d.StartLine)
		}
		b.WriteString(d.Source)
		b.WriteString("\n\n")
	}

	b.WriteString("func main() {\n")
	for _, s := range stmts {
		if withLineDirectives && s.CellID != "" && s.StartLine > 0 {
			fmt.Fprintf(&b, "//line %s:%d\n", CellFileTag(s.CellIndex, s.CellID), s.StartLine)
		}
		b.WriteString(s.Source)
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// findReferencedPackages walks the AST with lexical scope tracking to find all
// unresolved identifiers used as the left-hand side of a selector expression (X.Sel).
func findReferencedPackages(src string) map[string]bool {
	fset := token.NewFileSet()
	file, _ := parser.ParseFile(fset, "", src, 0)
	refs := make(map[string]bool)
	if file == nil {
		return refs
	}

	topScope := make(map[string]bool)
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name != nil {
				topScope[d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					topScope[s.Name.Name] = true
				case *ast.ValueSpec:
					for _, id := range s.Names {
						topScope[id.Name] = true
					}
				}
			}
		}
	}

	w := &scopeWalker{
		scopes: []map[string]bool{topScope},
		refs:   refs,
	}
	for _, decl := range file.Decls {
		w.walkNode(decl)
	}
	return refs
}

type scopeWalker struct {
	scopes []map[string]bool
	refs   map[string]bool
}

func (w *scopeWalker) push() {
	w.scopes = append(w.scopes, make(map[string]bool))
}

func (w *scopeWalker) pop() {
	w.scopes = w.scopes[:len(w.scopes)-1]
}

func (w *scopeWalker) declare(name string) {
	if name == "" || name == "_" {
		return
	}
	w.scopes[len(w.scopes)-1][name] = true
}

func (w *scopeWalker) declareFieldList(fl *ast.FieldList) {
	if fl == nil {
		return
	}
	for _, f := range fl.List {
		for _, id := range f.Names {
			w.declare(id.Name)
		}
	}
}

func (w *scopeWalker) isDeclared(name string) bool {
	for i := len(w.scopes) - 1; i >= 0; i-- {
		if w.scopes[i][name] {
			return true
		}
	}
	return false
}

func (w *scopeWalker) walkNode(node ast.Node) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ast.SelectorExpr:
		if ident, ok := n.X.(*ast.Ident); ok {
			if !w.isDeclared(ident.Name) {
				w.refs[ident.Name] = true
			}
		} else {
			w.walkNode(n.X)
		}

	case *ast.FuncDecl:
		w.push()
		if n.Recv != nil {
			w.walkNode(n.Recv)
			w.declareFieldList(n.Recv)
		}
		if n.Type != nil {
			if n.Type.TypeParams != nil {
				w.walkNode(n.Type.TypeParams)
				w.declareFieldList(n.Type.TypeParams)
			}
			if n.Type.Params != nil {
				w.walkNode(n.Type.Params)
				w.declareFieldList(n.Type.Params)
			}
			if n.Type.Results != nil {
				w.walkNode(n.Type.Results)
				w.declareFieldList(n.Type.Results)
			}
		}
		if n.Body != nil {
			w.walkStmtList(n.Body.List)
		}
		w.pop()

	case *ast.FuncLit:
		w.push()
		if n.Type != nil {
			if n.Type.Params != nil {
				w.walkNode(n.Type.Params)
				w.declareFieldList(n.Type.Params)
			}
			if n.Type.Results != nil {
				w.walkNode(n.Type.Results)
				w.declareFieldList(n.Type.Results)
			}
		}
		if n.Body != nil {
			w.walkStmtList(n.Body.List)
		}
		w.pop()

	case *ast.BlockStmt:
		w.push()
		w.walkStmtList(n.List)
		w.pop()

	case *ast.IfStmt:
		w.push()
		w.walkNode(n.Init)
		w.walkNode(n.Cond)
		w.walkNode(n.Body)
		w.walkNode(n.Else)
		w.pop()

	case *ast.ForStmt:
		w.push()
		w.walkNode(n.Init)
		w.walkNode(n.Cond)
		w.walkNode(n.Post)
		w.walkNode(n.Body)
		w.pop()

	case *ast.RangeStmt:
		w.push()
		w.walkNode(n.X)
		if n.Tok == token.DEFINE {
			if id, ok := n.Key.(*ast.Ident); ok {
				w.declare(id.Name)
			}
			if id, ok := n.Value.(*ast.Ident); ok {
				w.declare(id.Name)
			}
		} else {
			w.walkNode(n.Key)
			w.walkNode(n.Value)
		}
		w.walkNode(n.Body)
		w.pop()

	case *ast.SwitchStmt:
		w.push()
		w.walkNode(n.Init)
		w.walkNode(n.Tag)
		w.walkNode(n.Body)
		w.pop()

	case *ast.TypeSwitchStmt:
		w.push()
		w.walkNode(n.Init)
		w.walkNode(n.Assign)
		w.walkNode(n.Body)
		w.pop()

	case *ast.CaseClause:
		w.push()
		for _, e := range n.List {
			w.walkNode(e)
		}
		w.walkStmtList(n.Body)
		w.pop()

	case *ast.CommClause:
		w.push()
		w.walkNode(n.Comm)
		w.walkStmtList(n.Body)
		w.pop()

	case *ast.AssignStmt:
		for _, rhs := range n.Rhs {
			w.walkNode(rhs)
		}
		if n.Tok == token.DEFINE {
			for _, lhs := range n.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					w.declare(id.Name)
				} else {
					w.walkNode(lhs)
				}
			}
		} else {
			for _, lhs := range n.Lhs {
				w.walkNode(lhs)
			}
		}

	case *ast.DeclStmt:
		if gd, ok := n.Decl.(*ast.GenDecl); ok {
			for _, spec := range gd.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					w.walkNode(s.Type)
					for _, v := range s.Values {
						w.walkNode(v)
					}
					for _, id := range s.Names {
						w.declare(id.Name)
					}
				case *ast.TypeSpec:
					w.declare(s.Name.Name)
					w.walkNode(s.Type)
				}
			}
		}

	default:
		ast.Inspect(node, func(child ast.Node) bool {
			if child == nil || child == node {
				return true
			}
			w.walkNode(child)
			return false
		})
	}
}

func (w *scopeWalker) walkStmtList(stmts []ast.Stmt) {
	for _, s := range stmts {
		w.walkNode(s)
	}
}
