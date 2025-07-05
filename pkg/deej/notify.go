package deej

import (
	"os"
	"path/filepath"

	"github.com/gen2brain/beeep"
	"go.uber.org/zap"

	"github.com/omriharel/deej/pkg/deej/icon"
	"github.com/omriharel/deej/pkg/deej/util"
)

// Notifier provides generic notification sending
type Notifier interface {
	Notify(title string, message string)
}

// ToastNotifier provides toast notifications for Windows
type ToastNotifier struct {
	logger *zap.SugaredLogger
}

// NewToastNotifier creates a new ToastNotifier
func NewToastNotifier(logger *zap.SugaredLogger) (*ToastNotifier, error) {
	logger = logger.Named("notifier")
	tn := &ToastNotifier{logger: logger}

	logger.Debug("Created toast notifier instance")

	return tn, nil
}

// Notify sends a toast notification (or falls back to other types of notification for older Windows versions)
func (tn *ToastNotifier) Notify(title string, message string) {

	// Detect system theme to use appropriate icon
	theme := DetectSystemTheme()
	var iconData []byte

	switch theme {
	case ThemeLight:
		iconData = icon.NormalLightIcon
	default:
		iconData = icon.NormalDarkIcon
	}

	// we need to unpack the theme-appropriate icon somewhere to remain portable
	appIconPath := filepath.Join(os.TempDir(), "deej-theme.ico")

	if !util.FileExists(appIconPath) {
		tn.logger.Debugw("Deej theme icon file missing, creating", "path", appIconPath, "theme", theme)

		f, err := os.Create(appIconPath)
		if err != nil {
			tn.logger.Errorw("Failed to create toast notification icon", "error", err)
		}

		if _, err = f.Write(iconData); err != nil {
			tn.logger.Errorw("Failed to write toast notification icon", "error", err)
		}

		if err = f.Close(); err != nil {
			tn.logger.Errorw("Failed to close toast notification icon", "error", err)
		}
	}

	tn.logger.Infow("Sending toast notification", "title", title, "message", message, "theme", theme)

	// send the actual notification
	if err := beeep.Notify(title, message, appIconPath); err != nil {
		tn.logger.Errorw("Failed to send toast notification", "error", err)
	}
}
