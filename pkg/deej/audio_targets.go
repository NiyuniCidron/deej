package deej

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/godbus/dbus/v5"
	"github.com/omriharel/deej/pkg/deej/util"
)

// MprisInfo contains metadata about currently playing media via MPRIS
type MprisInfo struct {
	IsPlaying  bool   `json:"isPlaying"`
	Title      string `json:"title,omitempty"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
	PlayerName string `json:"playerName,omitempty"`
}

// AudioTarget represents an available audio target that can be assigned to a slider
type AudioTarget struct {
	Name        string     `json:"name"`
	DisplayName string     `json:"displayName"`
	Type        string     `json:"type"` // "special", "process", "device", "installed"
	Description string     `json:"description"`
	Category    string     `json:"category,omitempty"`
	Icon        string     `json:"icon,omitempty"`
	MprisInfo   *MprisInfo `json:"mprisInfo,omitempty"`
}

// AudioTargetEnumerator provides methods to enumerate available audio targets
type AudioTargetEnumerator interface {
	GetAvailableTargets() ([]AudioTarget, error)
}

// GetAvailableAudioTargets returns all available audio targets for the current platform
func (d *Deej) GetAvailableAudioTargets() ([]AudioTarget, error) {
	logger := d.logger.Named("audio_targets")

	var targets []AudioTarget

	// Add special targets that are always available
	specialTargets := []AudioTarget{
		{
			Name:        "master",
			DisplayName: "Master Volume",
			Type:        "special",
			Description: "Controls the master system volume",
		},
		{
			Name:        "mic",
			DisplayName: "Microphone",
			Type:        "special",
			Description: "Controls the microphone input level",
		},
		{
			Name:        "deej.unmapped",
			DisplayName: "Unmapped Applications",
			Type:        "special",
			Description: "Controls all applications not assigned to other sliders",
		},
	}

	// Add Windows-specific special targets
	if !util.Linux() {
		specialTargets = append(specialTargets, []AudioTarget{
			{
				Name:        "deej.current",
				DisplayName: "Currently Active App",
				Type:        "special",
				Description: "Controls the currently active/focused application",
			},
			{
				Name:        "system",
				DisplayName: "System Sounds",
				Type:        "special",
				Description: "Controls Windows system sounds volume",
			},
		}...)
	}

	targets = append(targets, specialTargets...)

	// Get running processes with audio sessions
	processTargets, err := d.getProcessAudioTargets()
	if err != nil {
		logger.Warnw("Failed to get process audio targets", "error", err)
	} else {
		targets = append(targets, processTargets...)
	}

	// Get audio device targets (Windows only for now)
	if !util.Linux() {
		deviceTargets, err := d.getDeviceAudioTargets()
		if err != nil {
			logger.Warnw("Failed to get device audio targets", "error", err)
		} else {
			targets = append(targets, deviceTargets...)
		}
	}

	// Add installed applications
	if util.Linux() {
		installed, err := getLinuxInstalledApps()
		if err != nil {
			logger.Warnw("Failed to get installed apps (Linux)", "error", err)
		} else {
			targets = append(targets, installed...)
		}
	} else {
		installed, err := getWindowsInstalledApps()
		if err != nil {
			logger.Warnw("Failed to get installed apps (Windows)", "error", err)
		} else {
			targets = append(targets, installed...)
		}
	}

	return targets, nil
}

// getProcessAudioTargets returns audio targets for running processes
func (d *Deej) getProcessAudioTargets() ([]AudioTarget, error) {
	var targets []AudioTarget

	// Get current sessions to find running processes
	sessions, err := d.sessions.sessionFinder.GetAllSessions()
	if err != nil {
		return nil, fmt.Errorf("get sessions: %w", err)
	}

	// Build MPRIS process name map and bus map
	mprisMap := getAllMprisPlayers()
	mprisBusMap := make(map[string]*MprisInfo)
	for k, v := range mprisMap {
		if strings.HasPrefix(k, "org.mpris.MediaPlayer2.") {
			mprisBusMap[k] = v
		}
	}

	matchedBusNames := make(map[string]bool)

	for _, session := range sessions {
		// Skip special sessions (master, mic, system, etc.)
		sessionKey := session.Key()
		if sessionKey == masterSessionName || sessionKey == systemSessionName || sessionKey == inputSessionName {
			continue
		}

		// Try to get all possible process names from session properties
		var processNames []string
		if pa, ok := session.(interface{ Key() string }); ok {
			processNames = append(processNames, strings.ToLower(pa.Key()))
		}
		if pa, ok := session.(*paSession); ok {
			if pa.processName != "" {
				processNames = append(processNames, strings.ToLower(pa.processName))
			}
		}

		// Try to match any process name to any MPRIS DesktopEntry
		var mprisInfo *MprisInfo
		var displayName string
		for _, name := range processNames {
			if info, ok := mprisMap[name]; ok {
				mprisInfo = info
				displayName = info.PlayerName
				// Mark all bus names for this info as matched
				for bus, i := range mprisBusMap {
					if i == info {
						matchedBusNames[bus] = true
					}
				}
				break
			}
		}

		if len(processNames) > 0 {
			displayName = processNames[0]
			displayName = strings.TrimSuffix(displayName, ".exe")
			displayName = cases.Title(language.English).String(strings.ToLower(displayName))
			displayName = strings.ReplaceAll(displayName, ".", " ")
		}

		targets = append(targets, AudioTarget{
			Name:        processNames[0],
			DisplayName: displayName,
			Type:        "process",
			Description: fmt.Sprintf("Running application: %s", processNames[0]),
			MprisInfo:   mprisInfo,
		})
	}

	// List unmatched MPRIS players (by bus name)
	for bus, info := range mprisBusMap {
		if matchedBusNames[bus] {
			continue
		}
		targets = append(targets, AudioTarget{
			Name:        bus,
			DisplayName: info.PlayerName,
			Type:        "mpris-unmatched",
			Description: "Unmatched MPRIS player (no audio session)",
			MprisInfo:   info,
		})
	}

	return targets, nil
}

// getMprisInfo attempts to get MPRIS metadata for a given process name
func getMprisInfo(processName string) *MprisInfo {
	if !util.Linux() {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil
	}
	defer conn.Close()

	var names []string
	call := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.ListNames", 0)
	if call.Err != nil {
		return nil
	}
	if err := call.Store(&names); err != nil {
		return nil
	}

	// Look for MPRIS players that might match this process
	for _, name := range names {
		if !strings.HasPrefix(name, "org.mpris.MediaPlayer2.") {
			continue
		}

		// Try to match the player name with the process name
		playerName := strings.TrimPrefix(name, "org.mpris.MediaPlayer2.")
		if !strings.Contains(strings.ToLower(playerName), strings.ToLower(processName)) &&
			!strings.Contains(strings.ToLower(processName), strings.ToLower(playerName)) {
			continue
		}

		// Get player identity
		obj := conn.Object(name, "/org/mpris/MediaPlayer2")
		var identity dbus.Variant
		err := obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2", "Identity").Store(&identity)
		if err != nil {
			continue
		}

		playerIdentity := playerName
		if s, ok := identity.Value().(string); ok && s != "" {
			playerIdentity = s
		}

		// Get playback status
		var playbackStatus dbus.Variant
		err = obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2.Player", "PlaybackStatus").Store(&playbackStatus)
		if err != nil {
			continue
		}

		status, ok := playbackStatus.Value().(string)
		if !ok {
			continue
		}

		isPlaying := status == "Playing"

		// Get metadata
		var metadata dbus.Variant
		err = obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2.Player", "Metadata").Store(&metadata)
		if err != nil {
			continue
		}

		metadataMap, ok := metadata.Value().(map[string]dbus.Variant)
		if !ok {
			continue
		}

		// Extract title, artist, album
		title := ""
		artist := ""
		album := ""

		if titleVar, exists := metadataMap["xesam:title"]; exists {
			if t, ok := titleVar.Value().(string); ok {
				title = t
			}
		}

		if artistVar, exists := metadataMap["xesam:artist"]; exists {
			if artists, ok := artistVar.Value().([]string); ok && len(artists) > 0 {
				artist = artists[0]
			}
		}

		if albumVar, exists := metadataMap["xesam:album"]; exists {
			if a, ok := albumVar.Value().(string); ok {
				album = a
			}
		}

		return &MprisInfo{
			IsPlaying:  isPlaying,
			Title:      title,
			Artist:     artist,
			Album:      album,
			PlayerName: playerIdentity,
		}
	}

	return nil
}

// getAllMprisPlayers returns a map of processName to MprisInfo for all active MPRIS players
func getAllMprisPlayers() map[string]*MprisInfo {
	mprisMap := make(map[string]*MprisInfo)
	mprisBusMap := make(map[string]*MprisInfo) // bus name -> MprisInfo
	if !util.Linux() {
		return mprisMap
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return mprisMap
	}
	defer conn.Close()

	var names []string
	call := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.ListNames", 0)
	if call.Err != nil {
		return mprisMap
	}
	if err := call.Store(&names); err != nil {
		return mprisMap
	}

	for _, name := range names {
		if !strings.HasPrefix(name, "org.mpris.MediaPlayer2.") {
			continue
		}
		obj := conn.Object(name, "/org/mpris/MediaPlayer2")

		var processName string
		// Try DesktopEntry, but don't require it
		var desktopEntry dbus.Variant
		err := obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2", "DesktopEntry").Store(&desktopEntry)
		if err == nil {
			if pn, ok := desktopEntry.Value().(string); ok && pn != "" {
				processName = strings.ToLower(pn)
			}
		}

		// Try to get process name from PID if DesktopEntry is not available
		if processName == "" {
			var uniqueName string
			call := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, name)
			if call.Err == nil {
				if err := call.Store(&uniqueName); err == nil && uniqueName != "" {
					var pid uint32
					pidCall := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetConnectionUnixProcessID", 0, uniqueName)
					if pidCall.Err == nil {
						if err := pidCall.Store(&pid); err == nil && pid > 0 {
							procName := getProcessNameFromPID(pid)
							if procName != "" && procName != "xdg-dbus-proxy" && procName != "bwrap" {
								processName = strings.ToLower(procName)
							}
						}
					}
				}
			}
		}

		// Get identity
		var identity dbus.Variant
		_ = obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2", "Identity").Store(&identity)
		playerIdentity := strings.TrimPrefix(name, "org.mpris.MediaPlayer2.")
		if s, ok := identity.Value().(string); ok && s != "" {
			playerIdentity = s
		}

		// Get playback status
		var playbackStatus dbus.Variant
		_ = obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2.Player", "PlaybackStatus").Store(&playbackStatus)
		status, _ := playbackStatus.Value().(string)
		isPlaying := status == "Playing"

		// Get metadata
		var metadata dbus.Variant
		_ = obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2.Player", "Metadata").Store(&metadata)
		metadataMap, _ := metadata.Value().(map[string]dbus.Variant)
		title := ""
		artist := ""
		album := ""
		if titleVar, exists := metadataMap["xesam:title"]; exists {
			if t, ok := titleVar.Value().(string); ok {
				title = t
			}
		}
		if artistVar, exists := metadataMap["xesam:artist"]; exists {
			if artists, ok := artistVar.Value().([]string); ok && len(artists) > 0 {
				artist = artists[0]
			}
		}
		if albumVar, exists := metadataMap["xesam:album"]; exists {
			if a, ok := albumVar.Value().(string); ok {
				album = a
			}
		}

		info := &MprisInfo{
			IsPlaying:  isPlaying,
			Title:      title,
			Artist:     artist,
			Album:      album,
			PlayerName: playerIdentity,
		}
		if processName != "" {
			mprisMap[processName] = info
		}
		mprisBusMap[name] = info
	}
	// Attach bus name map for unmatched listing
	mprisMap["__bus_map__"] = (*MprisInfo)(nil) // marker for getProcessAudioTargets
	return mprisMap
}

// getDeviceAudioTargets returns audio targets for audio devices (Windows only)
func (d *Deej) getDeviceAudioTargets() ([]AudioTarget, error) {
	var targets []AudioTarget

	// This would require additional implementation to enumerate audio devices
	// For now, we'll return an empty list and can implement this later
	// The existing session finder already has device enumeration code we can leverage

	return targets, nil
}

// getWindowsInstalledApps scans Start Menu for .lnk files and returns AudioTargets
func getWindowsInstalledApps() ([]AudioTarget, error) {
	var targets []AudioTarget
	// Common Start Menu locations
	startMenuDirs := []string{
		os.ExpandEnv("%APPDATA%\\Microsoft\\Windows\\Start Menu\\Programs"),
		"C:\\ProgramData\\Microsoft\\Windows\\Start Menu\\Programs",
	}
	seen := make(map[string]bool)
	for _, dir := range startMenuDirs {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() || filepath.Ext(info.Name()) != ".lnk" {
				return nil
			}
			// Use the shortcut name (without extension) as display name
			displayName := strings.TrimSuffix(info.Name(), ".lnk")
			// Use the parent folder as category
			category := filepath.Base(filepath.Dir(path))
			// Use the shortcut file name as the best guess for process name
			processName := displayName + ".exe"
			if seen[processName] {
				return nil
			}
			seen[processName] = true
			targets = append(targets, AudioTarget{
				Name:        processName,
				DisplayName: displayName,
				Type:        "installed",
				Description: "Installed application (may not be running)",
				Category:    category,
				Icon:        "", // Icon extraction can be implemented later
			})
			return nil
		})
		if err != nil {
			continue
		}
	}
	return targets, nil
}

// getLinuxInstalledApps scans .desktop files in standard locations and returns AudioTargets
func getLinuxInstalledApps() ([]AudioTarget, error) {
	var targets []AudioTarget
	seen := make(map[string]bool)

	dirs := []string{
		"/usr/share/applications",
		"/usr/local/share/applications",
		filepath.Join(os.Getenv("HOME"), ".local/share/applications"),
	}

	// .desktop files (existing)
	for _, dir := range dirs {
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".desktop") {
				return nil
			}

			name, exec, category := "", "", "Other"

			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "Name=") {
					name = strings.TrimPrefix(line, "Name=")
				} else if strings.HasPrefix(line, "Exec=") {
					exec = strings.TrimPrefix(line, "Exec=")
					if i := strings.IndexAny(exec, " %"); i > 0 {
						exec = exec[:i]
					}
					exec = filepath.Base(exec)
				} else if strings.HasPrefix(line, "Categories=") {
					cats := strings.Split(strings.TrimPrefix(line, "Categories="), ";")
					if len(cats) > 0 && cats[0] != "" {
						category = cats[0]
					}
				}
			}

			if name == "" || exec == "" {
				return nil
			}
			if seen[exec] {
				return nil
			}
			seen[exec] = true

			targets = append(targets, AudioTarget{
				Name:        exec,
				DisplayName: name,
				Type:        "installed",
				Description: "Installed application (may not be running)",
				Category:    category,
				Icon:        "",
			})
			return nil
		})
	}

	// Flatpak apps
	flatpakList, err := exec.Command("flatpak", "list", "--app", "--columns=application,name").Output()
	if err == nil {
		lines := strings.Split(string(flatpakList), "\n")
		for _, line := range lines {
			fields := strings.SplitN(line, "\t", 2)
			if len(fields) < 2 {
				continue
			}
			appID := fields[0]
			appName := fields[1]
			if appID == "" || seen[appID] {
				continue
			}
			// Fetch metadata
			infoOut, err := exec.Command("flatpak", "info", appID).Output()
			category, desc := "Flatpak", "Flatpak application"
			if err == nil {
				for _, l := range strings.Split(string(infoOut), "\n") {
					if strings.HasPrefix(l, "Categories:") {
						cats := strings.Split(strings.TrimSpace(strings.TrimPrefix(l, "Categories:")), ";")
						if len(cats) > 0 && cats[0] != "" {
							category = cats[0]
						}
					}
					if strings.HasPrefix(l, "Description:") {
						desc = strings.TrimSpace(strings.TrimPrefix(l, "Description:"))
					}
				}
			}
			seen[appID] = true
			targets = append(targets, AudioTarget{
				Name:        appID,
				DisplayName: appName,
				Type:        "installed",
				Description: desc,
				Category:    category,
				Icon:        "",
			})
		}
	}

	// Snap apps
	snapList, err := exec.Command("snap", "list").Output()
	if err == nil {
		lines := strings.Split(string(snapList), "\n")
		for i, line := range lines {
			if i == 0 || strings.TrimSpace(line) == "" { // skip header
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 1 {
				continue
			}
			name := fields[0]
			if name == "" || seen[name] {
				continue
			}
			// Fetch metadata
			infoOut, err := exec.Command("snap", "info", name).Output()
			desc, category, title := "Snap application", "Snap", name
			if err == nil {
				for _, l := range strings.Split(string(infoOut), "\n") {
					if strings.HasPrefix(l, "summary:") {
						desc = strings.TrimSpace(strings.TrimPrefix(l, "summary:"))
					}
					if strings.HasPrefix(l, "title:") {
						title = strings.TrimSpace(strings.TrimPrefix(l, "title:"))
					}
					if strings.HasPrefix(l, "category:") {
						category = strings.TrimSpace(strings.TrimPrefix(l, "category:"))
					}
				}
			}
			seen[name] = true
			targets = append(targets, AudioTarget{
				Name:        name,
				DisplayName: title,
				Type:        "installed",
				Description: desc,
				Category:    category,
				Icon:        "",
			})
		}
	}

	return targets, nil
}

// Add this helper function near getProcessNameFromPID
func getParentPID(pid uint32) uint32 {
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	data, err := os.ReadFile(statPath)
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 4 {
		return 0
	}
	ppid, err := strconv.Atoi(fields[3])
	if err != nil {
		return 0
	}
	return uint32(ppid)
}

// Add this helper function near getProcessNameFromPID and getParentPID
func getRealProcessNameFromPID(pid uint32) string {
	for i := 0; i < 5; i++ { // limit to 5 hops to avoid infinite loops
		name := getProcessNameFromPID(pid)
		if name != "xdg-dbus-proxy" && name != "bwrap" && name != "" {
			return strings.ToLower(name)
		}
		pid = getParentPID(pid)
		if pid == 0 {
			break
		}
	}
	return ""
}
