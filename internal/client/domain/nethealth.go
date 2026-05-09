package domain

import (
	"sync"
	"time"
)

// NetHealthTracker отслеживает последовательные сетевые ошибки для
// автоматического пересоздания HTTP Transport (§6c). Общий для commgui
// и commclient — живёт в domain, привязан к Client.
type NetHealthTracker struct {
	mu               sync.Mutex
	consecutiveFails int
	firstFailAt      time.Time
	lastRebuildAt    time.Time
	deferredRebuild  bool
}

const (
	netFailWindow      = 30 * time.Second
	netFailThreshold   = 3
	netRebuildCooldown = 60 * time.Second
)

// RecordFailure регистрирует сетевую ошибку. Возвращает true, если набрался
// threshold и rebuild не в cooldown — вызывающий должен выполнить
// RebuildTransport (или отложить через DeferRebuild, если активен звонок).
func (t *NetHealthTracker) RecordFailure() (shouldRebuild bool) {
	t.mu.Lock()
	now := time.Now()
	if !t.firstFailAt.IsZero() && now.Sub(t.firstFailAt) > netFailWindow {
		t.consecutiveFails = 0
		t.firstFailAt = time.Time{}
	}
	if t.consecutiveFails == 0 {
		t.firstFailAt = now
	}
	t.consecutiveFails++
	fails := t.consecutiveFails
	cooldownActive := now.Sub(t.lastRebuildAt) < netRebuildCooldown
	t.mu.Unlock()

	return fails >= netFailThreshold && !cooldownActive
}

// RecordSuccess сбрасывает счётчик ошибок (например, после успешного
// переподключения listener'а или push-канала).
func (t *NetHealthTracker) RecordSuccess() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.consecutiveFails = 0
	t.firstFailAt = time.Time{}
}

// DeferRebuild откладывает rebuild до конца текущего звонка. Вызывается,
// когда RecordFailure вернула true, но активный звонок не позволяет
// пересоздать Transport немедленно.
func (t *NetHealthTracker) DeferRebuild() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.deferredRebuild = true
}

// HandleCallClosed вызывается при закрытии звонка. Если failureReason
// указывает на обрыв сигнального соединения — регистрирует ошибку.
// Возвращает true, если был отложенный rebuild и его нужно выполнить.
func (t *NetHealthTracker) HandleCallClosed(failureReason string) (shouldRebuild bool) {
	if failureReason == "signal_lost" {
		_ = t.RecordFailure() // учитываем, даже если threshold ещё не набран
	}

	t.mu.Lock()
	deferred := t.deferredRebuild
	t.deferredRebuild = false
	t.mu.Unlock()

	return deferred
}

// MarkRebuilt сбрасывает счётчик ошибок и фиксирует время последнего
// rebuild'а. Вызывается после успешного выполнения RebuildTransport.
func (t *NetHealthTracker) MarkRebuilt() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastRebuildAt = time.Now()
	t.consecutiveFails = 0
	t.firstFailAt = time.Time{}
	t.deferredRebuild = false
}
