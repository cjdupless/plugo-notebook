package notebook

import (
	"errors"
	"testing"
)

func TestStoreCRUDAndStarter(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	starter, err := store.EnsureStarterNotebook()
	if err != nil {
		t.Fatalf("EnsureStarterNotebook: %v", err)
	}
	if starter.ID != "welcome" || len(starter.Cells) < 4 {
		t.Fatalf("unexpected starter notebook: id=%q cells=%d", starter.ID, len(starter.Cells))
	}

	created, err := store.Create("My Experiment")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID != "my-experiment" {
		t.Fatalf("expected slug id my-experiment, got %q", created.ID)
	}

	// Duplicate title gets unique suffixed ID.
	dup, err := store.Create("My Experiment")
	if err != nil {
		t.Fatalf("Create duplicate: %v", err)
	}
	if dup.ID != "my-experiment-2" {
		t.Fatalf("expected my-experiment-2, got %q", dup.ID)
	}

	// Path traversal ID must be rejected.
	if _, err := store.Get("../secret"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("expected ErrInvalidID for traversal id, got %v", err)
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 notebooks, got %d", len(list))
	}

	if err := store.Delete(dup.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(dup.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
