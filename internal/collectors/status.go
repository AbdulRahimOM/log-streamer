package collectors

import (
	"sort"
	"sync"

	"github.com/abdulrahimom/logstreamer/internal/config"
)

// SourceStatus is the safe operational state exposed by /api/services.
type SourceStatus struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	State     string `json:"state"`
	LastError string `json:"last_error,omitempty"`
}

type Registry struct {
	mu       sync.RWMutex
	statuses map[string]SourceStatus
}

func NewRegistry(sources []config.SourceConfig) *Registry {
	r := &Registry{statuses: make(map[string]SourceStatus, len(sources))}
	for _, source := range sources {
		r.statuses[source.Name] = SourceStatus{Name: source.Name, Type: source.Type, State: "configured"}
	}
	return r
}

func (r *Registry) set(name, state, safeError string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	status, ok := r.statuses[name]
	if !ok {
		return
	}
	status.State = state
	status.LastError = safeError
	r.statuses[name] = status
}

func (r *Registry) Snapshot() []SourceStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SourceStatus, 0, len(r.statuses))
	for _, status := range r.statuses {
		out = append(out, status)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
