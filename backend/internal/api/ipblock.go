package api

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)


type BlockedEntry struct {
	IP        string    `json:"ip"`
	BlockedAt time.Time `json:"blocked_at"`
}

type ipBlocker struct {
	mu        sync.RWMutex
	blocked   map[string]time.Time // ip → blocked at
	failures  map[string]int       // ip → failure count
	storePath string
}

func newIPBlocker(dataDir string) *ipBlocker {
	b := &ipBlocker{
		blocked:   make(map[string]time.Time),
		failures:  make(map[string]int),
		storePath: filepath.Join(dataDir, "blocked_ips.json"),
	}
	b.load()
	return b
}

func (b *ipBlocker) isBlocked(ip string) bool {
	b.syncFromFile()
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.blocked[ip]
	return ok
}

// syncFromFile 은 파일을 읽어 파싱에 성공하면 인메모리를 교체하고,
// 실패하면 현재 인메모리 데이터를 파일에 덤프한다.
func (b *ipBlocker) syncFromFile() {
	data, err := os.ReadFile(b.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			// 파일 삭제 = 차단 목록 초기화
			b.mu.Lock()
			b.blocked = make(map[string]time.Time)
			b.mu.Unlock()
		} else {
			// 읽기 실패 → 인메모리를 덤프
			b.mu.Lock()
			b.save()
			b.mu.Unlock()
		}
		return
	}
	var entries []BlockedEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		// 파싱 실패 → 인메모리를 덤프
		b.mu.Lock()
		b.save()
		b.mu.Unlock()
		return
	}
	// 파싱 성공 → 인메모리 교체 (failures 는 유지)
	newBlocked := make(map[string]time.Time, len(entries))
	for _, e := range entries {
		newBlocked[e.IP] = e.BlockedAt
	}
	b.mu.Lock()
	b.blocked = newBlocked
	b.mu.Unlock()
}

// recordFailure 는 실패 횟수를 기록하고, 10회 이상이면 자동 차단 후 true 를 반환한다.
func (b *ipBlocker) recordFailure(ip string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures[ip]++
	if b.failures[ip] >= 10 {
		b.blocked[ip] = time.Now()
		delete(b.failures, ip)
		b.save()
		return true
	}
	return false
}

func (b *ipBlocker) resetFailures(ip string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.failures, ip)
}

func (b *ipBlocker) block(ip string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.blocked[ip] = time.Now()
	delete(b.failures, ip)
	b.save()
}

func (b *ipBlocker) unblock(ip string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.blocked, ip)
	b.save()
}

func (b *ipBlocker) list() []BlockedEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]BlockedEntry, 0, len(b.blocked))
	for ip, t := range b.blocked {
		result = append(result, BlockedEntry{IP: ip, BlockedAt: t})
	}
	return result
}

func (b *ipBlocker) save() {
	entries := make([]BlockedEntry, 0, len(b.blocked))
	for ip, t := range b.blocked {
		entries = append(entries, BlockedEntry{IP: ip, BlockedAt: t})
	}
	data, _ := json.Marshal(entries)
	os.WriteFile(b.storePath, data, 0600)
}

func (b *ipBlocker) load() {
	data, err := os.ReadFile(b.storePath)
	if err != nil {
		return
	}
	var entries []BlockedEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return
	}
	for _, e := range entries {
		b.blocked[e.IP] = e.BlockedAt
	}
}

func extractClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
