package deej

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/omriharel/deej/pkg/deej/util"
	"github.com/thoas/go-funk"
	"go.uber.org/zap"
)

type sessionMap struct {
	deej   *Deej
	logger *zap.SugaredLogger

	m    map[string][]Session
	lock sync.Locker

	sessionFinder SessionFinder

	lastSessionRefresh time.Time
	unmappedSessions   []Session
}

const (
	masterSessionName = "master" // master device volume
	systemSessionName = "system" // system sounds volume
	inputSessionName  = "mic"    // microphone input level

	// some targets need to be transformed before their correct audio sessions can be accessed.
	// this prefix identifies those targets to ensure they don't contradict with another similarly-named process
	specialTargetTransformPrefix = "deej."

	// targets the currently active window (Windows-only, experimental)
	specialTargetCurrentWindow = "current"

	// targets all currently unmapped sessions (experimental)
	specialTargetAllUnmapped = "unmapped"

	// this threshold constant assumes that re-acquiring all sessions is a kind of expensive operation,
	// and needs to be limited in some manner. this value was previously user-configurable through a config
	// key "process_refresh_frequency", but exposing this type of implementation detail seems wrong now
	minTimeBetweenSessionRefreshes = time.Second * 5
)

// this matches friendly device names (on Windows), e.g. "Headphones (Realtek Audio)"
var deviceSessionKeyPattern = regexp.MustCompile(`^.+ \(.+\)$`)

func newSessionMap(deej *Deej, logger *zap.SugaredLogger, sessionFinder SessionFinder) (*sessionMap, error) {
	logger = logger.Named("sessions")

	logger.Debug("Creating session map instance")

	m := &sessionMap{
		deej:          deej,
		logger:        logger,
		m:             make(map[string][]Session),
		lock:          &sync.Mutex{},
		sessionFinder: sessionFinder,
	}

	logger.Debug("Created session map instance")

	return m, nil
}

func (m *sessionMap) initialize() error {
	m.logger.Info("Initializing session map")

	if err := m.getAndAddSessions(); err != nil {
		m.logger.Warnw("Failed to get all sessions during session map initialization", "error", err)
		return fmt.Errorf("get all sessions during init: %w", err)
	}

	m.setupOnConfigReload()
	m.setupOnSliderMove()

	m.logger.Info("Session map initialization complete")
	return nil
}

func (m *sessionMap) release() error {
	if err := m.sessionFinder.Release(); err != nil {
		m.logger.Warnw("Failed to release session finder during session map release", "error", err)
		return fmt.Errorf("release session finder during release: %w", err)
	}

	return nil
}

func (m *sessionMap) getAndAddSessions() error {
	m.lastSessionRefresh = time.Now()
	m.unmappedSessions = nil

	sessions, err := m.sessionFinder.GetAllSessions()
	if err != nil {
		m.logger.Warnw("Failed to get sessions from session finder", "error", err)
		return fmt.Errorf("get sessions from SessionFinder: %w", err)
	}

	for _, session := range sessions {
		m.add(session)
		if !m.sessionMapped(session) {
			m.unmappedSessions = append(m.unmappedSessions, session)
		}
	}

	m.logger.Infow("Discovered audio sessions", "count", len(sessions))
	return nil
}

func (m *sessionMap) setupOnConfigReload() {
	configReloadedChannel := m.deej.config.SubscribeToChanges()
	go func() {
		for range configReloadedChannel {
			m.logger.Info("Config reloaded, refreshing audio sessions")
			m.refreshSessions(false)
		}
	}()
}

func (m *sessionMap) setupOnSliderMove() {
	m.logger.Debug("Setting up slider move event subscription")
	sliderEventsChannel := m.deej.serial.SubscribeToSliderMoveEvents()
	m.logger.Debug("Subscribed to slider move events")
	go func() {
		m.logger.Debug("Starting slider event processing loop")
		for event := range sliderEventsChannel {
			m.logger.Debugw("Received slider move event", "sliderID", event.SliderID, "percentValue", event.PercentValue)
			m.handleSliderMoveEvent(event)
		}
		m.logger.Debug("Slider event processing loop ended")
	}()
}

// performance: explain why force == true at every such use to avoid unintended forced refresh spams
func (m *sessionMap) refreshSessions(force bool) {

	// make sure enough time passed since the last refresh, unless force is true in which case always clear
	if !force && m.lastSessionRefresh.Add(minTimeBetweenSessionRefreshes).After(time.Now()) {
		return
	}

	// clear and release sessions first
	m.clear()

	if err := m.getAndAddSessions(); err != nil {
		m.logger.Warnw("Failed to re-acquire all audio sessions", "error", err)
	} else {
		m.logger.Debug("Re-acquired sessions successfully")
	}
}

// returns true if a session is not currently mapped to any slider, false otherwise
// special sessions (master, system, mic) and device-specific sessions always count as mapped,
// even when absent from the config. this makes sense for every current feature that uses "unmapped sessions"
func (m *sessionMap) sessionMapped(session Session) bool {

	// count master/system/mic as mapped
	if funk.ContainsString([]string{masterSessionName, systemSessionName, inputSessionName}, session.Key()) {
		return true
	}

	// count device sessions as mapped
	if deviceSessionKeyPattern.MatchString(session.Key()) {
		return true
	}

	matchFound := false

	// look through the actual mappings
	m.deej.config.SliderMapping.iterate(func(sliderIdx int, targets []string) {
		for _, target := range targets {

			// ignore special transforms
			if m.targetHasSpecialTransform(target) {
				continue
			}

			// safe to assume this has a single element because we made sure there's no special transform
			target = m.resolveTarget(target)[0]

			if target == session.Key() {
				matchFound = true
				return
			}
		}
	})

	return matchFound
}

func (m *sessionMap) handleSliderMoveEvent(event SliderMoveEvent) {
	m.logger.Debugw("Handling slider move event", "sliderID", event.SliderID, "percentValue", event.PercentValue)
	targets, ok := m.deej.config.SliderMapping.get(event.SliderID)
	if !ok {
		m.logger.Debugw("No targets mapped for slider", "sliderID", event.SliderID)
		return
	}

	m.logger.Debugw("Found targets for slider", "sliderID", event.SliderID, "targets", targets)
	for _, target := range targets {
		resolvedTargets := m.resolveTarget(target)
		m.logger.Debugw("Resolved target", "original", target, "resolved", resolvedTargets)
		for _, resolvedTarget := range resolvedTargets {
			sessions, ok := m.get(resolvedTarget)
			if !ok {
				m.logger.Debugw("No sessions found for target", "target", resolvedTarget)
				continue
			}
			m.logger.Debugw("Found sessions for target", "target", resolvedTarget, "sessionCount", len(sessions))
			for _, session := range sessions {
				go func(s Session, volume float32, target string) {
					if err := s.SetVolume(volume); err != nil {
						m.logger.Warnw("Failed to set session volume", "target", target, "error", err)
						go func() {
							time.Sleep(100 * time.Millisecond)
							m.refreshSessions(true)
						}()
					} else {
						m.logger.Debugw("Successfully set session volume", "target", target, "volume", volume)
					}
				}(session, event.PercentValue, resolvedTarget)
			}
		}
	}
}

func (m *sessionMap) targetHasSpecialTransform(target string) bool {
	return strings.HasPrefix(target, specialTargetTransformPrefix)
}

func (m *sessionMap) resolveTarget(target string) []string {

	// start by ignoring the case
	target = strings.ToLower(target)

	// look for any special targets first, by examining the prefix
	if m.targetHasSpecialTransform(target) {
		return m.applyTargetTransform(strings.TrimPrefix(target, specialTargetTransformPrefix))
	}

	return []string{target}
}

func (m *sessionMap) applyTargetTransform(specialTargetName string) []string {

	// select the transformation based on its name
	switch specialTargetName {

	// get current active window
	case specialTargetCurrentWindow:
		currentWindowProcessNames, err := util.GetCurrentWindowProcessNames()

		// silently ignore errors here, as this is on deej's "hot path" (and it could just mean the user's running linux)
		if err != nil {
			return nil
		}

		// we could have gotten a non-lowercase names from that, so let's ensure we return ones that are lowercase
		for targetIdx, target := range currentWindowProcessNames {
			currentWindowProcessNames[targetIdx] = strings.ToLower(target)
		}

		// remove dupes
		return funk.UniqString(currentWindowProcessNames)

	// get currently unmapped sessions
	case specialTargetAllUnmapped:
		targetKeys := make([]string, len(m.unmappedSessions))
		for sessionIdx, session := range m.unmappedSessions {
			targetKeys[sessionIdx] = session.Key()
		}

		return targetKeys
	}

	return nil
}

func (m *sessionMap) add(value Session) {
	m.logger.Debugw("About to add session to map", "session", value)

	m.logger.Debug("About to acquire lock")
	m.lock.Lock()
	m.logger.Debug("Lock acquired")
	defer m.lock.Unlock()

	key := value.Key()
	m.logger.Debugw("Session key", "key", key)

	existing, ok := m.m[key]
	if !ok {
		m.m[key] = []Session{value}
		m.logger.Debugw("Created new session list", "key", key)
	} else {
		m.m[key] = append(existing, value)
		m.logger.Debugw("Added to existing session list", "key", key, "count", len(m.m[key]))
	}
}

func (m *sessionMap) get(key string) ([]Session, bool) {
	m.lock.Lock()
	defer m.lock.Unlock()

	value, ok := m.m[key]
	return value, ok
}

func (m *sessionMap) clear() {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.logger.Debug("Releasing and clearing all audio sessions")

	for key, sessions := range m.m {
		for _, session := range sessions {
			session.Release()
		}

		delete(m.m, key)
	}

	m.logger.Debug("Session map cleared")
}

func (m *sessionMap) String() string {
	m.lock.Lock()
	defer m.lock.Unlock()

	sessionCount := 0

	for _, value := range m.m {
		sessionCount += len(value)
	}

	return fmt.Sprintf("<%d audio sessions>", sessionCount)
}
