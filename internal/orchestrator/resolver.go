package orchestrator

import (
	"fmt"
	"sync"

	"github.com/google/uuid"

	"nexios-finance/internal/openbanking"
)

type StaticResolver struct {
	mu          sync.RWMutex
	pisByName   map[string]*openbanking.PISService
	accountRefs map[uuid.UUID]string
}

func NewStaticResolver() *StaticResolver {
	return &StaticResolver{
		pisByName:   make(map[string]*openbanking.PISService),
		accountRefs: make(map[uuid.UUID]string),
	}
}

func (r *StaticResolver) RegisterProvider(name string, pis *openbanking.PISService) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pisByName[name] = pis
}

func (r *StaticResolver) RegisterAccountRef(sourceID uuid.UUID, accountRef string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accountRefs[sourceID] = accountRef
}

func (r *StaticResolver) ResolvePIS(provider string) (*openbanking.PISService, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	pis, ok := r.pisByName[provider]
	if !ok {
		return nil, fmt.Errorf("no connection configured for provider: %s", provider)
	}
	return pis, nil
}

func (r *StaticResolver) ResolveAccountRef(sourceID uuid.UUID) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ref, ok := r.accountRefs[sourceID]
	if !ok {
		return "", fmt.Errorf("no account reference registered for asset: %s", sourceID)
	}
	return ref, nil
}
