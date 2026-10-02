package deej

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jacobsa/go-serial/serial"
	"go.uber.org/zap"

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
	sliderDataMutex            sync.Mutex
	connMutex                  sync.Mutex

	sliderMoveConsumers []chan SliderMoveEvent
}

// SliderMoveEvent represents a single slider move captured by deej
type SliderMoveEvent struct {
	SliderID     int
	PercentValue float32
}

var expectedLinePattern = regexp.MustCompile(`^\d{1,4}(\|\d{1,4})*\r\n$`)

const firmwareVersion = "v2.0"

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

// isAutoCOMPort reports whether the configured port asks us to auto-detect the device
func isAutoCOMPort(comPort string) bool {
	trimmed := strings.TrimSpace(comPort)

	return trimmed == "" || strings.EqualFold(trimmed, "auto")
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
			// permission issues are handled once at startup by (*Deej).VerifyDeviceAccess
			logger.Debugw("Failed to open candidate port", "port", port, "error", err)
			continue // skip if can't open (e.g., permission denied)
		}
		// Give Arduino time to reset and respond
		time.Sleep(1 * time.Second)

		// Try to read multiple times in case the Arduino is slow to respond
		for attempt := 1; attempt <= 3; attempt++ {
			logger.Debugw("Attempting to read from port", "port", port, "attempt", attempt)

			// Send a command to request slider data to trigger a response
			if attempt == 1 {
				logger.Debugw("Sending slider request command to trigger response", "port", port)
				sliderCommand := fmt.Sprintf("deej:%s:command:sliders\n", firmwareVersion)
				_, writeErr := f.Write([]byte(sliderCommand))
				if writeErr != nil {
					logger.Debugw("Failed to send slider request command", "port", port, "error", writeErr)
				} else {
					logger.Debugw("Slider request command sent successfully", "port", port)
					// Give Arduino time to respond
					time.Sleep(200 * time.Millisecond)
				}
			}

			buf := make([]byte, 256)
			n, err := f.Read(buf)
			if err != nil {
				logger.Debugw("Read attempt failed", "port", port, "attempt", attempt, "error", err)
				time.Sleep(500 * time.Millisecond)
				continue
			}

			logger.Debugw("Read data from port", "port", port, "attempt", attempt, "bytesRead", n)
			if n > 0 {
				response := string(buf[:n])
				logger.Debugw("Read response from port", "port", port, "attempt", attempt, "response", response)

				// Check for any deej message (robust detection)
				lines := strings.Split(response, "\r\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					logger.Debugw("Checking line for deej message", "port", port, "line", line)
					if strings.HasPrefix(line, "deej:") {
						logger.Infow("Detected Arduino device", "port", port, "response_type", "deej_message", "sample_line", line)

						// Send reboot command to ensure Arduino goes through full startup sequence
						logger.Infow("Sending reboot command to Arduino to ensure proper startup sequence", "port", port)
						rebootCommand := fmt.Sprintf("deej:%s:command:reboot\n", firmwareVersion)
						_, writeErr := f.Write([]byte(rebootCommand))
						if writeErr != nil {
							logger.Warnw("Failed to send reboot command", "port", port, "error", writeErr)
						} else {
							logger.Infow("Reboot command sent successfully", "port", port)
							// Give Arduino time to process reboot command
							time.Sleep(200 * time.Millisecond)
						}

						f.Close()
						return port, nil
					}
				}
			} else {
				logger.Debugw("No data read from port", "port", port, "attempt", attempt)
			}

			// Wait before next attempt
			time.Sleep(500 * time.Millisecond)
		}

		logger.Debugw("No deej device found on port", "port", port)
		f.Close()
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
	if isAutoCOMPort(comPort) {
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

	// Set tray icon immediately on connection
	sio.deej.SetTrayIcon(TrayNormal, DetectSystemTheme())

	// Give Arduino time to reboot and send startup sequence if a reboot was triggered
	// This ensures we receive the initial slider data
	time.Sleep(1 * time.Second)

	// read lines or await a stop
	go func() {
		connReader := bufio.NewReader(sio.conn)
		lineChannel := sio.readLine(namedLogger, connReader)

		for {
			select {

			// someone asked us to shut this connection down - closing the connection also
			// unblocks the reader goroutine, which will then close its channel
			case <-sio.stopChannel:
				namedLogger.Debug("Stop requested, closing serial connection")
				sio.close(namedLogger)

				return

			case line, ok := <-lineChannel:
				if !ok {
					// Channel closed means Arduino disconnected
					sio.logger.Warn("Arduino disconnected")
					sio.close(namedLogger)
					sio.startReconnecting()

					return
				}

				// Process each line asynchronously to prevent blocking the serial reading
				go sio.handleLine(namedLogger, line)
			}
		}
	}()

	return nil
}

// startReconnecting keeps trying to re-establish a lost connection, unless we're already at it
func (sio *SerialIO) startReconnecting() {
	if sio.reconnecting {
		return
	}

	sio.reconnecting = true

	go func() {
		sio.logger.Info("Starting reconnection attempts...")

		for {
			time.Sleep(5 * time.Second) // Wait before retry

			if err := sio.Start(); err == nil {
				sio.logger.Info("Successfully reconnected to Arduino")
				sio.deej.SetTrayIcon(TrayNormal, DetectSystemTheme())
				sio.reconnecting = false

				return
			}

			sio.logger.Debug("Reconnection attempt failed")
		}
	}()
}

// Stop signals us to shut down our serial connection, if one is active
func (sio *SerialIO) Stop() {
	if !sio.connected {
		sio.logger.Debug("Not currently connected, nothing to stop")
		return
	}

	sio.logger.Debug("Shutting down serial connection")

	// the reader goroutine owns the connection, but it may already be on its way down (e.g. the
	// device was unplugged). don't block the caller forever if nobody's listening anymore
	select {
	case sio.stopChannel <- true:
	case <-time.After(time.Second):
		sio.logger.Debug("Nothing picked up our stop signal, closing the connection directly")
		sio.close(sio.logger)
	}
}

// SubscribeToSliderMoveEvents returns a buffered channel that receives
// a sliderMoveEvent struct every time a slider moves
func (sio *SerialIO) SubscribeToSliderMoveEvents() chan SliderMoveEvent {
	ch := make(chan SliderMoveEvent, 100) // Buffer up to 100 events to prevent blocking
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

			// if connection params have changed, attempt to stop and start the connection.
			// an auto-detected port never equals the literal "auto" we were configured with,
			// so only compare port names when the user pinned a specific one - otherwise every
			// single config reload would needlessly tear down a perfectly good connection
			configuredPort := sio.deej.config.ConnectionInfo.COMPort
			portChanged := !isAutoCOMPort(configuredPort) && configuredPort != sio.connOptions.PortName
			baudRateChanged := uint(sio.deej.config.ConnectionInfo.BaudRate) != sio.connOptions.BaudRate

			if portChanged || baudRateChanged {

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
	sio.connMutex.Lock()
	defer sio.connMutex.Unlock()

	// a disconnect and an explicit stop can race each other - whoever gets here second
	// has nothing left to close, and must not dereference the nil connection
	if sio.conn == nil {
		sio.connected = false
		return
	}

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

				// the tray icon is updated by whoever closes the connection, so that a
				// deliberate restart doesn't leave us showing an error over a healthy link
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
	// Trim whitespace and newlines
	line = strings.TrimSpace(line)

	// Handle new deej protocol messages
	if strings.HasPrefix(line, "deej:") {
		parts := strings.Split(line, ":")
		if len(parts) < 3 {
			return // Invalid message format
		}

		messageType := parts[2]

		switch messageType {
		case "startup":
			if len(parts) >= 4 {
				capabilities := parts[3]
				logger.Infow("Arduino connected", "version", parts[1], "capabilities", capabilities)
			}
			sio.deej.SetTrayIcon(TrayNormal, DetectSystemTheme())
			return

		case "sliders":
			if len(parts) >= 4 {
				// Extract slider data from the message
				sliderData := parts[3]
				sio.processSliderData(logger, sliderData)
			}
			return

		case "response":
			if len(parts) >= 4 {
				responseType := parts[3]
				sio.handleCommandResponse(logger, responseType, parts[4:])
			}
			return
		}
	}

	// Fallback: Handle old format messages for backward compatibility
	if strings.HasPrefix(line, "status:") {
		status := strings.TrimSpace(strings.TrimPrefix(line, "status:"))
		if sio.deej.Verbose() {
			logger.Debugw("Received status from Arduino (old format)", "status", status)
		}

		switch status {
		case "ok":
			sio.deej.SetTrayIcon(TrayNormal, DetectSystemTheme())
		case "warning":
			sio.deej.SetTrayIcon(TrayNormal, DetectSystemTheme())
		default:
			sio.deej.SetTrayIcon(TrayError, DetectSystemTheme())
		}
		return
	}

	// Handle old format slider data
	if expectedLinePattern.MatchString(line) {
		sio.processSliderData(logger, line)
	}
}

func (sio *SerialIO) processSliderData(logger *zap.SugaredLogger, sliderData string) {
	// split on pipe (|), this gives a slice of numerical strings between "0" and "1023"
	splitLine := strings.Split(sliderData, "|")
	numSliders := len(splitLine)

	// Use a mutex to protect shared state when processing slider data concurrently
	sio.sliderDataMutex.Lock()
	defer sio.sliderDataMutex.Unlock()

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
			sio.logger.Debugw("Got malformed line from serial, ignoring", "line", sliderData)
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
		// For initial values (when currentSliderPercentValues[sliderIdx] == -1.0), always process
		// to ensure initial volume levels are set
		if sio.currentSliderPercentValues[sliderIdx] == -1.0 ||
			util.SignificantlyDifferent(sio.currentSliderPercentValues[sliderIdx], normalizedScalar, sio.deej.config.NoiseReductionLevel) {

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
		if sio.deej.Verbose() {
			logger.Debugw("Processing slider events", "count", len(moveEvents))
		} else {
			// Always log initial slider events for debugging
			logger.Infow("Processing initial slider events", "count", len(moveEvents), "consumers", len(sio.sliderMoveConsumers))
		}
		for _, consumer := range sio.sliderMoveConsumers {
			for _, moveEvent := range moveEvents {
				// Use non-blocking send to prevent serial processing from being blocked
				select {
				case consumer <- moveEvent:
					// Event sent successfully
				default:
					// Channel is full, skip this event to prevent blocking
					if sio.deej.Verbose() {
						logger.Debugw("Slider event channel full, skipping event", "sliderID", moveEvent.SliderID)
					}
				}
			}
		}
	} else {
		// Log when no events are generated (for debugging)
		if sio.deej.Verbose() {
			logger.Debugw("No slider events generated", "sliderData", sliderData)
		}
	}
}

// SendCommand sends a command to the Arduino
func (sio *SerialIO) SendCommand(command string) error {
	if !sio.connected || sio.conn == nil {
		return fmt.Errorf("not connected to Arduino")
	}

	// Format command with protocol prefix
	formattedCommand := fmt.Sprintf("deej:%s:command:%s\n", firmwareVersion, command)

	_, err := sio.conn.Write([]byte(formattedCommand))
	if err != nil {
		sio.logger.Warnw("Failed to send command to Arduino", "command", command, "error", err)
		return fmt.Errorf("send command: %w", err)
	}

	sio.logger.Debugw("Sent command to Arduino", "command", command)
	return nil
}

// RebootArduino sends a reboot command to the Arduino
func (sio *SerialIO) RebootArduino() error {
	// Notify user that reboot command is being sent
	sio.deej.notifier.Notify("Arduino Reboot", "Sending reboot command to Arduino...")

	return sio.SendCommand("reboot")
}

// RequestVersion sends a version request command to the Arduino
func (sio *SerialIO) RequestVersion() error {
	return sio.SendCommand("version")
}

// GetNumSliders returns the number of sliders detected from the Arduino
func (sio *SerialIO) GetNumSliders() int {
	sio.sliderDataMutex.Lock()
	defer sio.sliderDataMutex.Unlock()
	return sio.lastKnownNumSliders
}

func (sio *SerialIO) handleCommandResponse(logger *zap.SugaredLogger, responseType string, responseArgs []string) {
	// Handle command response based on the response type
	switch responseType {
	case "reboot_ack":
		logger.Info("Arduino acknowledged reboot command, device will restart")
		return

	case "version":
		if len(responseArgs) >= 1 {
			version := responseArgs[0]
			logger.Infow("Arduino firmware version", "version", version)
		} else {
			logger.Info("Arduino version response received")
		}
		return

	case "error":
		if len(responseArgs) >= 2 {
			errorType := responseArgs[0]
			errorDetails := responseArgs[1]
			logger.Warnw("Arduino command error", "type", errorType, "details", errorDetails)
		} else {
			logger.Warn("Arduino command error received")
		}
		return

	default:
		logger.Debugw("Unhandled command response type", "type", responseType, "args", responseArgs)
		return
	}
}
