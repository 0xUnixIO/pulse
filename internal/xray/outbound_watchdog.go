package xray

import (
	"errors"
	"log"
	"strings"
	"time"
)

// SS 落地出口拨号失败达到阈值后强制 Recycle。
// 窗口/阈值对齐线上 V.PS→AT&T 抖动：短时连续 i/o timeout 后出口粘死数小时。
const (
	ssDialFailWindow    = 2 * time.Minute
	ssDialFailThreshold = 3
	ssRecycleCooldown   = 10 * time.Minute
)

func isSSOutboundDialFail(line string) bool {
	return strings.Contains(line, "proxy/shadowsocks_2022: failed to connect to server")
}

// noteSSDialFailLocked 记录一次 SS 出口拨号失败。返回是否应触发 Recycle。
// 必须在持有 m.mu 时调用。
func (m *Manager) noteSSDialFailLocked(now time.Time) bool {
	cut := now.Add(-ssDialFailWindow)
	dst := m.ssFailAt[:0]
	for _, t := range m.ssFailAt {
		if !t.Before(cut) {
			dst = append(dst, t)
		}
	}
	m.ssFailAt = append(dst, now)

	if m.recycleQueued {
		return false
	}
	if !m.lastRecycleAt.IsZero() && now.Sub(m.lastRecycleAt) < ssRecycleCooldown {
		return false
	}
	if len(m.ssFailAt) < ssDialFailThreshold {
		return false
	}
	m.recycleQueued = true
	return true
}

func (m *Manager) recycleFromWatchdog() {
	err := m.Recycle()
	m.mu.Lock()
	m.recycleQueued = false
	if err == nil {
		m.lastRecycleAt = time.Now()
		m.ssFailAt = nil
		m.appendLogLocked("xray recycle: shadowsocks outbound dial failures, graceful reload")
	} else if !errors.Is(err, ErrNotRunning) {
		// 失败也进冷却。否则 ssFailAt 还在窗口内，下一条拨号失败会立刻再开一轮。
		m.lastRecycleAt = time.Now()
	}
	m.mu.Unlock()
	if err != nil && !errors.Is(err, ErrNotRunning) {
		log.Printf("xray recycle: %v", err)
	}
}
