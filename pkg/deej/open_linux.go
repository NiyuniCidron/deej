package deej

import (
	"fmt"
	"path/filepath"

	"go.uber.org/zap"
)

// openConfigFile opens deej's configuration file for editing. xdg-open picks whatever the
// user has set as their text editor, and inside a flatpak the request goes to the host,
// since the runtime doesn't ship an editor of its own.
func (d *Deej) openConfigFile(logger *zap.SugaredLogger) error {
	path, err := filepath.Abs(userConfigFilepath)
	if err != nil {
		return fmt.Errorf("resolve config file path: %w", err)
	}

	cmd := hostCommand("xdg-open", path)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}

	logger.Debugw("Opened config file for editing", "path", path, "sandboxed", inFlatpak())

	// don't leave the editor behind as a zombie, but don't wait on it either
	go cmd.Wait()

	return nil
}
