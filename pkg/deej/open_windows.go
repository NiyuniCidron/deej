package deej

import (
	"go.uber.org/zap"

	"github.com/omriharel/deej/pkg/deej/util"
)

// openConfigFile opens deej's configuration file for editing
func (d *Deej) openConfigFile(logger *zap.SugaredLogger) error {
	return util.OpenExternal(logger, "notepad.exe", userConfigFilepath)
}
