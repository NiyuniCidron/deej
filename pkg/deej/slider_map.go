package deej

import (
	"fmt"
	"strconv"
	"sync"
)

type sliderMap struct {
	m    map[int][]string
	lock sync.Locker
}

func newSliderMap() *sliderMap {
	return &sliderMap{
		m:    make(map[int][]string),
		lock: &sync.Mutex{},
	}
}

func sliderMapFromConfigs(userMapping map[string][]string, internalMapping map[string][]string) *sliderMap {
	resultMap := newSliderMap()

	// Helper function to convert string to int with caching
	sliderIdxCache := make(map[string]int)
	getSliderIdx := func(sliderIdxString string) int {
		if idx, exists := sliderIdxCache[sliderIdxString]; exists {
			return idx
		}
		idx, _ := strconv.Atoi(sliderIdxString)
		sliderIdxCache[sliderIdxString] = idx
		return idx
	}

	// copy targets from user config, ignoring empty values
	for sliderIdxString, targets := range userMapping {
		sliderIdx := getSliderIdx(sliderIdxString)

		// Filter out empty strings in a single pass
		filteredTargets := make([]string, 0, len(targets))
		for _, target := range targets {
			if target != "" {
				filteredTargets = append(filteredTargets, target)
			}
		}

		if len(filteredTargets) > 0 {
			resultMap.set(sliderIdx, filteredTargets)
		}
	}

	// add targets from internal configs, ignoring duplicate or empty values
	for sliderIdxString, targets := range internalMapping {
		sliderIdx := getSliderIdx(sliderIdxString)

		existingTargets, ok := resultMap.get(sliderIdx)
		if !ok {
			existingTargets = []string{}
		}

		// Filter and deduplicate in a single pass
		newTargets := make([]string, 0, len(targets))
		existingSet := make(map[string]bool)
		for _, target := range existingTargets {
			existingSet[target] = true
		}

		for _, target := range targets {
			if target != "" && !existingSet[target] {
				newTargets = append(newTargets, target)
				existingSet[target] = true
			}
		}

		if len(newTargets) > 0 {
			existingTargets = append(existingTargets, newTargets...)
			resultMap.set(sliderIdx, existingTargets)
		}
	}

	return resultMap
}

func (m *sliderMap) iterate(f func(int, []string)) {
	m.lock.Lock()
	defer m.lock.Unlock()

	for key, value := range m.m {
		f(key, value)
	}
}

func (m *sliderMap) get(key int) ([]string, bool) {
	m.lock.Lock()
	defer m.lock.Unlock()

	value, ok := m.m[key]
	return value, ok
}

func (m *sliderMap) set(key int, value []string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.m[key] = value
}

func (m *sliderMap) String() string {
	m.lock.Lock()
	defer m.lock.Unlock()

	sliderCount := 0
	targetCount := 0

	for _, value := range m.m {
		sliderCount++
		targetCount += len(value)
	}

	return fmt.Sprintf("<%d sliders mapped to %d targets>", sliderCount, targetCount)
}
