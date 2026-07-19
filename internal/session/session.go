package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/owainlewis/neo/internal/atomicfile"
	"github.com/owainlewis/neo/internal/llm"
)

const indexFile = "index.json"

var ErrNotFound = errors.New("session not found")

type Metadata struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	CWD   string `json:"cwd"`
	Model string `json:"model"`
	// Provider records which LLM backend produced this transcript. Transcripts
	// can carry provider-specific blocks, so resume logic uses this to decide
	// whether the saved model still applies.
	Provider  string    `json:"provider,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Session struct {
	Metadata Metadata      `json:"metadata"`
	Messages []llm.Message `json:"messages"`
	Usage    llm.Usage     `json:"usage"`
}

type SearchResult struct {
	Metadata Metadata
	Excerpt  string
}

type SearchWarning struct {
	ID  string
	Err error
}

// Store persists sessions as one JSON file each plus an index.json summary.
// Index mutations are read-modify-write with an atomic rename and no
// cross-process lock — like the auth store, this assumes a single interactive
// CLI per sessions directory; concurrent neo processes can lose index updates
// (session files themselves are never affected).
type Store struct {
	dir string
}

type index struct {
	Sessions []Metadata `json:"sessions"`
}

func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	if home == "" {
		return "", fmt.Errorf("home dir: empty")
	}
	return filepath.Join(home, ".neo", "sessions"), nil
}

func DefaultStore() (*Store, error) {
	dir, err := DefaultDir()
	if err != nil {
		return nil, err
	}
	return NewStore(dir), nil
}

func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) Dir() string { return s.dir }

func (s *Store) Create(ctx context.Context, meta Metadata) (*Session, error) {
	now := time.Now().UTC()
	if meta.ID == "" {
		id, err := newID()
		if err != nil {
			return nil, err
		}
		meta.ID = id
	}
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = now
	}
	if meta.UpdatedAt.IsZero() {
		meta.UpdatedAt = meta.CreatedAt
	}
	sess := &Session{Metadata: meta}
	if err := s.Save(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Store) Load(_ context.Context, id string) (*Session, error) {
	id, err := cleanID(id)
	if err != nil {
		return nil, fmt.Errorf("invalid session id %q", id)
	}
	b, err := os.ReadFile(s.sessionPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read session %s: %w", id, err)
	}
	var sess Session
	if err := json.Unmarshal(b, &sess); err != nil {
		return nil, fmt.Errorf("parse session %s: %w", id, err)
	}
	if sess.Metadata.ID == "" {
		sess.Metadata.ID = id
	}
	return &sess, nil
}

func (s *Store) Save(_ context.Context, sess *Session) error {
	if sess == nil {
		return fmt.Errorf("session: nil session")
	}
	if isBlankID(sess.Metadata.ID) {
		id, err := newID()
		if err != nil {
			return err
		}
		sess.Metadata.ID = id
	}
	if hasPathSeparator(sess.Metadata.ID) {
		return fmt.Errorf("invalid session id %q", sess.Metadata.ID)
	}
	now := time.Now().UTC()
	if sess.Metadata.CreatedAt.IsZero() {
		sess.Metadata.CreatedAt = now
	}
	sess.Metadata.UpdatedAt = now
	if sess.Metadata.Title == "" {
		sess.Metadata.Title = TitleFromMessages(sess.Messages)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}
	b, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session %s: %w", sess.Metadata.ID, err)
	}
	if err := writeJSONAtomic(s.sessionPath(sess.Metadata.ID), b); err != nil {
		return err
	}
	idx, err := s.readIndex()
	if err != nil {
		return err
	}
	upsertMetadata(&idx, sess.Metadata)
	return s.writeIndex(idx)
}

func (s *Store) List(_ context.Context) ([]Metadata, error) {
	idx, err := s.readIndex()
	if err != nil {
		return nil, err
	}
	items := append([]Metadata(nil), idx.Sessions...)
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items, nil
}

func (s *Store) Search(ctx context.Context, query string) ([]SearchResult, []SearchWarning, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil, fmt.Errorf("search query is empty")
	}
	items, err := s.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	needle := strings.ToLower(query)
	var results []SearchResult
	var warnings []SearchWarning
	for _, meta := range items {
		if err := ctx.Err(); err != nil {
			return results, warnings, err
		}
		sess, err := s.Load(ctx, meta.ID)
		if err != nil {
			warnings = append(warnings, SearchWarning{ID: meta.ID, Err: err})
			continue
		}
		text := transcriptText(sess.Messages)
		if idx := strings.Index(strings.ToLower(text), needle); idx >= 0 {
			results = append(results, SearchResult{
				Metadata: sess.Metadata,
				Excerpt:  matchingExcerpt(text, idx, len(query)),
			})
		}
	}
	return results, warnings, nil
}

func TitleFromMessages(messages []llm.Message) string {
	for _, msg := range messages {
		if msg.Role != llm.RoleUser {
			continue
		}
		for _, block := range msg.Content {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				return TitleFromText(block.Text)
			}
		}
	}
	return ""
}

func transcriptText(messages []llm.Message) string {
	var parts []string
	for _, msg := range messages {
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				parts = append(parts, block.Text)
			case "tool_result":
				parts = append(parts, block.Content)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func matchingExcerpt(text string, idx, queryLen int) string {
	const contextRunes = 48
	runes := []rune(text)
	byteToRune := 0
	for i := range text {
		if i >= idx {
			break
		}
		byteToRune++
	}
	queryRunes := len([]rune(text[idx:min(len(text), idx+queryLen)]))
	start := max(0, byteToRune-contextRunes)
	end := min(len(runes), byteToRune+queryRunes+contextRunes)
	excerpt := strings.Join(strings.Fields(string(runes[start:end])), " ")
	if start > 0 {
		excerpt = "..." + excerpt
	}
	if end < len(runes) {
		excerpt += "..."
	}
	return excerpt
}

func TitleFromText(text string) string {
	text = strings.TrimSpace(strings.Join(strings.FieldsFunc(text, unicode.IsSpace), " "))
	const max = 80
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return strings.TrimSpace(string(runes[:max-1])) + "…"
}

func (s *Store) sessionPath(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *Store) indexPath() string {
	return filepath.Join(s.dir, indexFile)
}

func (s *Store) readIndex() (index, error) {
	var idx index
	b, err := os.ReadFile(s.indexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return idx, nil
		}
		return idx, fmt.Errorf("read session index: %w", err)
	}
	if err := json.Unmarshal(b, &idx); err != nil {
		return idx, fmt.Errorf("parse session index: %w", err)
	}
	return idx, nil
}

func (s *Store) writeIndex(idx index) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session index: %w", err)
	}
	return writeJSONAtomic(s.indexPath(), b)
}

func upsertMetadata(idx *index, meta Metadata) {
	for i := range idx.Sessions {
		if idx.Sessions[i].ID == meta.ID {
			idx.Sessions[i] = meta
			return
		}
	}
	idx.Sessions = append(idx.Sessions, meta)
}

func writeJSONAtomic(path string, b []byte) error {
	// Sessions hold full transcripts (potentially sensitive file contents), so
	// they are written 0600 like the auth store.
	return atomicfile.Write(path, append(b, '\n'), 0o600, 0o755)
}

func cleanID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if isBlankID(id) || hasPathSeparator(id) {
		return id, fmt.Errorf("invalid")
	}
	return id, nil
}

func isBlankID(id string) bool {
	return strings.TrimSpace(id) == ""
}

func hasPathSeparator(id string) bool {
	return strings.ContainsAny(id, `/\\`)
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return "sess_" + hex.EncodeToString(b[:]), nil
}
