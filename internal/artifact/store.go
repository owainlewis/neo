package artifact

import (
	"fmt"
	"os"
	"path/filepath"
)

type Store struct {
	Root string
}

func NewStore(root string) *Store {
	if root == "" {
		root = ".agent/runs"
	}
	return &Store{Root: root}
}

func (s *Store) InitRun(runID string) error {
	return os.MkdirAll(filepath.Join(s.Root, runID), 0o755)
}

func (s *Store) WritePhase(runID, phase string, round int, output string) error {
	dir := filepath.Join(s.Root, runID, fmt.Sprintf("%s-round-%d", phase, round))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "output.md"), []byte(output), 0o644)
}

func (s *Store) RunDir(runID string) string {
	return filepath.Join(s.Root, runID)
}
