package deej

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jacobsa/go-serial/serial"
	"go.uber.org/zap"

	"github.com/gen2brain/beeep"
	"github.com/omriharel/deej/pkg/deej/util"
)

// SerialIO provides a deej-aware abstraction layer to managing serial I/O
type SerialIO struct {
	deej   *Deej
	logger *zap.SugaredLogger

	stopChannel  chan bool
	connected    bool
	reconnecting bool
	connOptions  serial.OpenOptions
	conn         io.ReadWriteCloser

	lastKnownNumSliders        int
	currentSliderPercentValues []float32

	sliderMoveConsumers []chan SliderMoveEvent
}

// SliderMoveEvent represents a single slider move captured by deej
type SliderMoveEvent struct {
	SliderID     int
	PercentValue float32
}

var expectedLinePattern = regexp.MustCompile(`^\d{1,4}(\|\d{1,4})*\r\n$`)

// NewSerialIO creates a SerialIO instance that uses the provided deej
// instance's connection info to establish communications with the arduino chip
func NewSerialIO(deej *Deej, logger *zap.SugaredLogger) (*SerialIO, error) {
	logger = logger.Named("serial")

	sio := &SerialIO{
		deej:                deej,
		logger:              logger,
		stopChannel:         make(chan bool),
		connected:           false,
		conn:                nil,
		sliderMoveConsumers: []chan SliderMoveEvent{},
	}

	logger.Debug("Created serial i/o instance")

	// respond to config changes
	sio.setupOnConfigReload()

	return sio, nil
}

// autoDetectArduinoPort scans for likely Arduino serial ports and returns the first one that sends a recognizable signature.
func autoDetectArduinoPort(baudRate uint, logger *zap.SugaredLogger) (string, error) {
	candidates := []string{}
	files, err := os.ReadDir("/dev")
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), "ttyUSB") || strings.HasPrefix(f.Name(), "ttyACM") {
			candidates = append(candidates, "/dev/"+f.Name())
		}
	}
	logger.Debugw("Auto-detecting Arduino port", "candidates", candidates)
	for _, port := range candidates {
		opts := serial.OpenOptions{
			PortName:        port,
			BaudRate:        baudRate,
			DataBits:        8,
			StopBits:        1,
			MinimumReadSize: 1,
		}
		f, err := serial.Open(opts)
		if err != nil {
			if strings.Contains(err.Error(), "permission denied") {
				// Try to get the group owner of the device
				if fi, statErr := os.Stat(port); statErr == nil {
					if stat, ok := fi.Sys().(*syscall.Stat_t); ok {
						gid := stat.Gid
						groupNames := []string{}
						if groupFile, gerr := os.Open("/etc/group"); gerr == nil {
							scanner := bufio.NewScanner(groupFile)
							for scanner.Scan() {
								line := scanner.Text()
								parts := strings.Split(line, ":")
								if len(parts) >= 3 && parts[2] == fmt.Sprint(gid) {
									groupNames = append(groupNames, parts[0])
								}
							}
							groupFile.Close()
						}
						groupNameStr := fmt.Sprintf("GID %d (unknown group)", gid)
						if len(groupNames) > 0 {
							groupNameStr = strings.Join(groupNames, " or ")
						}
						logger.Debugw("Detected group(s) for serial device", "port", port, "gid", gid, "groupNames", groupNameStr)

						user := os.Getenv("USER")
						if user == "" {
							user = os.Getenv("USERNAME") // Windows fallback
						}
						// Check if user is already in the group
						checkCmd := exec.Command("id", "-nG", user)
						output, err := checkCmd.Output()
						alreadyInGroup := false
						for _, g := range groupNames {
							if err == nil && strings.Contains(string(output), g) {
								alreadyInGroup = true
								break
							}
						}
						if alreadyInGroup {
							beeep.Alert("Already a Member", fmt.Sprintf("You are already a member of the '%s' group.\n\nPlease log out and log back in if you still have issues.", groupNameStr), "")
							continue
						}
						// Ask for confirmation using zenity
						confirm := exec.Command("zenity", "--question", "--text",
							fmt.Sprintf("Permission denied opening %s.\n\nWould you like to add yourself to the '%s' group?\n\nYou will be prompted for your password.", port, groupNameStr))
						err = confirm.Run()
						if err == nil && len(groupNames) > 0 { // User clicked Yes
							cmd := exec.Command("pkexec", "usermod", "-aG", groupNames[0], user)
							if err := cmd.Run(); err == nil {
								beeep.Alert("Action Required", "You have been added to the group.\n\nPlease log out and log back in, then rerun this program to continue.", "")
							} else {
								beeep.Alert("Error", "Failed to add you to the group.\n\nPlease run this command manually:\nsudo usermod -aG "+groupNames[0]+" "+user, "")
							}
						} else {
							beeep.Alert("Action Cancelled", "No changes were made.", "")
						}
					}
				}
			}
			logger.Debugw("Failed to open candidate port", "port", port, "error", err)
			continue // skip if can't open (e.g., permission denied)
		}
		// Give Arduino time to reset
		time.Sleep(2 * time.Second)
		buf := make([]byte, 64)
		n, _ := f.Read(buf)
		f.Close()
		if n > 0 && strings.Contains(string(buf[:n]), "deej") { // Check for enhanced startup message
			logger.Infow("Detected Arduino device", "port", port)
			return port, nil
		}
	}
	return "", fmt.Errorf("no Arduino device found")
}

// Start attempts to connect to our arduino chip
func (sio *SerialIO) Start() error {
	// don't allow multiple concurrent connections
	if sio.connected {
		sio.logger.Warn("Already connected, can't start another without closing first")
		return errors.New("serial: connection already active")
	}

	// set minimum read size according to platform (0 for windows, 1 for linux)
	minimumReadSize := 0
	if util.Linux() {
		minimumReadSize = 1
	}

	comPort := sio.deej.config.ConnectionInfo.COMPort
	baudRate := uint(sio.deej.config.ConnectionInfo.BaudRate)
	if comPort == "" || strings.ToLower(comPort) == "auto" {
		port, err := autoDetectArduinoPort(baudRate, sio.logger)
		if err != nil {
			sio.logger.Warnw("Could not auto-detect Arduino port", "error", err)
			sio.deej.SetTrayIcon(TrayError, DetectSystemTheme())
			return fmt.Errorf("auto-detect Arduino port: %w", err)
		}
		comPort = port
	}

	sio.connOptions = serial.OpenOptions{
		PortName:        comPort,
		BaudRate:        baudRate,
		DataBits:        8,
		StopBits:        1,
		MinimumReadSize: uint(minimumReadSize),
	}

	sio.logger.Debugw("Attempting serial connection",
		"comPort", sio.connOptions.PortName,
		"baudRate", sio.connOptions.BaudRate,
		"minReadSize", minimumReadSize)

	var err error
	sio.conn, err = serial.Open(sio.connOptions)
	if err != nil {
		// might need a user notification here, TBD
		sio.logger.Warnw("Failed to open serial connection", "error", err)
		return fmt.Errorf("open serial connection: %w", err)
	}

	namedLogger := sio.logger.Named(strings.ToLower(sio.connOptions.PortName))

	namedLogger.Infow("Connected", "conn", sio.conn)
	sio.connected = true
	sio.reconnecting = false // Reset reconnecting flag on successful connection
	sio.deej.SetTrayIcon(TrayNormal, DetectSystemTheme())

	// read lines or await a stop
	go func() {
		connReader := bufio.NewReader(sio.conn)
		lineChannel := sio.readLine(namedLogger, connReader)

		for line := range lineChannel {
			sio.handleLine(namedLogger, line)
		}

		// Channel closed means Arduino disconnected
		sio.logger.Warn("Arduino disconnected")
		sio.close(namedLogger)

		// Start reconnection attempts if not already reconnecting
		if !sio.reconnecting {
			sio.reconnecting = true
			go func() {
				sio.logger.Info("Starting reconnection attempts...")
				for {
					time.Sleep(5 * time.Second) // Wait before retry
					if err := sio.Start(); err == nil {
						sio.logger.Info("Successfully reconnected to Arduino")
						sio.deej.SetTrayIcon(TrayNormal, DetectSystemTheme())
						sio.reconnecting = false
						break
					} else {
						sio.logger.Warnw("Reconnection attempt failed", "error", err)
					}
				}
			}()
		}
	}()

	return nil
}

// Stop signals us to shut down our serial connection, if one is active
func (sio *SerialIO) Stop() {
	if sio.connected {
		sio.logger.Debug("Shutting down serial connection")
		sio.stopChannel <- true
	} else {
		sio.logger.Debug("Not currently connected, nothing to stop")
	}
}

// SubscribeToSliderMoveEvents returns an unbuffered channel that receives
// a sliderMoveEvent struct every time a slider moves
func (sio *SerialIO) SubscribeToSliderMoveEvents() chan SliderMoveEvent {
	ch := make(chan SliderMoveEvent)
	sio.sliderMoveConsumers = append(sio.sliderMoveConsumers, ch)

	return ch
}

func (sio *SerialIO) setupOnConfigReload() {
	configReloadedChannel := sio.deej.config.SubscribeToChanges()

	const stopDelay = 50 * time.Millisecond

	go func() {
		for range configReloadedChannel {
			// make any config reload unset our slider number to ensure process volumes are being re-set
			// (the next read line will emit SliderMoveEvent instances for all sliders)\
			// this needs to happen after a small delay, because the session map will also re-acquire sessions
			// whenever the config file is reloaded, and we don't want it to receive these move events while the map
			// is still cleared. this is kind of ugly, but shouldn't cause any issues
			go func() {
				<-time.After(stopDelay)
				sio.lastKnownNumSliders = 0
			}()

			// if connection params have changed, attempt to stop and start the connection
			if sio.deej.config.ConnectionInfo.COMPort != sio.connOptions.PortName ||
				uint(sio.deej.config.ConnectionInfo.BaudRate) != sio.connOptions.BaudRate {

				sio.logger.Info("Detected change in connection parameters, attempting to renew connection")
				sio.Stop()

				// let the connection close
				<-time.After(stopDelay)

				if err := sio.Start(); err != nil {
					sio.logger.Warnw("Failed to renew connection after parameter change", "error", err)
				} else {
					sio.logger.Debug("Renewed connection successfully")
				}
			}
		}
	}()
}

func (sio *SerialIO) close(logger *zap.SugaredLogger) {
	if err := sio.conn.Close(); err != nil {
		logger.Warnw("Failed to close serial connection", "error", err)
	} else {
		logger.Debug("Serial connection closed")
	}

	sio.conn = nil
	sio.connected = false

	// Set error icon when disconnected
	sio.deej.SetTrayIcon(TrayError, DetectSystemTheme())
}

func (sio *SerialIO) readLine(logger *zap.SugaredLogger, reader *bufio.Reader) chan string {
	ch := make(chan string)

	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {

				if sio.deej.Verbose() {
					logger.Warnw("Failed to read line from serial", "error", err, "line", line)
				}

				// Arduino disconnected - set error icon
				sio.deej.SetTrayIcon(TrayError, DetectSystemTheme())
				logger.Warnw("Arduino disconnected", "error", err)

				// Close the channel to signal disconnection
				close(ch)
				return
			}

			if sio.deej.Verbose() {
				logger.Debugw("Read new line", "line", line)
			}

			// deliver the line to the channel
			ch <- line
		}
	}()

	return ch
}

func (sio *SerialIO) handleLine(logger *zap.SugaredLogger, line string) {

	// Handle heartbeat signal
	if strings.Contains(line, "heartbeat") {
		if sio.deej.Verbose() {
			logger.Debug("Received heartbeat from Arduino")
		}
		sio.deej.SetTrayIcon(TrayNormal, DetectSystemTheme())
		return
	}

	// Handle status messages
	if strings.HasPrefix(line, "status:") {
		status := strings.TrimSpace(strings.TrimPrefix(line, "status:"))
		if sio.deej.Verbose() {
			logger.Debugw("Received status from Arduino", "status", status)
		}

		switch status {
		case "ok":
			sio.deej.SetTrayIcon(TrayNormal, DetectSystemTheme())
		case "warning":
			// Could set a warning icon here if you have one
			sio.deej.SetTrayIcon(TrayNormal, DetectSystemTheme())
		default:
			sio.deej.SetTrayIcon(TrayError, DetectSystemTheme())
		}
		return
	}

	// Handle version info (startup message)
	if strings.HasPrefix(line, "deej:") {
		parts := strings.Split(line, ":")
		if len(parts) >= 3 {
			version := parts[1]
			capabilities := parts[2]
			logger.Infow("Arduino connected", "version", version, "capabilities", capabilities)
		}
		sio.deej.SetTrayIcon(TrayNormal, DetectSystemTheme())
		return
	}

	// Handle regular slider data
	// this function receives an unsanitized line which is guaranteed to end with LF,
	// but most lines will end with CRLF. it may also have garbage instead of
	// deej-formatted values, so we must check for that! just ignore bad ones
	if !expectedLinePattern.MatchString(line) {
		return
	}

	// trim the suffix
	line = strings.TrimSuffix(line, "\r\n")

	// split on pipe (|), this gives a slice of numerical strings between "0" and "1023"
	splitLine := strings.Split(line, "|")
	numSliders := len(splitLine)

	// update our slider count, if needed - this will send slider move events for all
	if numSliders != sio.lastKnownNumSliders {
		logger.Infow("Detected sliders", "amount", numSliders)
		sio.lastKnownNumSliders = numSliders
		sio.currentSliderPercentValues = make([]float32, numSliders)

		// reset everything to be an impossible value to force the slider move event later
		for idx := range sio.currentSliderPercentValues {
			sio.currentSliderPercentValues[idx] = -1.0
		}
	}

	// for each slider:
	moveEvents := []SliderMoveEvent{}
	for sliderIdx, stringValue := range splitLine {

		// convert string values to integers ("1023" -> 1023)
		number, _ := strconv.Atoi(stringValue)

		// turns out the first line could come out dirty sometimes (i.e. "4558|925|41|643|220")
		// so let's check the first number for correctness just in case
		if sliderIdx == 0 && number > 1023 {
			sio.logger.Debugw("Got malformed line from serial, ignoring", "line", line)
			return
		}

		// map the value from raw to a "dirty" float between 0 and 1 (e.g. 0.15451...)
		dirtyFloat := float32(number) / 1023.0

		// normalize it to an actual volume scalar between 0.0 and 1.0 with 2 points of precision
		normalizedScalar := util.NormalizeScalar(dirtyFloat)

		// if sliders are inverted, take the complement of 1.0
		if sio.deej.config.InvertSliders {
			normalizedScalar = 1 - normalizedScalar
		}

		// check if it changes the desired state (could just be a jumpy raw slider value)
		if util.SignificantlyDifferent(sio.currentSliderPercentValues[sliderIdx], normalizedScalar, sio.deej.config.NoiseReductionLevel) {

			// if it does, update the saved value and create a move event
			sio.currentSliderPercentValues[sliderIdx] = normalizedScalar

			moveEvents = append(moveEvents, SliderMoveEvent{
				SliderID:     sliderIdx,
				PercentValue: normalizedScalar,
			})

			if sio.deej.Verbose() {
				logger.Debugw("Slider moved", "event", moveEvents[len(moveEvents)-1])
			}
		}
	}

	// deliver move events if there are any, towards all potential consumers
	if len(moveEvents) > 0 {
		for _, consumer := range sio.sliderMoveConsumers {
			for _, moveEvent := range moveEvents {
				consumer <- moveEvent
			}
		}
	}
}
