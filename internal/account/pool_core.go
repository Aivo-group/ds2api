package account

import (
	"sort"
	"sync"

	"ds2api/internal/config"
)

type Pool struct {
	store                  *config.Store
	mu                     sync.Mutex
	queue                  []string
	inUse                  map[string]int
	waiters                []chan struct{}
	maxInflightPerAccount  int
	recommendedConcurrency int
	maxQueueSize           int
	globalMaxInflight      int
	quarantined            map[string]struct{}
}

func NewPool(store *config.Store) *Pool {
	maxPer := 2
	if store != nil {
		maxPer = store.RuntimeAccountMaxInflight()
	}
	p := &Pool{
		store:                 store,
		inUse:                 map[string]int{},
		quarantined:           map[string]struct{}{},
		maxInflightPerAccount: maxPer,
	}
	p.Reset()
	return p
}

// Quarantine removes an account from new acquisitions while an explicit ban
// signal is independently confirmed. Existing holders can release normally.
func (p *Pool) Quarantine(accountID string) {
	if accountID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.quarantined[accountID] = struct{}{}
	p.notifyWaiterLocked()
}

func (p *Pool) Restore(accountID string) {
	if accountID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.quarantined, accountID)
	p.notifyWaiterLocked()
}

func (p *Pool) Remove(accountID string) {
	if accountID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := 0; i < len(p.queue); i++ {
		if p.queue[i] == accountID {
			p.queue = append(p.queue[:i], p.queue[i+1:]...)
			break
		}
	}
	p.quarantined[accountID] = struct{}{}
	p.notifyWaiterLocked()
}

func (p *Pool) Reset() {
	accounts := p.store.Accounts()
	sort.SliceStable(accounts, func(i, j int) bool {
		iHas := accounts[i].Token != ""
		jHas := accounts[j].Token != ""
		if iHas == jHas {
			return i < j
		}
		return iHas
	})
	ids := make([]string, 0, len(accounts))
	for _, a := range accounts {
		id := a.Identifier()
		if id != "" {
			ids = append(ids, id)
		}
	}
	if p.store != nil {
		p.maxInflightPerAccount = p.store.RuntimeAccountMaxInflight()
	} else {
		p.maxInflightPerAccount = maxInflightFromEnv()
	}
	recommended := defaultRecommendedConcurrency(len(ids), p.maxInflightPerAccount)
	queueLimit := maxQueueFromEnv(recommended)
	globalLimit := recommended
	if p.store != nil {
		queueLimit = p.store.RuntimeAccountMaxQueue(recommended)
		globalLimit = p.store.RuntimeGlobalMaxInflight(recommended)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drainWaitersLocked()
	p.queue = ids
	p.inUse = map[string]int{}
	p.recommendedConcurrency = recommended
	p.maxQueueSize = queueLimit
	p.globalMaxInflight = globalLimit
	config.Logger.Info(
		"[init_account_queue] initialized",
		"total", len(ids),
		"max_inflight_per_account", p.maxInflightPerAccount,
		"global_max_inflight", p.globalMaxInflight,
		"recommended_concurrency", p.recommendedConcurrency,
		"max_queue_size", p.maxQueueSize,
	)
}

func (p *Pool) Release(accountID string) {
	if accountID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	count := p.inUse[accountID]
	if count <= 0 {
		return
	}
	if count == 1 {
		delete(p.inUse, accountID)
		p.notifyWaiterLocked()
		return
	}
	p.inUse[accountID] = count - 1
	p.notifyWaiterLocked()
}

func (p *Pool) Status() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	available := make([]string, 0, len(p.queue))
	inUseAccounts := make([]string, 0, len(p.inUse))
	inUseSlots := 0
	for _, id := range p.queue {
		if _, quarantined := p.quarantined[id]; quarantined {
			continue
		}
		if p.inUse[id] < p.maxInflightPerAccount {
			available = append(available, id)
		}
	}
	for id, count := range p.inUse {
		if count > 0 {
			inUseAccounts = append(inUseAccounts, id)
			inUseSlots += count
		}
	}
	sort.Strings(inUseAccounts)
	return map[string]any{
		"available":                len(available),
		"in_use":                   inUseSlots,
		"total":                    len(p.store.Accounts()),
		"available_accounts":       available,
		"in_use_accounts":          inUseAccounts,
		"max_inflight_per_account": p.maxInflightPerAccount,
		"global_max_inflight":      p.globalMaxInflight,
		"recommended_concurrency":  p.recommendedConcurrency,
		"waiting":                  len(p.waiters),
		"max_queue_size":           p.maxQueueSize,
		"quarantined":              len(p.quarantined),
	}
}
