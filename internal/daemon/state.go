package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"

	"firebox/internal/model"
)

type persistentState struct {
	Sandboxes map[string]model.Sandbox `json:"sandboxes"`
}

type State struct {
	mu        sync.RWMutex
	sandboxes map[string]model.Sandbox
	path      string
}

func LoadState(path string) (*State, error) {
	s := &State{sandboxes: make(map[string]model.Sandbox), path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read state db: %w", err)
	}
	if len(data) == 0 {
		return s, nil
	}
	var ps persistentState
	if err := json.Unmarshal(data, &ps); err != nil {
		return nil, fmt.Errorf("decode state db: %w", err)
	}
	if ps.Sandboxes != nil {
		s.sandboxes = ps.Sandboxes
	}
	return s, nil
}

func (s *State) Save() error {
	s.mu.RLock()
	ps := persistentState{Sandboxes: s.sandboxes}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state db: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return fmt.Errorf("write state db: %w", err)
	}
	return nil
}

func (s *State) PutSandbox(sb model.Sandbox) error {
	s.mu.Lock()
	s.sandboxes[sb.ID] = sb
	s.mu.Unlock()
	return s.Save()
}

func (s *State) GetSandbox(id string) (model.Sandbox, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sb, ok := s.sandboxes[id]
	return sb, ok
}

func (s *State) DeleteSandbox(id string) error {
	s.mu.Lock()
	delete(s.sandboxes, id)
	s.mu.Unlock()
	return s.Save()
}

func (s *State) ListSandboxes() []model.Sandbox {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]model.Sandbox, 0, len(s.sandboxes))
	for _, sb := range s.sandboxes {
		items = append(items, sb)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items
}
