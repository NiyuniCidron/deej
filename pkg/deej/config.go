package deej

import (
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	"go.uber.org/zap"

	"github.com/omriharel/deej/pkg/deej/util"
)

// CanonicalConfig provides application-wide access to configuration fields,
// as well as loading/file watching logic for deej's configuration file
type CanonicalConfig struct {
	SliderMapping *sliderMap

	ConnectionInfo struct {
		COMPort  string
		BaudRate int
	}

	InvertSliders bool

	NoiseReductionLevel string

	logger             *zap.SugaredLogger
	notifier           Notifier
	stopWatcherChannel chan bool

	reloadConsumers []chan bool
	reloadMutex     sync.Mutex
	lastReload      time.Time

	userConfig     *viper.Viper
	internalConfig *viper.Viper
}

const (
	userConfigFilepath     = "config.yaml"
	internalConfigFilepath = "preferences.yaml"

	userConfigName     = "config"
	internalConfigName = "preferences"

	userConfigPath = "."

	configType = "yaml"

	minTimeBetweenReloadAttempts = time.Millisecond * 500

	configKeySliderMapping       = "slider_mapping"
	configKeyInvertSliders       = "invert_sliders"
	configKeyCOMPort             = "com_port"
	configKeyBaudRate            = "baud_rate"
	configKeyNoiseReductionLevel = "noise_reduction"

	defaultCOMPort  = "COM4"
	defaultBaudRate = 9600
)

// has to be defined as a non-constant because we're using path.Join
var internalConfigPath = path.Join(".", logDirectory)

// NewConfig creates a config instance for the deej object and sets up viper instances for deej's config files
func NewConfig(logger *zap.SugaredLogger, notifier Notifier) (*CanonicalConfig, error) {
	logger = logger.Named("config")

	cc := &CanonicalConfig{
		logger:             logger,
		notifier:           notifier,
		reloadConsumers:    []chan bool{},
		stopWatcherChannel: make(chan bool),
	}

	// distinguish between the user-provided config (config.yaml) and the internal config (logs/preferences.yaml)
	userConfig := viper.New()
	userConfig.SetConfigName(userConfigName)
	userConfig.SetConfigType(configType)
	userConfig.AddConfigPath(userConfigPath)

	userConfig.SetDefault(configKeySliderMapping, map[string][]string{})
	userConfig.SetDefault(configKeyInvertSliders, false)
	userConfig.SetDefault(configKeyCOMPort, defaultCOMPort)
	userConfig.SetDefault(configKeyBaudRate, defaultBaudRate)

	internalConfig := viper.New()
	internalConfig.SetConfigName(internalConfigName)
	internalConfig.SetConfigType(configType)
	internalConfig.AddConfigPath(internalConfigPath)

	cc.userConfig = userConfig
	cc.internalConfig = internalConfig

	logger.Debug("Created config instance")

	return cc, nil
}

// Load reads deej's config files from disk and tries to parse them
func (cc *CanonicalConfig) Load() error {
	cc.logger.Debugw("Loading config", "path", userConfigFilepath)

	// make sure it exists
	if !util.FileExists(userConfigFilepath) {
		cc.logger.Warnw("Config file not found", "path", userConfigFilepath)
		cc.notifier.Notify("Can't find configuration!",
			fmt.Sprintf("%s must be in the same directory as deej. Please re-launch", userConfigFilepath))

		return fmt.Errorf("config file doesn't exist: %s", userConfigFilepath)
	}

	// load the user config
	if err := cc.userConfig.ReadInConfig(); err != nil {
		cc.logger.Warnw("Viper failed to read user config", "error", err)

		// if the error is yaml-format-related, show a sensible error. otherwise, show 'em to the logs
		if strings.Contains(err.Error(), "yaml:") {
			cc.notifier.Notify("Invalid configuration!",
				fmt.Sprintf("Please make sure %s is in a valid YAML format.", userConfigFilepath))
		} else {
			cc.notifier.Notify("Error loading configuration!", "Please check deej's logs for more details.")
		}

		return fmt.Errorf("read user config: %w", err)
	}

	// load the internal config - this doesn't have to exist, so it can error
	if err := cc.internalConfig.ReadInConfig(); err != nil {
		cc.logger.Debugw("Viper failed to read internal config", "error", err, "reminder", "this is fine")
	}

	// canonize the configuration with viper's helpers
	if err := cc.populateFromVipers(); err != nil {
		cc.logger.Warnw("Failed to populate config fields", "error", err)
		return fmt.Errorf("populate config fields: %w", err)
	}

	cc.logger.Info("Loaded config successfully")
	cc.logger.Infow("Config values",
		"sliderMapping", cc.SliderMapping,
		"connectionInfo", cc.ConnectionInfo,
		"invertSliders", cc.InvertSliders)

	return nil
}

// SubscribeToChanges allows external components to receive updates when the config is reloaded.
// The channel is buffered, so a consumer that's busy applying the previous reload can never
// hold up the reload itself or anyone else subscribed to it
func (cc *CanonicalConfig) SubscribeToChanges() chan bool {
	c := make(chan bool, 1)
	cc.reloadConsumers = append(cc.reloadConsumers, c)

	return c
}

// Reload re-reads deej's config file from disk and lets everyone know about it. Reloads
// coming in quicker than minTimeBetweenReloadAttempts of each other are skipped, which both
// absorbs the double writes many editors do and keeps the file watcher from repeating a
// reload that saving the config already performed
func (cc *CanonicalConfig) Reload() error {
	cc.reloadMutex.Lock()
	defer cc.reloadMutex.Unlock()

	now := time.Now()
	if cc.lastReload.Add(minTimeBetweenReloadAttempts).After(now) {
		cc.logger.Debug("Skipping reload, the config was just reloaded")
		return nil
	}
	cc.lastReload = now

	if err := cc.Load(); err != nil {
		return err
	}

	cc.logger.Info("Reloaded config successfully")
	cc.notifier.Notify("Configuration reloaded!", "Your changes have been applied.")

	cc.onConfigReloaded()

	return nil
}

// WatchConfigFileChanges starts watching for configuration file changes
// and attempts reloading the config when they happen
func (cc *CanonicalConfig) WatchConfigFileChanges() {
	cc.logger.Debugw("Starting to watch user config file for changes", "path", userConfigFilepath)

	const delayBetweenEventAndReload = time.Millisecond * 50

	// establish watch using viper as opposed to doing it ourselves, though our internal cooldown is still required
	cc.userConfig.WatchConfig()
	cc.userConfig.OnConfigChange(func(event fsnotify.Event) {

		// when we get a write event...
		if event.Op&fsnotify.Write == fsnotify.Write {
			cc.logger.Debugw("Config file modified, attempting reload", "event", event)

			// wait a bit to let the editor actually flush the new file contents to disk
			<-time.After(delayBetweenEventAndReload)

			if err := cc.Reload(); err != nil {
				cc.logger.Warnw("Failed to reload config file", "error", err)
			}
		}
	})

	// wait till they stop us
	<-cc.stopWatcherChannel
	cc.logger.Debug("Stopping user config file watcher")
	cc.userConfig.OnConfigChange(nil)
}

// StopWatchingConfigFile signals our filesystem watcher to stop
func (cc *CanonicalConfig) StopWatchingConfigFile() {
	cc.stopWatcherChannel <- true
}

func (cc *CanonicalConfig) populateFromVipers() error {

	// merge the slider mappings from the user and internal configs
	cc.SliderMapping = sliderMapFromConfigs(
		cc.userConfig.GetStringMapStringSlice(configKeySliderMapping),
		cc.internalConfig.GetStringMapStringSlice(configKeySliderMapping),
	)

	// get the rest of the config fields - viper saves us a lot of effort here
	cc.ConnectionInfo.COMPort = cc.userConfig.GetString(configKeyCOMPort)

	cc.ConnectionInfo.BaudRate = cc.userConfig.GetInt(configKeyBaudRate)
	if cc.ConnectionInfo.BaudRate <= 0 {
		cc.logger.Warnw("Invalid baud rate specified, using default value",
			"key", configKeyBaudRate,
			"invalidValue", cc.ConnectionInfo.BaudRate,
			"defaultValue", defaultBaudRate)

		cc.ConnectionInfo.BaudRate = defaultBaudRate
	}

	cc.InvertSliders = cc.userConfig.GetBool(configKeyInvertSliders)
	cc.NoiseReductionLevel = cc.userConfig.GetString(configKeyNoiseReductionLevel)

	cc.logger.Debug("Populated config fields from vipers")

	return nil
}

func (cc *CanonicalConfig) onConfigReloaded() {
	cc.logger.Debug("Notifying consumers about configuration reload")

	for _, consumer := range cc.reloadConsumers {
		select {
		case consumer <- true:
		default:
			// this consumer hasn't picked up the previous reload yet, and a pending
			// notification already tells it everything this one would have
			cc.logger.Debug("Consumer already has a reload pending, not queueing another")
		}
	}
}
