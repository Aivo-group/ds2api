package account

import (
	"sort"
	"sync"
	"time"

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
	cooldowns              map[string]time.Time
	rateLimitEvents        uint64
	bannedRemovals         uint64
}

type Stats struct {
	Total                int
	Healthy              int
	Available            int
	InUse                int
	Cooldown             int
	Quarantined          int
	Waiting              int
	RateLimitEventsTotal uint64
	BannedRemovalsTotal  uint64
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
		cooldowns:             map[string]time.Time{},
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

// Cooldown temporarily removes an account from new acquisitions. Repeated
// rate limits extend an existing cooldown but never shorten it.
func (p *Pool) Cooldown(accountID string, duration time.Duration) time.Time {
	if accountID == "" {
		return time.Time{}
	}
	if duration <= 0 {
		duration = time.Minute
	}
	if duration > 24*time.Hour {
		duration = 24 * time.Hour
	}
	until := time.Now().Add(duration)
	p.mu.Lock()
	if current := p.cooldowns[accountID]; current.After(until) {
		until = current
	} else {
		p.cooldowns[accountID] = until
		p.rateLimitEvents++
	}
	p.notifyWaiterLocked()
	p.mu.Unlock()

	time.AfterFunc(time.Until(until), func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if current, ok := p.cooldowns[accountID]; ok && !current.After(time.Now()) {
			delete(p.cooldowns, accountID)
			p.notifyWaiterLocked()
		}
	})
	return until
}

func (p *Pool) Remove(accountID string) {
	p.remove(accountID, false)
}

func (p *Pool) RemoveBanned(accountID string) {
	p.remove(accountID, true)
}

func (p *Pool) remove(accountID string, banned bool) {
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
	delete(p.cooldowns, accountID)
	if banned {
		p.bannedRemovals++
	}
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
	p.expireCooldownsLocked(time.Now())
	available := make([]string, 0, len(p.queue))
	inUseAccounts := make([]string, 0, len(p.inUse))
	cooldownAccounts := make([]string, 0, len(p.cooldowns))
	cooldownUntil := make(map[string]string, len(p.cooldowns))
	inUseSlots := 0
	healthy := 0
	quarantinedCount := 0
	for _, id := range p.queue {
		if _, isQuarantined := p.quarantined[id]; isQuarantined {
			quarantinedCount++
			continue
		}
		if _, coolingDown := p.cooldowns[id]; coolingDown {
			continue
		}
		healthy++
		if p.inUse[id] < p.maxInflightPerAccount {
			available = append(available, id)
		}
	}
	for id := range p.cooldowns {
		cooldownAccounts = append(cooldownAccounts, id)
		cooldownUntil[id] = p.cooldowns[id].UTC().Format(time.RFC3339)
	}
	for id, count := range p.inUse {
		if count > 0 {
			inUseAccounts = append(inUseAccounts, id)
			inUseSlots += count
		}
	}
	sort.Strings(inUseAccounts)
	sort.Strings(cooldownAccounts)
	return map[string]any{
		"available":                len(available),
		"healthy":                  healthy,
		"in_use":                   inUseSlots,
		"total":                    len(p.store.Accounts()),
		"available_accounts":       available,
		"in_use_accounts":          inUseAccounts,
		"max_inflight_per_account": p.maxInflightPerAccount,
		"global_max_inflight":      p.globalMaxInflight,
		"recommended_concurrency":  p.recommendedConcurrency,
		"waiting":                  len(p.waiters),
		"max_queue_size":           p.maxQueueSize,
		"quarantined":              quarantinedCount,
		"cooldown":                 len(p.cooldowns),
		"cooldown_accounts":        cooldownAccounts,
		"cooldown_until":           cooldownUntil,
		"rate_limit_events_total":  p.rateLimitEvents,
		"banned_removals_total":    p.bannedRemovals,
	}
}

func (p *Pool) Stats() Stats {
	status := p.Status()
	return Stats{
		Total:                status["total"].(int),
		Healthy:              status["healthy"].(int),
		Available:            status["available"].(int),
		InUse:                status["in_use"].(int),
		Cooldown:             status["cooldown"].(int),
		Quarantined:          status["quarantined"].(int),
		Waiting:              status["waiting"].(int),
		RateLimitEventsTotal: status["rate_limit_events_total"].(uint64),
		BannedRemovalsTotal:  status["banned_removals_total"].(uint64),
	}
}

func (p *Pool) expireCooldownsLocked(now time.Time) {
	for accountID, until := range p.cooldowns {
		if !until.After(now) {
			delete(p.cooldowns, accountID)
		}
	}
}
