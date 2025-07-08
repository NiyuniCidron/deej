package deej

import (
	"fmt"
	"io/ioutil"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/jfreymuth/pulse/proto"
	"go.uber.org/zap"
)

// getProcessNameFromPID returns the process name for a given PID using /proc
func getProcessNameFromPID(pid uint32) string {
	commPath := "/proc/" + strconv.Itoa(int(pid)) + "/comm"
	data, err := ioutil.ReadFile(commPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

type paSessionFinder struct {
	logger        *zap.SugaredLogger
	sessionLogger *zap.SugaredLogger

	client *proto.Client
	conn   net.Conn
}

func newSessionFinder(logger *zap.SugaredLogger) (SessionFinder, error) {
	client, conn, err := proto.Connect("")
	if err != nil {
		logger.Warnw("Failed to establish PulseAudio connection", "error", err)
		return nil, fmt.Errorf("establish PulseAudio connection: %w", err)
	}

	request := proto.SetClientName{
		Props: proto.PropList{
			"application.name": proto.PropListString("deej"),
		},
	}
	reply := proto.SetClientNameReply{}

	if err := client.Request(&request, &reply); err != nil {
		return nil, err
	}

	sf := &paSessionFinder{
		logger:        logger.Named("session_finder"),
		sessionLogger: logger.Named("sessions"),
		client:        client,
		conn:          conn,
	}

	sf.logger.Debug("Created PA session finder instance")

	return sf, nil
}

func (sf *paSessionFinder) GetAllSessions() ([]Session, error) {
	sf.logger.Debug("Starting GetAllSessions")
	sessions := []Session{}

	// get the master sink session
	sf.logger.Debug("Getting master sink session")
	masterSink, err := sf.getMasterSinkSession()
	if err == nil {
		sessions = append(sessions, masterSink)
		sf.logger.Debug("Added master sink session")
	} else {
		sf.logger.Warnw("Failed to get master audio sink session", "error", err)
	}

	// get the master source session
	sf.logger.Debug("Getting master source session")
	masterSource, err := sf.getMasterSourceSession()
	if err == nil {
		sessions = append(sessions, masterSource)
		sf.logger.Debug("Added master source session")
	} else {
		sf.logger.Warnw("Failed to get master audio source session", "error", err)
	}

	// enumerate sink inputs and add sessions along the way
	sf.logger.Debug("Enumerating sink inputs")
	if err := sf.enumerateAndAddSessions(&sessions); err != nil {
		sf.logger.Warnw("Failed to enumerate audio sessions", "error", err)
		return nil, fmt.Errorf("enumerate audio sessions: %w", err)
	}

	sf.logger.Debugw("GetAllSessions complete", "sessionCount", len(sessions))
	return sessions, nil
}

func (sf *paSessionFinder) Release() error {
	if err := sf.conn.Close(); err != nil {
		sf.logger.Warnw("Failed to close PulseAudio connection", "error", err)
		return fmt.Errorf("close PulseAudio connection: %w", err)
	}

	sf.logger.Debug("Released PA session finder instance")

	return nil
}

func (sf *paSessionFinder) getMasterSinkSession() (Session, error) {
	sf.logger.Debug("Requesting master sink info")

	request := proto.GetSinkInfo{
		SinkIndex: proto.Undefined,
	}
	reply := proto.GetSinkInfoReply{}

	// Use a channel to implement timeout
	done := make(chan error, 1)
	go func() {
		done <- sf.client.Request(&request, &reply)
	}()

	// Wait for either completion or timeout
	select {
	case err := <-done:
		if err != nil {
			sf.logger.Warnw("Failed to get master sink info", "error", err)
			return nil, fmt.Errorf("get master sink info: %w", err)
		}
	case <-time.After(2 * time.Second):
		sf.logger.Warnw("Timeout getting master sink info")
		return nil, fmt.Errorf("timeout getting master sink info")
	}

	sf.logger.Debug("Got master sink info, creating session")
	// create the master sink session
	sink := newMasterSession(sf.sessionLogger, sf.client, reply.SinkIndex, reply.Channels, true)

	return sink, nil
}

func (sf *paSessionFinder) getMasterSourceSession() (Session, error) {
	sf.logger.Debug("Requesting master source info")

	request := proto.GetSourceInfo{
		SourceIndex: proto.Undefined,
	}
	reply := proto.GetSourceInfoReply{}

	// Use a channel to implement timeout
	done := make(chan error, 1)
	go func() {
		done <- sf.client.Request(&request, &reply)
	}()

	// Wait for either completion or timeout
	select {
	case err := <-done:
		if err != nil {
			sf.logger.Warnw("Failed to get master source info", "error", err)
			return nil, fmt.Errorf("get master source info: %w", err)
		}
	case <-time.After(2 * time.Second):
		sf.logger.Warnw("Timeout getting master source info")
		return nil, fmt.Errorf("timeout getting master source info")
	}

	sf.logger.Debug("Got master source info, creating session")
	// create the master source session
	source := newMasterSession(sf.sessionLogger, sf.client, reply.SourceIndex, reply.Channels, false)

	return source, nil
}

func (sf *paSessionFinder) enumerateAndAddSessions(sessions *[]Session) error {
	sf.logger.Debug("Starting enumerateAndAddSessions")

	request := proto.GetSinkInputInfoList{}
	reply := proto.GetSinkInputInfoListReply{}

	sf.logger.Debug("Requesting sink input list from PulseAudio")

	// Use a channel to implement timeout
	done := make(chan error, 1)
	go func() {
		done <- sf.client.Request(&request, &reply)
	}()

	// Wait for either completion or timeout
	select {
	case err := <-done:
		if err != nil {
			sf.logger.Warnw("Failed to get sink input list", "error", err)
			return fmt.Errorf("get sink input list: %w", err)
		}
	case <-time.After(2 * time.Second):
		sf.logger.Warnw("Timeout getting sink input list")
		return fmt.Errorf("timeout getting sink input list")
	}

	sf.logger.Debugw("Got sink input list", "count", len(reply))

	for i, info := range reply {
		sf.logger.Debugw("Processing sink input", "index", i, "sinkInputIndex", info.SinkInputIndex)

		// Try to get the process binary name first, fall back to application name
		name, ok := info.Properties["application.process.binary"]
		if !ok {
			// Fall back to application.name if process.binary is not available
			name, ok = info.Properties["application.name"]
			if !ok {
				sf.logger.Warnw("Failed to get sink input's process name or application name",
					"sinkInputIndex", info.SinkInputIndex)
				continue
			}
			sf.logger.Debugw("Using application.name as fallback", "name", name.String())
		}

		// No reliable PID from PulseAudio, set to 0
		var pid uint32 = 0

		// create the deej session object
		newSession := newPASession(sf.sessionLogger, sf.client, info.SinkInputIndex, info.Channels, name.String(), pid)

		// add it to our slice
		*sessions = append(*sessions, newSession)
		sf.logger.Debugw("Added sink input session", "name", name.String())
	}

	sf.logger.Debug("Finished enumerateAndAddSessions")
	return nil
}
