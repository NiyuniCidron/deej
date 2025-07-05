package deej

import (
	//"github.com/getlantern/systray"
	"os"

	"fyne.io/systray"
	"github.com/omriharel/deej/pkg/deej/icon"
	"github.com/omriharel/deej/pkg/deej/util"
)

// ThemeType represents the system theme
type ThemeType int

const (
	ThemeDark ThemeType = iota
	ThemeLight
)

// TrayState represents the tray icon state
type TrayState int

const (
	TrayNormal TrayState = iota
	TrayError
)

// DetectSystemTheme attempts to detect the system theme on Linux
func DetectSystemTheme() ThemeType {
	// Check GTK theme
	if gtkTheme := os.Getenv("GTK_THEME"); gtkTheme != "" {
		if isLightTheme(gtkTheme) {
			return ThemeLight
		}
		return ThemeDark
	}
	// Check common desktop environment variables
	if xdgTheme := os.Getenv("XDG_CURRENT_DESKTOP"); xdgTheme != "" {
		if isLightTheme(xdgTheme) {
			return ThemeLight
		}
		return ThemeDark
	}
	// Fallback to dark
	return ThemeDark
}

func isLightTheme(theme string) bool {
	// crude check for common light theme names
	lightNames := []string{"light", "adwaita", "breeze-light", "yaru-light"}
	for _, name := range lightNames {
		if containsIgnoreCase(theme, name) {
			return true
		}
	}
	return false
}

func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > len(substr) && (containsIgnoreCase(s[1:], substr) || containsIgnoreCase(s, substr[1:]))) ||
		len(s) > 0 && len(substr) > 0 && (s[0]|32) == (substr[0]|32) && containsIgnoreCase(s[1:], substr[1:])
}

// SetTrayIcon sets the tray icon based on state and theme
func (d *Deej) SetTrayIcon(state TrayState, theme ThemeType) {
	switch state {
	case TrayNormal:
		switch theme {
		case ThemeLight:
			systray.SetIcon(icon.NormalLightIcon)
		default:
			systray.SetIcon(icon.NormalDarkIcon)
		}
	case TrayError:
		switch theme {
		case ThemeLight:
			systray.SetIcon(icon.ErrorLightIcon)
		default:
			systray.SetIcon(icon.ErrorDarkIcon)
		}
	}
}

func (d *Deej) initializeTray(onDone func()) {
	logger := d.logger.Named("tray")

	theme := DetectSystemTheme()
	d.SetTrayIcon(TrayNormal, theme)

	onReady := func() {
		logger.Debug("Tray instance ready")

		// Set the initial tray icon based on theme instead of hardcoded DeejLogo
		switch theme {
		case ThemeLight:
			systray.SetIcon(icon.NormalLightIcon)
		default:
			systray.SetIcon(icon.NormalDarkIcon)
		}
		systray.SetTitle("deej")
		systray.SetTooltip("deej")

		editConfig := systray.AddMenuItem("Edit configuration", "Open config file with notepad")
		editConfig.SetIcon(icon.EditConfig)

		refreshSessions := systray.AddMenuItem("Re-scan audio sessions", "Manually refresh audio sessions if something's stuck")
		refreshSessions.SetIcon(icon.RefreshSessions)

		if d.version != "" {
			systray.AddSeparator()
			versionInfo := systray.AddMenuItem(d.version, "")
			versionInfo.Disable()
		}

		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit", "Stop deej and quit")

		// wait on things to happen
		go func() {
			for {
				select {

				// quit
				case <-quit.ClickedCh:
					logger.Info("Quit menu item clicked, stopping")

					d.signalStop()

				// edit config
				case <-editConfig.ClickedCh:
					logger.Info("Edit config menu item clicked, opening config for editing")

					editor := "notepad.exe"
					if util.Linux() {
						editor = "gedit"
					}

					if err := util.OpenExternal(logger, editor, userConfigFilepath); err != nil {
						logger.Warnw("Failed to open config file for editing", "error", err)
					}

				// refresh sessions
				case <-refreshSessions.ClickedCh:
					logger.Info("Refresh sessions menu item clicked, triggering session map refresh")

					// performance: the reason that forcing a refresh here is okay is that users can't spam the
					// right-click -> select-this-option sequence at a rate that's meaningful to performance
					d.sessions.refreshSessions(true)
				}
			}
		}()

		// actually start the main runtime
		onDone()
	}

	onExit := func() {
		logger.Debug("Tray exited")
	}

	// start the tray icon
	logger.Debug("Running in tray")
	systray.Run(onReady, onExit)
}

func (d *Deej) stopTray() {
	d.logger.Debug("Quitting tray")
	systray.Quit()
}
