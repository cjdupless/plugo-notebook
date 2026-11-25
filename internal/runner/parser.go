package runner

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"strconv"
	"strings"

	"github.com/cjdupless/plugo-notebook/internal/notebook"
)

// ImportDecl represents a single Go import specification extracted from a cell.
type ImportDecl struct {
	Alias     string // "", ".", "_", or explicit identifier
	Path      string // unquoted import path
	PkgName   string // effective identifier used in code ("" for "_" or ".")
	CellID    string
	CellIndex int
	Line      int
}

// TopDecl represents a shared package-level declaration (func, method, type, var, const).
type TopDecl struct {
	Kind      string   // func, method, type, var, const, init
	Keys      []string // deduplication keys (e.g. "func:Add", "method:Gopher.Greet", "type:Gopher")
	Symbols   []notebook.Symbol
	Source    string
	CellID    string
	CellIndex int
	StartLine int
}

// StmtChunk represents executable statements destined for func main().
type StmtChunk struct {
	Source    string
	CellID    string
	CellIndex int
	StartLine int
}

// ParsedCell is the structured breakdown of a single Go code cell.
type ParsedCell struct {
	CellID        string
	CellIndex     int // 1-based index in the notebook
	IsFullPackage bool
	Imports       []ImportDecl
	Decls         []TopDecl
	MainStmts     []StmtChunk
	ParseError    string
}

type scannedToken struct {
	offset    int
	endOffset int
	line      int
	tok       token.Token
	lit       string
}

type topChunk struct {
	startOffset int
	endOffset   int
	startLine   int
	firstTok    token.Token
	tokens      []scannedToken
}

// ParseCell parses a Go code cell into imports, shared package-level declarations,
// and executable statements for main(). It accepts both snippet-style cells (without
// package/main boilerplate) and full `package main` cells.
func ParseCell(cellID string, cellIndex int, src string) *ParsedCell {
	pc := &ParsedCell{
		CellID:    cellID,
		CellIndex: cellIndex,
	}

	normalized := strings.ReplaceAll(src, "\r\n", "\n")
	if strings.TrimSpace(normalized) == "" {
		return pc
	}

	tokens := scanTokens(normalized)
	if len(tokens) == 0 {
		return pc
	}

	// Case 1: Full Go file beginning with `package ...`.
	if tokens[0].tok == token.PACKAGE {
		pc.IsFullPackage = true
		parseFullPackageCell(pc, normalized)
		return pc
	}

	// Case 2: Go-native snippet cell (mixed imports, declarations, and top-level statements).
	chunks := splitTopChunks(tokens)
	hasStmt := false

	for _, ch := range chunks {
		chunkSrc := strings.TrimRight(normalized[ch.startOffset:ch.endOffset], " \t\n;")
		if strings.TrimSpace(chunkSrc) == "" {
			continue
		}

		switch ch.firstTok {
		case token.IMPORT:
			if imports, ok := parseImportChunk(chunkSrc, cellID, cellIndex, ch.startLine); ok {
				pc.Imports = append(pc.Imports, imports...)
				continue
			}
			// Fall through to statement/compiler error if malformed import.
			pc.MainStmts = append(pc.MainStmts, StmtChunk{
				Source:    chunkSrc,
				CellID:    cellID,
				CellIndex: cellIndex,
				StartLine: ch.startLine,
			})

		case token.FUNC:
			// Distinguish named function / method declarations from immediately-invoked
			// function literals like `func() { ... }()`.
			if decl, mainBody, mainLine, handled := parseFuncChunk(chunkSrc, cellID, cellIndex, ch.startLine); handled {
				if mainBody != "" {
					hasStmt = true
					pc.MainStmts = append(pc.MainStmts, StmtChunk{
						Source:    mainBody,
						CellID:    cellID,
						CellIndex: cellIndex,
						StartLine: mainLine,
					})
				} else if decl != nil {
					pc.Decls = append(pc.Decls, *decl)
				}
				continue
			}
			hasStmt = true
			pc.MainStmts = append(pc.MainStmts, StmtChunk{
				Source:    chunkSrc,
				CellID:    cellID,
				CellIndex: cellIndex,
				StartLine: ch.startLine,
			})

		case token.TYPE, token.CONST:
			if decl, ok := parseGenDeclChunk(chunkSrc, cellID, cellIndex, ch.startLine); ok {
				pc.Decls = append(pc.Decls, decl)
				continue
			}
			// Keep malformed type/const at package level so Go reports a clean compiler diagnostic.
			pc.Decls = append(pc.Decls, TopDecl{
				Kind:      strings.ToLower(ch.firstTok.String()),
				Keys:      []string{fmt.Sprintf("raw:%s:%d", cellID, ch.startLine)},
				Source:    chunkSrc,
				CellID:    cellID,
				CellIndex: cellIndex,
				StartLine: ch.startLine,
			})

		case token.VAR:
			// Hoist leading `var` declarations (before any executable statements in the cell)
			// to shared package scope. Keep `var` declarations that appear after statements
			// inside main() so they can safely reference local variables.
			if !hasStmt {
				if decl, ok := parseGenDeclChunk(chunkSrc, cellID, cellIndex, ch.startLine); ok {
					pc.Decls = append(pc.Decls, decl)
					continue
				}
			}
			hasStmt = true
			pc.MainStmts = append(pc.MainStmts, StmtChunk{
				Source:    chunkSrc,
				CellID:    cellID,
				CellIndex: cellIndex,
				StartLine: ch.startLine,
			})

		default:
			hasStmt = true
			pc.MainStmts = append(pc.MainStmts, StmtChunk{
				Source:    chunkSrc,
				CellID:    cellID,
				CellIndex: cellIndex,
				StartLine: ch.startLine,
			})
		}
	}

	return pc
}

func parseFullPackageCell(pc *ParsedCell, src string) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "cell.go", src, parser.ParseComments)
	if err != nil {
		pc.ParseError = err.Error()
		if file == nil {
			// Pass verbatim so go build reports exact compiler diagnostics.
			pc.MainStmts = append(pc.MainStmts, StmtChunk{
				Source:    src,
				CellID:    pc.CellID,
				CellIndex: pc.CellIndex,
				StartLine: 1,
			})
			return
		}
	}

	for _, decl := range file.Decls {
		startPos := fset.Position(decl.Pos())
		endPos := fset.Position(decl.End())
		if startPos.Offset < 0 || endPos.Offset > len(src) || startPos.Offset >= endPos.Offset {
			continue
		}
		chunkSrc := src[startPos.Offset:endPos.Offset]

		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				for _, spec := range d.Specs {
					if imp, ok := spec.(*ast.ImportSpec); ok {
						pc.Imports = append(pc.Imports, importSpecToDecl(imp, pc.CellID, pc.CellIndex, fset.Position(imp.Pos()).Line))
					}
				}
				continue
			}
			top := buildGenTopDecl(d, fset, chunkSrc, pc.CellID, pc.CellIndex, startPos.Line, 0)
			pc.Decls = append(pc.Decls, top)

		case *ast.FuncDecl:
			if d.Recv == nil && d.Name.Name == "main" {
				if d.Body != nil {
					lbrace := fset.Position(d.Body.Lbrace)
					rbrace := fset.Position(d.Body.Rbrace)
					if lbrace.Offset+1 < rbrace.Offset && rbrace.Offset <= len(src) {
						body, startLine := extractBraceBody(src[lbrace.Offset+1:rbrace.Offset], lbrace.Line)
						if strings.TrimSpace(body) != "" {
							pc.MainStmts = append(pc.MainStmts, StmtChunk{
								Source:    body,
								CellID:    pc.CellID,
								CellIndex: pc.CellIndex,
								StartLine: startLine,
							})
						}
					}
				}
				continue
			}
			top := buildFuncTopDecl(d, fset, chunkSrc, pc.CellID, pc.CellIndex, startPos.Line)
			pc.Decls = append(pc.Decls, top)
		}
	}
}

func scanTokens(src string) []scannedToken {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))

	var s scanner.Scanner
	s.Init(file, []byte(src), nil, 0)

	var out []scannedToken
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		p := fset.Position(pos)
		endOff := p.Offset + tokenLength(tok, lit)
		if endOff > len(src) {
			endOff = len(src)
		}
		out = append(out, scannedToken{
			offset:    p.Offset,
			endOffset: endOff,
			line:      p.Line,
			tok:       tok,
			lit:       lit,
		})
	}
	return out
}

func tokenLength(tok token.Token, lit string) int {
	if tok == token.SEMICOLON && lit == "\n" {
		return 0
	}
	if lit != "" {
		return len(lit)
	}
	return len(tok.String())
}

func splitTopChunks(tokens []scannedToken) []topChunk {
	var chunks []topChunk
	depth := 0
	chunkStart := -1

	flush := func(lastIdx int) {
		if chunkStart < 0 || lastIdx < chunkStart {
			chunkStart = -1
			return
		}
		first := tokens[chunkStart]
		last := tokens[lastIdx]
		chunks = append(chunks, topChunk{
			startOffset: first.offset,
			endOffset:   last.endOffset,
			startLine:   first.line,
			firstTok:    first.tok,
			tokens:      tokens[chunkStart : lastIdx+1],
		})
		chunkStart = -1
	}

	for i, tk := range tokens {
		if depth == 0 && tk.tok == token.SEMICOLON {
			flush(i - 1)
			continue
		}
		if chunkStart < 0 {
			chunkStart = i
		}
		switch tk.tok {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACK, token.RBRACE:
			if depth > 0 {
				depth--
			}
		}
	}
	if chunkStart >= 0 {
		flush(len(tokens) - 1)
	}
	return chunks
}

func parseImportChunk(chunkSrc, cellID string, cellIndex, startLine int) ([]ImportDecl, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", "package plugo\n"+chunkSrc, 0)
	if err != nil || file == nil || len(file.Imports) == 0 {
		return nil, false
	}
	out := make([]ImportDecl, 0, len(file.Imports))
	for _, imp := range file.Imports {
		line := startLine + (fset.Position(imp.Pos()).Line - 2)
		out = append(out, importSpecToDecl(imp, cellID, cellIndex, line))
	}
	return out, true
}

func importSpecToDecl(imp *ast.ImportSpec, cellID string, cellIndex, line int) ImportDecl {
	pathVal, _ := strconv.Unquote(imp.Path.Value)
	alias := ""
	pkgName := DefaultPackageName(pathVal)
	if imp.Name != nil {
		alias = imp.Name.Name
		if alias == "_" || alias == "." {
			pkgName = ""
		} else {
			pkgName = alias
		}
	}
	return ImportDecl{
		Alias:     alias,
		Path:      pathVal,
		PkgName:   pkgName,
		CellID:    cellID,
		CellIndex: cellIndex,
		Line:      line,
	}
}

func parseFuncChunk(chunkSrc, cellID string, cellIndex, startLine int) (*TopDecl, string, int, bool) {
	wrapped := "package plugo\n" + chunkSrc
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", wrapped, 0)
	if err != nil || file == nil || len(file.Decls) != 1 {
		// Check if it's an immediately-invoked anonymous function `func(...) { ... }(...)`.
		if isLikelyIIFE(chunkSrc) {
			return nil, "", 0, false
		}
		// If it starts with `func <ident>` (broken named func), keep at package level so
		// the Go compiler reports a clear diagnostic at the exact line.
		tks := scanTokens(chunkSrc)
		if len(tks) >= 2 && tks[1].tok == token.IDENT {
			return &TopDecl{
				Kind:      "func",
				Keys:      []string{fmt.Sprintf("raw:%s:%d", cellID, startLine)},
				Source:    chunkSrc,
				CellID:    cellID,
				CellIndex: cellIndex,
				StartLine: startLine,
			}, "", 0, true
		}
		return nil, "", 0, false
	}

	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		return nil, "", 0, false
	}

	if fn.Recv == nil && fn.Name.Name == "main" {
		if fn.Body == nil {
			return nil, "", 0, true
		}
		prefixLen := len("package plugo\n")
		lbrace := fset.Position(fn.Body.Lbrace)
		rbrace := fset.Position(fn.Body.Rbrace)
		startOff := lbrace.Offset - prefixLen + 1
		endOff := rbrace.Offset - prefixLen
		if startOff >= 0 && endOff >= startOff && endOff <= len(chunkSrc) {
			lbraceLine := startLine + (lbrace.Line - 2)
			body, bodyLine := extractBraceBody(chunkSrc[startOff:endOff], lbraceLine)
			return nil, body, bodyLine, true
		}
		return nil, "", 0, true
	}

	top := buildFuncTopDecl(fn, fset, chunkSrc, cellID, cellIndex, startLine)
	return &top, "", 0, true
}

func isLikelyIIFE(chunkSrc string) bool {
	trimmed := strings.TrimSpace(chunkSrc)
	return strings.HasPrefix(trimmed, "func(") || (strings.HasPrefix(trimmed, "func (") && strings.HasSuffix(trimmed, ")"))
}

func parseGenDeclChunk(chunkSrc, cellID string, cellIndex, startLine int) (TopDecl, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", "package plugo\n"+chunkSrc, 0)
	if err != nil || file == nil || len(file.Decls) != 1 {
		return TopDecl{}, false
	}
	gd, ok := file.Decls[0].(*ast.GenDecl)
	if !ok {
		return TopDecl{}, false
	}
	return buildGenTopDecl(gd, fset, chunkSrc, cellID, cellIndex, startLine, 1), true
}

func buildFuncTopDecl(fn *ast.FuncDecl, fset *token.FileSet, chunkSrc, cellID string, cellIndex, startLine int) TopDecl {
	kind := "func"
	name := fn.Name.Name
	key := "func:" + name

	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		kind = "method"
		recvType := receiverTypeName(fset, fn.Recv.List[0].Type)
		name = recvType + "." + fn.Name.Name
		key = "method:" + name
	} else if fn.Name.Name == "init" {
		kind = "init"
		key = fmt.Sprintf("init:%s:%d", cellID, startLine)
	}

	sig := formatFuncSignature(fset, fn)
	return TopDecl{
		Kind: kind,
		Keys: []string{key},
		Symbols: []notebook.Symbol{
			{
				Kind:      kind,
				Name:      name,
				Signature: sig,
				CellID:    cellID,
				CellIndex: cellIndex,
				Line:      startLine,
			},
		},
		Source:    chunkSrc,
		CellID:    cellID,
		CellIndex: cellIndex,
		StartLine: startLine,
	}
}

func buildGenTopDecl(gd *ast.GenDecl, fset *token.FileSet, chunkSrc, cellID string, cellIndex, startLine, lineOffset int) TopDecl {
	kind := strings.ToLower(gd.Tok.String())
	var keys []string
	var syms []notebook.Symbol

	for _, spec := range gd.Specs {
		specLine := startLine
		if fset != nil {
			specLine = startLine + (fset.Position(spec.Pos()).Line - 1 - lineOffset)
			if specLine < 1 {
				specLine = startLine
			}
		}
		switch s := spec.(type) {
		case *ast.TypeSpec:
			name := s.Name.Name
			if name != "_" {
				keys = append(keys, "type:"+name)
				syms = append(syms, notebook.Symbol{
					Kind:      "type",
					Name:      name,
					Signature: "type " + name + " " + shortTypeExpr(fset, s.Type),
					CellID:    cellID,
					CellIndex: cellIndex,
					Line:      specLine,
				})
			}
		case *ast.ValueSpec:
			for _, ident := range s.Names {
				if ident.Name == "_" {
					continue
				}
				keys = append(keys, "val:"+ident.Name)
				sig := kind + " " + ident.Name
				if s.Type != nil {
					sig += " " + exprString(fset, s.Type)
				}
				syms = append(syms, notebook.Symbol{
					Kind:      kind,
					Name:      ident.Name,
					Signature: sig,
					CellID:    cellID,
					CellIndex: cellIndex,
					Line:      specLine,
				})
			}
		}
	}
	if len(keys) == 0 {
		keys = []string{fmt.Sprintf("%s:%s:%d", kind, cellID, startLine)}
	}

	return TopDecl{
		Kind:      kind,
		Keys:      keys,
		Symbols:   syms,
		Source:    chunkSrc,
		CellID:    cellID,
		CellIndex: cellIndex,
		StartLine: startLine,
	}
}

func receiverTypeName(fset *token.FileSet, expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(fset, t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverTypeName(fset, t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(fset, t.X)
	default:
		return exprString(fset, expr)
	}
}

func formatFuncSignature(fset *token.FileSet, fn *ast.FuncDecl) string {
	shallow := *fn
	shallow.Body = nil
	shallow.Doc = nil
	if fset == nil {
		fset = token.NewFileSet()
	}
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, fset, &shallow)
	return strings.Join(strings.Fields(buf.String()), " ")
}

func shortTypeExpr(fset *token.FileSet, expr ast.Expr) string {
	switch expr.(type) {
	case *ast.StructType:
		return "struct { ... }"
	case *ast.InterfaceType:
		return "interface { ... }"
	default:
		s := strings.Join(strings.Fields(exprString(fset, expr)), " ")
		if len(s) > 60 {
			return s[:57] + "..."
		}
		return s
	}
}

func exprString(fset *token.FileSet, expr ast.Expr) string {
	if fset == nil {
		fset = token.NewFileSet()
	}
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, fset, expr)
	return buf.String()
}

func extractBraceBody(raw string, lbraceLine int) (string, int) {
	line := lbraceLine
	i := 0
	for i < len(raw) {
		ch := raw[i]
		if ch == '\n' {
			line++
			i++
			continue
		}
		if ch == ' ' || ch == '\t' {
			i++
			continue
		}
		break
	}
	// Back up to start of the first non-empty line so indentation is preserved.
	lineStart := i
	for lineStart > 0 && raw[lineStart-1] != '\n' {
		lineStart--
	}
	body := strings.TrimRight(raw[lineStart:], " \t\n")
	return body, line
}

// FormatCell formats a Go code cell using go/format, supporting both complete Go files
// and snippet-style Plugo cells with mixed declarations and statements.
func FormatCell(src string) (string, error) {
	normalized := strings.ReplaceAll(src, "\r\n", "\n")
	if strings.TrimSpace(normalized) == "" {
		return "", nil
	}

	tokens := scanTokens(normalized)
	if len(tokens) == 0 {
		return strings.TrimSpace(normalized) + "\n", nil
	}

	// 1. Full package file.
	if tokens[0].tok == token.PACKAGE {
		formatted, err := format.Source([]byte(normalized))
		if err != nil {
			return "", err
		}
		return string(formatted), nil
	}

	// 2. Try formatting as pure declarations.
	if out, err := formatWrappedDecls(normalized); err == nil {
		return out, nil
	}

	// 3. Try formatting as pure statements.
	if out, err := formatWrappedStmts(normalized); err == nil {
		return out, nil
	}

	// 4. Mixed declarations and statements: preserve exact chunk ordering & comments
	// by grouping contiguous decl vs stmt regions.
	chunks := splitTopChunks(tokens)
	type region struct {
		isDecl bool
		start  int
		end    int
	}

	var regions []region
	prevEnd := 0
	hasStmt := false

	for _, ch := range chunks {
		chunkSrc := strings.TrimRight(normalized[ch.startOffset:ch.endOffset], " \t\n;")
		isDecl := false
		switch ch.firstTok {
		case token.IMPORT:
			_, isDecl = parseImportChunk(chunkSrc, "", 0, ch.startLine)
		case token.TYPE, token.CONST:
			_, isDecl = parseGenDeclChunk(chunkSrc, "", 0, ch.startLine)
		case token.VAR:
			if !hasStmt {
				_, isDecl = parseGenDeclChunk(chunkSrc, "", 0, ch.startLine)
			}
		case token.FUNC:
			if d, body, _, handled := parseFuncChunk(chunkSrc, "", 0, ch.startLine); handled && d != nil && body == "" {
				isDecl = true
			}
		}
		if !isDecl {
			hasStmt = true
		}

		// Extend region end to the end of its line so trailing comments stay attached.
		endOff := ch.endOffset
		for endOff < len(normalized) && normalized[endOff] != '\n' {
			endOff++
		}
		if endOff < len(normalized) && normalized[endOff] == '\n' {
			endOff++
		}

		if len(regions) > 0 && regions[len(regions)-1].isDecl == isDecl {
			regions[len(regions)-1].end = endOff
		} else {
			regions = append(regions, region{
				isDecl: isDecl,
				start:  prevEnd,
				end:    endOff,
			})
		}
		prevEnd = endOff
	}
	if prevEnd < len(normalized) && len(regions) > 0 {
		regions[len(regions)-1].end = len(normalized)
	}

	var outParts []string
	for _, r := range regions {
		segment := strings.Trim(normalized[r.start:r.end], "\n")
		if strings.TrimSpace(segment) == "" {
			continue
		}
		var formatted string
		var err error
		if r.isDecl {
			formatted, err = formatWrappedDecls(segment)
		} else {
			formatted, err = formatWrappedStmts(segment)
		}
		if err != nil {
			return "", err
		}
		outParts = append(outParts, strings.TrimRight(formatted, "\n"))
	}

	return strings.Join(outParts, "\n\n") + "\n", nil
}

func formatWrappedDecls(src string) (string, error) {
	const prefix = "package plugofmt\n\n"
	formatted, err := format.Source([]byte(prefix + src + "\n"))
	if err != nil {
		return "", err
	}
	res := strings.TrimPrefix(string(formatted), "package plugofmt\n")
	res = strings.TrimLeft(res, "\n")
	return res, nil
}

func formatWrappedStmts(src string) (string, error) {
	const header = "package plugofmt\n\nfunc _plugo_fmt() {\n"
	const footer = "\n}\n"
	formatted, err := format.Source([]byte(header + src + footer))
	if err != nil {
		return "", err
	}
	s := string(formatted)
	idx := strings.Index(s, "func _plugo_fmt() {\n")
	if idx < 0 {
		return "", fmt.Errorf("unexpected format output")
	}
	body := s[idx+len("func _plugo_fmt() {\n"):]
	body = strings.TrimSuffix(body, "}\n")
	body = strings.TrimSuffix(body, "}")

	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "\t")
	}
	return strings.Join(lines, "\n") + "\n", nil
}
