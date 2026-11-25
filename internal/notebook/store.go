package notebook

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const FileExtension = ".plugo"

var (
	ErrNotFound  = errors.New("notebook not found")
	ErrInvalidID = errors.New("invalid notebook id")
	validIDRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
	slugCleanRe  = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
)

// Store manages persistence of .plugo notebooks in a directory on disk.
type Store struct {
	mu  sync.RWMutex
	dir string
}

// NewStore creates a Store backed by dir, creating dir if needed.
func NewStore(dir string) (*Store, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve notebook dir: %w", err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, fmt.Errorf("create notebook dir: %w", err)
	}
	return &Store{dir: absDir}, nil
}

// Dir returns the absolute directory storing notebook files.
func (s *Store) Dir() string {
	return s.dir
}

// EnsureStarterNotebook creates a sample welcome notebook if the store is empty.
func (s *Store) EnsureStarterNotebook() (*Notebook, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	if len(list) > 0 {
		return s.Get(list[0].ID)
	}
	nb := StarterNotebook()
	if err := s.Save(nb); err != nil {
		return nil, err
	}
	return nb, nil
}

// List returns summaries of all notebooks in the store, newest first.
func (s *Store) List() ([]Summary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read notebook dir: %w", err)
	}

	summaries := make([]Summary, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), FileExtension) {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), FileExtension)
		if !validIDRegex.MatchString(id) {
			continue
		}
		nb, err := s.readFileLocked(id)
		if err != nil {
			continue
		}
		summaries = append(summaries, Summary{
			ID:        nb.ID,
			Title:     nb.Title,
			UpdatedAt: nb.UpdatedAt,
			CellCount: len(nb.Cells),
		})
	}

	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].UpdatedAt.Equal(summaries[j].UpdatedAt) {
			return summaries[i].ID < summaries[j].ID
		}
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})
	return summaries, nil
}

// Get loads a notebook by its ID.
func (s *Store) Get(id string) (*Notebook, error) {
	if !validIDRegex.MatchString(id) {
		return nil, ErrInvalidID
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readFileLocked(id)
}

// Create creates and persists a new notebook with a starter Go cell.
func (s *Store) Create(title string) (*Notebook, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled Notebook"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.uniqueIDLocked(slugify(title))
	now := time.Now().UTC()
	nb := &Notebook{
		ID:        id,
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
		Cells: []Cell{
			{
				ID:     NewCellID(),
				Type:   CellTypeMarkdown,
				Source: fmt.Sprintf("# %s\n\nWrite notes in **Markdown** and run native Go in the cells below.", title),
			},
			{
				ID:     NewCellID(),
				Type:   CellTypeCode,
				Source: "fmt.Println(\"Hello from Plugo!\")\n",
			},
		},
	}
	if err := s.writeFileLocked(nb); err != nil {
		return nil, err
	}
	return nb, nil
}

// Save validates and atomically writes a notebook to disk.
func (s *Store) Save(nb *Notebook) error {
	if nb == nil || !validIDRegex.MatchString(nb.ID) {
		return ErrInvalidID
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if nb.CreatedAt.IsZero() {
		if existing, err := s.readFileLocked(nb.ID); err == nil {
			nb.CreatedAt = existing.CreatedAt
		} else {
			nb.CreatedAt = now
		}
	}
	nb.UpdatedAt = now
	if strings.TrimSpace(nb.Title) == "" {
		nb.Title = nb.ID
	}
	if nb.Cells == nil {
		nb.Cells = []Cell{}
	}
	for i := range nb.Cells {
		if nb.Cells[i].ID == "" {
			nb.Cells[i].ID = NewCellID()
		}
		if nb.Cells[i].Type != CellTypeCode && nb.Cells[i].Type != CellTypeMarkdown {
			nb.Cells[i].Type = CellTypeCode
		}
	}
	return s.writeFileLocked(nb)
}

// Delete removes a notebook file from disk.
func (s *Store) Delete(id string) error {
	if !validIDRegex.MatchString(id) {
		return ErrInvalidID
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	err := os.Remove(s.pathFor(id))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

func (s *Store) pathFor(id string) string {
	return filepath.Join(s.dir, id+FileExtension)
}

func (s *Store) readFileLocked(id string) (*Notebook, error) {
	data, err := os.ReadFile(s.pathFor(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var nb Notebook
	if err := json.Unmarshal(data, &nb); err != nil {
		return nil, fmt.Errorf("decode notebook %q: %w", id, err)
	}
	nb.ID = id
	if nb.Cells == nil {
		nb.Cells = []Cell{}
	}
	return &nb, nil
}

func (s *Store) writeFileLocked(nb *Notebook) error {
	data, err := json.MarshalIndent(nb, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	target := s.pathFor(nb.ID)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		// On Windows, fallback to direct write if rename across an open handle fails.
		_ = os.Remove(tmp)
		return os.WriteFile(target, data, 0o644)
	}
	return nil
}

func (s *Store) uniqueIDLocked(base string) string {
	if base == "" {
		base = "notebook"
	}
	candidate := base
	for i := 2; ; i++ {
		if _, err := os.Stat(s.pathFor(candidate)); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugCleanRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-_")
	if len(s) > 48 {
		s = s[:48]
	}
	if s == "" || !validIDRegex.MatchString(s) {
		return "notebook"
	}
	return s
}

// NewCellID generates a short random identifier suitable for Go filenames and JSON keys.
func NewCellID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "c" + hex.EncodeToString(b[:])
}

// StarterNotebook returns a welcoming interactive notebook showcasing Plugo's Go-native workflow.
func StarterNotebook() *Notebook {
	now := time.Now().UTC()
	return &Notebook{
		ID:        "welcome",
		Title:     "Welcome to Plugo",
		CreatedAt: now,
		UpdatedAt: now,
		Cells: []Cell{
			{
				ID:   "cwelcome1",
				Type: CellTypeMarkdown,
				Source: `# Welcome to Plugo

**Plugo** is a lightweight, Go-native notebook. Every notebook combines **Markdown** cells with **compiled Go** cells powered directly by your local Go toolchain.

### What makes Plugo Go-native?
- **Zero boilerplate**: write Go statements directly — no need to wrap every cell in ` + "`package main`" + ` and ` + "`func main()`" + `.
- **Shared package scope**: ` + "`type`" + `, ` + "`func`" + `, ` + "`const`" + `, ` + "`var`" + `, and ` + "`import`" + ` declarations defined in earlier cells are automatically shared with cells below.
- **Smart stdlib imports**: use standard packages like ` + "`fmt`" + `, ` + "`strings`" + `, ` + "`slices`" + `, or ` + "`time`" + ` right away — Plugo resolves them automatically and prunes unused imports.
- **Native tooling**: format any cell with ` + "`gofmt`" + ` (` + "`Alt+Shift+F`" + `), inspect the generated Go source, or export the whole notebook to a single ` + "`.go`" + ` file.

> Press **` + "`Shift+Enter`" + `** inside any cell to run Go code or render Markdown.`,
			},
			{
				ID:   "cwelcome2",
				Type: CellTypeCode,
				Source: `// Declarations here become part of the shared notebook package.
const AppName = "Plugo"

type Gopher struct {
	Name   string
	Skills []string
}

func (g Gopher) Introduce() string {
	return fmt.Sprintf("Hi, I'm %s! I love %s.", g.Name, strings.Join(g.Skills, ", "))
}

g := Gopher{
	Name:   "Gophy",
	Skills: []string{"goroutines", "channels", "fast compiles"},
}
fmt.Printf("[%s] %s\n", AppName, g.Introduce())
`,
			},
			{
				ID:   "cwelcome3",
				Type: CellTypeMarkdown,
				Source: `## Reusing Shared Declarations & Concurrency

Because **Plugo** compiles with the real Go compiler, you get full generics, goroutines, channels, and standard library support. The cell below reuses ` + "`Gopher`" + ` and ` + "`AppName`" + ` from above:`,
			},
			{
				ID:   "cwelcome4",
				Type: CellTypeCode,
				Source: `team := []Gopher{
	{Name: "Ada", Skills: []string{"generics", "slices"}},
	{Name: "Ken", Skills: []string{"compilers", "simplicity"}},
	{Name: "Rob", Skills: []string{"concurrency", "interfaces"}},
}

results := make(chan string, len(team))
var wg sync.WaitGroup

for _, member := range team {
	wg.Add(1)
	go func(g Gopher) {
		defer wg.Done()
		results <- g.Introduce()
	}(member)
}

wg.Wait()
close(results)

lines := make([]string, 0, len(team))
for msg := range results {
	lines = append(lines, msg)
}
slices.Sort(lines)

fmt.Printf("=== %s Team (%d members) ===\n", AppName, len(lines))
for _, line := range lines {
	fmt.Println("•", line)
}
`,
			},
		},
	}
}
