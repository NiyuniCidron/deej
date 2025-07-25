// Package deej provides a machine-side client that pairs with an Arduino
// chip to form a tactile, physical volume control system/
package deej

import (
	"errors"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/omriharel/deej/pkg/deej/util"
)

const (

	// when this is set to anything, deej won't use a tray icon
	envNoTray = "DEEJ_NO_TRAY_ICON"
)

// Deej is the main entity managing access to all sub-components
type Deej struct {
	logger   *zap.SugaredLogger
	notifier Notifier
	config   *CanonicalConfig
	serial   *SerialIO
	sessions *sessionMap

	stopChannel chan bool
	version     string
	verbose     bool
}

// NewDeej creates a Deej instance
func NewDeej(logger *zap.SugaredLogger, verbose bool) (*Deej, error) {
	logger = logger.Named("deej")

	notifier, err := NewToastNotifier(logger)
	if err != nil {
		logger.Errorw("Failed to create ToastNotifier", "error", err)
		return nil, fmt.Errorf("create new ToastNotifier: %w", err)
	}

	config, err := NewConfig(logger, notifier)
	if err != nil {
		logger.Errorw("Failed to create Config", "error", err)
		return nil, fmt.Errorf("create new Config: %w", err)
	}

	d := &Deej{
		logger:      logger,
		notifier:    notifier,
		config:      config,
		stopChannel: make(chan bool),
		verbose:     verbose,
	}

	serial, err := NewSerialIO(d, logger)
	if err != nil {
		logger.Errorw("Failed to create SerialIO", "error", err)
		return nil, fmt.Errorf("create new SerialIO: %w", err)
	}

	d.serial = serial

	sessionFinder, err := newSessionFinder(logger)
	if err != nil {
		logger.Errorw("Failed to create SessionFinder", "error", err)
		return nil, fmt.Errorf("create new SessionFinder: %w", err)
	}

	sessions, err := newSessionMap(d, logger, sessionFinder)
	if err != nil {
		logger.Errorw("Failed to create sessionMap", "error", err)
		return nil, fmt.Errorf("create new sessionMap: %w", err)
	}

	d.sessions = sessions

	logger.Debug("Created deej instance")

	return d, nil
}

// Initialize sets up components and starts to run in the background
func (d *Deej) Initialize() error {
	d.logger.Debug("Initializing")

	// load the config for the first time
	if err := d.config.Load(); err != nil {
		d.logger.Errorw("Failed to load config during initialization", "error", err)
		return fmt.Errorf("load config during init: %w", err)
	}

	// initialize the session map
	if err := d.sessions.initialize(); err != nil {
		d.logger.Errorw("Failed to initialize session map", "error", err)
		return fmt.Errorf("init session map: %w", err)
	}

	d.logger.Debug("About to check for tray mode")

	// decide whether to run with/without tray
	if _, noTraySet := os.LookupEnv(envNoTray); noTraySet {

		d.logger.Debugw("Running without tray icon", "reason", "envvar set")

		// run in main thread while waiting on ctrl+C
		d.setupInterruptHandler()
		d.run()

	} else {
		d.logger.Debug("About to setup interrupt handler")
		d.setupInterruptHandler()
		d.logger.Debug("About to initialize tray")
		d.initializeTray(d.run)
	}

	return nil
}

// SetVersion causes deej to add a version string to its tray menu if called before Initialize
func (d *Deej) SetVersion(version string) {
	d.version = version
}

// Verbose returns a boolean indicating whether deej is running in verbose mode
func (d *Deej) Verbose() bool {
	return d.verbose
}

func (d *Deej) setupInterruptHandler() {
	interruptChannel := util.SetupCloseHandler()

	go func() {
		signal := <-interruptChannel
		d.logger.Debugw("Interrupted", "signal", signal)
		d.signalStop()
	}()
}

func (d *Deej) run() {
	d.logger.Info("Run loop starting")

	// watch the config file for changes
	go d.config.WatchConfigFileChanges()

	// connect to the arduino for the first time with retry logic
	go func() {
		// Try initial connection with retries
		maxRetries := 5
		retryDelay := 2 * time.Second

		for attempt := 1; attempt <= maxRetries; attempt++ {
			d.logger.Infow("Attempting initial Arduino connection", "attempt", attempt, "maxRetries", maxRetries)

			if err := d.serial.Start(); err == nil {
				d.logger.Info("Initial Arduino connection successful")
				return
			} else {
				d.logger.Warnw("Failed to start first-time serial connection", "attempt", attempt, "error", err)

				// If the port is busy, that's because something else is connected - notify and quit
				if errors.Is(err, os.ErrPermission) {
					d.logger.Warnw("Serial port seems busy, notifying user and closing",
						"comPort", d.config.ConnectionInfo.COMPort)

					d.notifier.Notify(fmt.Sprintf("Can't connect to %s!", d.config.ConnectionInfo.COMPort),
						"This serial port is busy, make sure to close any serial monitor or other deej instance.")

					d.signalStop()
					return

					// also notify if the COM port they gave isn't found, maybe their config is wrong
				} else if errors.Is(err, os.ErrNotExist) {
					d.logger.Warnw("Provided COM port seems wrong, notifying user and closing",
						"comPort", d.config.ConnectionInfo.COMPort)

					d.notifier.Notify(fmt.Sprintf("Can't connect to %s!", d.config.ConnectionInfo.COMPort),
						"This serial port doesn't exist, check your configuration and make sure it's set correctly.")

					d.signalStop()
					return
				}

				// For other errors, retry after delay
				if attempt < maxRetries {
					d.logger.Infow("Retrying initial connection", "attempt", attempt+1, "delay", retryDelay)
					time.Sleep(retryDelay)
				}
			}
		}

		// If we get here, all retries failed
		d.logger.Error("All initial connection attempts failed, starting reconnection loop")

		// Start the reconnection loop for ongoing attempts
		go func() {
			for {
				time.Sleep(5 * time.Second)
				if err := d.serial.Start(); err == nil {
					d.logger.Info("Successfully connected to Arduino after initial failures")
					return
				} else {
					d.logger.Debugw("Reconnection attempt failed", "error", err)
				}
			}
		}()
	}()

	// wait until stopped (gracefully)
	<-d.stopChannel
	d.logger.Debug("Stop channel signaled, terminating")

	if err := d.stop(); err != nil {
		d.logger.Warnw("Failed to stop deej", "error", err)
		os.Exit(1)
	} else {
		// exit with 0
		os.Exit(0)
	}
}

func (d *Deej) signalStop() {
	d.logger.Debug("Signalling stop channel")
	d.stopChannel <- true
}

func (d *Deej) stop() error {
	d.logger.Info("Stopping")

	d.config.StopWatchingConfigFile()
	d.serial.Stop()

	// release the session map
	if err := d.sessions.release(); err != nil {
		d.logger.Errorw("Failed to release session map", "error", err)
		return fmt.Errorf("release session map: %w", err)
	}

	d.stopTray()

	// attempt to sync on exit - this won't necessarily work but can't harm
	d.logger.Sync()

	return nil
}
