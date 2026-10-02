package deej

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"

	"github.com/gen2brain/beeep"
	"go.uber.org/zap"
)

const (
	flatpakInfoPath  = "/.flatpak-info"
	hostEtcGroupPath = "/run/host/etc/group"
	sandboxGroupPath = "/etc/group"

	// access(2) mode bits
	readOK  = 0x4
	writeOK = 0x2
)

// deviceGroup pairs the group id owning a serial device with every name the host's
// group database knows that id by
type deviceGroup struct {
	gid   uint32
	names []string
}

// promptAnswer is the outcome of asking the user whether we should fix their group membership
type promptAnswer int

const (
	promptYes promptAnswer = iota
	promptNo
	promptUnavailable
)

// VerifyDeviceAccess makes sure deej is allowed to open the serial devices attached to this
// machine, and offers to add the user to the group that owns them when it isn't. Everything
// it needs to answer that question - the group database, the user's group membership, usermod
// and pkexec - belongs to the host, so inside a flatpak every lookup is made outside the
// sandbox. It returns without prompting once access is granted, so in practice this only ever
// asks anything on a first run.
func (d *Deej) VerifyDeviceAccess() {
	logger := d.logger.Named("device_access")

	devices, err := serialDevicePaths()
	if err != nil {
		logger.Warnw("Failed to enumerate serial devices", "error", err)
		return
	}

	if len(devices) == 0 {
		logger.Info("No serial devices attached, skipping access verification")
		return
	}

	blocked := []string{}
	for _, device := range devices {
		if syscall.Access(device, readOK|writeOK) != nil {
			blocked = append(blocked, device)
		}
	}

	if len(blocked) == 0 {
		logger.Debugw("All serial devices are accessible", "devices", devices)
		return
	}

	logger.Infow("Serial devices can't be accessed, resolving their owning group(s)",
		"devices", blocked,
		"sandboxed", inFlatpak())

	groups := resolveDeviceGroups(logger, blocked)
	if len(groups) == 0 {
		logger.Warnw("Couldn't determine the group owning any inaccessible serial device", "devices", blocked)
		beeep.Alert("Can't access your USB device",
			fmt.Sprintf("deej isn't allowed to open %s, and the group owning it couldn't be determined.\n\nPlease check deej's logs for more details.",
				strings.Join(blocked, ", ")), "")

		return
	}

	username := hostUsername()
	if username == "" {
		logger.Warn("Couldn't determine the current username, can't offer to fix device permissions")
		return
	}

	memberships, err := hostGroupMemberships(username)
	if err != nil {
		// not fatal - we'd rather ask again than silently do nothing
		logger.Warnw("Failed to read group membership from the host", "user", username, "error", err)
	}

	missing := []string{}
	satisfied := []string{}

	for _, group := range groups {
		if member, name := memberOfAny(memberships, group.names); member {
			satisfied = append(satisfied, name)
			continue
		}

		// any name mapping to the gid works for usermod, so pick the first one the host gave us
		missing = append(missing, group.names[0])
	}

	logger.Infow("Resolved group ownership of inaccessible serial devices",
		"user", username,
		"groups", groups,
		"alreadyMember", satisfied,
		"missing", missing)

	if len(missing) == 0 {
		beeep.Alert("Already a member",
			fmt.Sprintf("You're already a member of the '%s' group, but this session doesn't have it yet.\n\nPlease log out and log back in.",
				strings.Join(satisfied, "', '")), "")

		return
	}

	manualCommand := fmt.Sprintf("sudo usermod -aG %s %s", strings.Join(missing, ","), username)

	question := fmt.Sprintf("deej isn't allowed to open %s.\n\nWould you like to add %s to the '%s' group?\n\nYou will be prompted for your password.",
		strings.Join(blocked, ", "), username, strings.Join(missing, "', '"))

	switch askToFixPermissions(logger, question) {
	case promptYes:
		cmd := hostCommand("pkexec", "usermod", "-aG", strings.Join(missing, ","), username)
		if err := cmd.Run(); err != nil {
			logger.Warnw("Failed to add user to group", "user", username, "groups", missing, "error", err)
			beeep.Alert("Error", "Failed to add you to the group.\n\nPlease run this command manually:\n"+manualCommand, "")

			return
		}

		logger.Infow("Added user to group", "user", username, "groups", missing)
		beeep.Alert("Action Required", "You have been added to the group.\n\nPlease log out and log back in, then rerun this program to continue.", "")

	case promptNo:
		beeep.Alert("Action Cancelled", "No changes were made.\n\nYou can do this yourself later by running:\n"+manualCommand, "")

	default:
		logger.Warn("Couldn't show a confirmation dialog, falling back to a notification")
		beeep.Alert("Can't access your USB device", "Please run this command and then log out and back in:\n"+manualCommand, "")
	}
}

// serialDevicePaths returns the paths of all USB serial devices currently attached
func serialDevicePaths() ([]string, error) {
	entries, err := os.ReadDir("/dev")
	if err != nil {
		return nil, fmt.Errorf("read /dev: %w", err)
	}

	devices := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "ttyUSB") || strings.HasPrefix(entry.Name(), "ttyACM") {
			devices = append(devices, "/dev/"+entry.Name())
		}
	}

	return devices, nil
}

// resolveDeviceGroups maps the given devices to the distinct groups owning them
func resolveDeviceGroups(logger *zap.SugaredLogger, devices []string) []deviceGroup {
	groups := []deviceGroup{}
	seen := map[uint32]bool{}

	for _, device := range devices {
		info, err := os.Stat(device)
		if err != nil {
			logger.Warnw("Failed to stat serial device", "device", device, "error", err)
			continue
		}

		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			logger.Warnw("Serial device has no unix stat information", "device", device)
			continue
		}

		if seen[stat.Gid] {
			continue
		}
		seen[stat.Gid] = true

		names := groupNamesForGID(logger, stat.Gid)
		if len(names) == 0 {
			logger.Warnw("No group name found for serial device owner", "device", device, "gid", stat.Gid)
			continue
		}

		groups = append(groups, deviceGroup{gid: stat.Gid, names: names})
	}

	return groups
}

// groupNamesForGID resolves a gid to its group name(s). The sandbox's own group database
// describes the flatpak runtime rather than the machine deej is running on, so it's only
// consulted as a last resort.
func groupNamesForGID(logger *zap.SugaredLogger, gid uint32) []string {
	gidString := strconv.FormatUint(uint64(gid), 10)

	sources := []struct {
		name    string
		resolve func() []string
	}{
		{"host getent", func() []string {
			return groupNamesFromCommand(hostCommand("getent", "group", gidString), gid)
		}},
		{hostEtcGroupPath, func() []string {
			return groupNamesFromFile(hostEtcGroupPath, gid)
		}},
		{"sandbox getent", func() []string {
			return groupNamesFromCommand(exec.Command("getent", "group", gidString), gid)
		}},
		{sandboxGroupPath, func() []string {
			return groupNamesFromFile(sandboxGroupPath, gid)
		}},
	}

	for idx, source := range sources {
		names := source.resolve()
		if len(names) == 0 {
			continue
		}

		if idx > 1 && inFlatpak() {
			logger.Warnw("Resolved device group from inside the sandbox, it may not match the host",
				"source", source.name, "gid", gid, "names", names)
		} else {
			logger.Debugw("Resolved device group", "source", source.name, "gid", gid, "names", names)
		}

		return names
	}

	return nil
}

func groupNamesFromCommand(cmd *exec.Cmd, gid uint32) []string {
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	return parseGroupDatabase(strings.NewReader(string(output)), gid)
}

func groupNamesFromFile(path string, gid uint32) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	return parseGroupDatabase(file, gid)
}

// parseGroupDatabase returns every group name in a group(5)-formatted stream that maps to gid
func parseGroupDatabase(reader io.Reader, gid uint32) []string {
	names := []string{}

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		if name, ok := groupNameForGIDLine(scanner.Text(), gid); ok {
			names = append(names, name)
		}
	}

	return names
}

// groupNameForGIDLine parses a single "name:password:gid:members" line, returning its
// group name if (and only if) it maps to the given gid
func groupNameForGIDLine(line string, gid uint32) (string, bool) {
	fields := strings.Split(line, ":")
	if len(fields) < 3 {
		return "", false
	}

	parsed, err := strconv.ParseUint(strings.TrimSpace(fields[2]), 10, 32)
	if err != nil || uint32(parsed) != gid {
		return "", false
	}

	name := strings.TrimSpace(fields[0])
	if name == "" {
		return "", false
	}

	return name, true
}

// hostUsername returns the name of the user deej is running as, as the host knows it
func hostUsername() string {
	if output, err := hostCommand("id", "-un").Output(); err == nil {
		if name := strings.TrimSpace(string(output)); name != "" {
			return name
		}
	}

	if current, err := user.Current(); err == nil && current.Username != "" {
		return current.Username
	}

	return os.Getenv("USER")
}

// hostGroupMemberships returns the groups the given user belongs to on the host
func hostGroupMemberships(username string) ([]string, error) {
	output, err := hostCommand("id", "-nG", username).Output()
	if err != nil {
		return nil, fmt.Errorf("list groups for %s: %w", username, err)
	}

	return strings.Fields(string(output)), nil
}

// memberOfAny reports whether any of the given group names appears in memberships, matching
// whole names only so that e.g. "dialout" doesn't match a group named "nodialout"
func memberOfAny(memberships []string, names []string) (bool, string) {
	for _, name := range names {
		for _, membership := range memberships {
			if membership == name {
				return true, name
			}
		}
	}

	return false, ""
}

// askToFixPermissions shows a yes/no dialog, preferring the host's zenity so that the dialog
// shows up even when the flatpak runtime doesn't ship one
func askToFixPermissions(logger *zap.SugaredLogger, question string) promptAnswer {
	args := []string{"--question", "--title", "deej", "--text", question}

	attempts := []*exec.Cmd{hostCommand("zenity", args...)}
	if inFlatpak() {
		attempts = append(attempts, exec.Command("zenity", args...))
	}

	for _, cmd := range attempts {
		err := cmd.Run()
		if err == nil {
			return promptYes
		}

		// zenity exits with 1 when the user declines; anything else means we never got an answer
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return promptNo
		}

		logger.Debugw("Failed to show confirmation dialog", "command", cmd.Path, "error", err)
	}

	return promptUnavailable
}

// inFlatpak reports whether we're running inside a flatpak sandbox
func inFlatpak() bool {
	if _, err := os.Stat(flatpakInfoPath); err == nil {
		return true
	}

	return os.Getenv("FLATPAK_ID") != ""
}

// hostCommand builds a command that runs on the host rather than inside the sandbox.
// Outside of a flatpak this is just the command itself.
func hostCommand(name string, args ...string) *exec.Cmd {
	if !inFlatpak() {
		return exec.Command(name, args...)
	}

	return exec.Command("flatpak-spawn", append([]string{"--host", name}, args...)...)
}
