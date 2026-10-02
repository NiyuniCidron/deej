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

	"go.uber.org/zap"
)

const (
	// flatpakInfoPath exists in every flatpak sandbox
	flatpakInfoPath = "/.flatpak-info"

	// hostGroupDatabasePath is where a flatpak can read the host's group database, when
	// the host's /etc is exposed to the sandbox
	hostGroupDatabasePath = "/run/host/etc/group"

	// localGroupDatabasePath is the group database of whatever filesystem we're running on.
	// Inside a sandbox that's the flatpak runtime's, not the machine's
	localGroupDatabasePath = "/etc/group"

	// mode bits for access(2)
	accessRead  = 0x4
	accessWrite = 0x2

	// the gid the kernel reports for groups that aren't mapped into our user namespace,
	// unless the machine says otherwise
	defaultOverflowGID = 65534
)

// serialDevicePrefixes are the /dev entry names a deej board shows up as
var serialDevicePrefixes = []string{"ttyUSB", "ttyACM"}

// deviceGroup is a group owning at least one serial device deej isn't allowed to open.
// A single gid can have more than one name in the group database, and any of them can be
// handed to usermod
type deviceGroup struct {
	gid   uint32
	names []string
}

// name returns the name to use when changing group membership
func (dg deviceGroup) name() string {
	return dg.names[0]
}

// String describes the group the way the user should see it
func (dg deviceGroup) String() string {
	return strings.Join(dg.names, "/")
}

// groupDatabase maps group ids to every name a group(5)-formatted database knows them by
type groupDatabase map[uint32][]string

// promptAnswer is the outcome of asking the user whether deej should fix their membership
type promptAnswer int

const (
	promptYes promptAnswer = iota
	promptNo
	promptUnavailable
)

// verifyDeviceAccess makes sure deej is allowed to open the USB serial devices attached to
// this machine, and offers to add the user to the group owning them when it isn't.
//
// Everything needed to answer that question - which gid owns the device, the names that gid
// goes by, the user's own groups, and usermod itself - describes the machine deej runs on,
// not the flatpak runtime it might be running inside. A sandbox has its own group database
// and maps only our own gid into its user namespace, so asking it yields a single group that
// doesn't exist locally (usually 'nogroup'). Every lookup below is therefore made against
// the host.
//
// This does nothing and says nothing once access is granted, so in practice it only prompts
// on a first run.
func (d *Deej) verifyDeviceAccess() {
	logger := d.logger.Named("device_access")

	devices, err := serialDevices()
	if err != nil {
		logger.Warnw("Failed to enumerate serial devices", "error", err)
		return
	}

	// a sandbox only sees the device nodes that were shared into it, so an empty /dev means
	// the flatpak is missing its device permission rather than the machine having no board
	if len(devices) == 0 {
		if hidden := hostSerialDevices(logger); len(hidden) > 0 {
			logger.Warnw("The host has serial devices that aren't visible in the sandbox",
				"devices", hidden)

			d.notifier.Notify("Can't see your USB device",
				fmt.Sprintf("%s exists on this machine but isn't shared with deej's sandbox. Please grant it device access (flatpak override --device=all).",
					strings.Join(hidden, ", ")))

			return
		}

		logger.Info("No serial devices attached, nothing to verify")
		return
	}

	inaccessible := []string{}
	for _, device := range devices {
		if err := syscall.Access(device, accessRead|accessWrite); err != nil {
			inaccessible = append(inaccessible, device)
		}
	}

	if len(inaccessible) == 0 {
		logger.Debugw("All serial devices are accessible", "devices", devices)
		return
	}

	logger.Infow("Serial devices can't be opened, resolving the group(s) owning them",
		"devices", inaccessible,
		"sandboxed", inFlatpak())

	groups := deviceGroups(logger, inaccessible)
	if len(groups) == 0 {
		d.notifier.Notify("Can't access your USB device",
			fmt.Sprintf("deej isn't allowed to open %s, and couldn't work out which group owns it. Please check deej's logs.",
				strings.Join(inaccessible, ", ")))

		return
	}

	username := hostUsername(logger)
	if username == "" {
		logger.Warn("Couldn't determine the current username, not offering to change group membership")
		return
	}

	memberships, err := hostGroupMemberships(username)
	if err != nil {
		// not fatal: we'd rather offer a change that turns out to be redundant than do nothing
		logger.Warnw("Failed to read group membership from the host", "user", username, "error", err)
	}

	missing := []string{}
	satisfied := []string{}
	resolved := []string{}

	for _, group := range groups {
		resolved = append(resolved, group.String())

		if name, member := firstMembership(memberships, group.names); member {
			satisfied = append(satisfied, name)
			continue
		}

		missing = append(missing, group.name())
	}

	logger.Infow("Resolved the group ownership of inaccessible serial devices",
		"user", username,
		"groups", resolved,
		"alreadyMember", satisfied,
		"missing", missing)

	// the membership is already there, it just isn't part of this login session yet
	if len(missing) == 0 {
		d.notifier.Notify("Almost there!",
			fmt.Sprintf("You're already in the '%s' group, but this session doesn't have it yet. Please log out and back in.",
				strings.Join(satisfied, "', '")))

		return
	}

	manualCommand := fmt.Sprintf("sudo usermod -aG %s %s", strings.Join(missing, ","), username)

	question := fmt.Sprintf("deej isn't allowed to open %s.\n\nWould you like to add %s to the '%s' group?\n\nYou will be asked for your password.",
		strings.Join(inaccessible, ", "), username, strings.Join(missing, "', '"))

	switch askToChangeGroups(logger, question) {
	case promptYes:
		if err := hostCommand("pkexec", "usermod", "-aG", strings.Join(missing, ","), username).Run(); err != nil {
			logger.Warnw("Failed to add user to group", "user", username, "groups", missing, "error", err)

			d.notifier.Notify("Couldn't change your groups",
				"Please run this yourself and then log out and back in:\n"+manualCommand)

			return
		}

		logger.Infow("Added user to group", "user", username, "groups", missing)

		d.notifier.Notify("Almost there!",
			fmt.Sprintf("You've been added to the '%s' group. Please log out and back in, then start deej again.",
				strings.Join(missing, "', '")))

	case promptNo:
		logger.Info("User declined the group change")

		d.notifier.Notify("No changes were made",
			"You can grant deej access yourself later by running:\n"+manualCommand)

	default:
		logger.Warn("Couldn't ask the user anything, falling back to a notification")

		d.notifier.Notify("Can't access your USB device",
			"Please run this and then log out and back in:\n"+manualCommand)
	}
}

// serialDevices returns the paths of the USB serial devices deej can see
func serialDevices() ([]string, error) {
	entries, err := os.ReadDir("/dev")
	if err != nil {
		return nil, fmt.Errorf("read /dev: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return serialDevicePaths(names), nil
}

// hostSerialDevices returns the USB serial devices the machine has, which inside a sandbox
// can differ from the ones deej can see
func hostSerialDevices(logger *zap.SugaredLogger) []string {
	if !inFlatpak() {
		return nil
	}

	output, err := hostCommand("ls", "-1", "/dev").Output()
	if err != nil {
		logger.Debugw("Failed to list the host's devices", "error", err)
		return nil
	}

	return serialDevicePaths(strings.Fields(string(output)))
}

// serialDevicePaths picks the USB serial devices out of a list of /dev entry names
func serialDevicePaths(entries []string) []string {
	devices := []string{}

	for _, entry := range entries {
		for _, prefix := range serialDevicePrefixes {
			if strings.HasPrefix(entry, prefix) {
				devices = append(devices, "/dev/"+entry)
				break
			}
		}
	}

	return devices
}

// deviceGroups returns the distinct groups owning the given devices, named as the machine
// deej runs on names them
func deviceGroups(logger *zap.SugaredLogger, devices []string) []deviceGroup {
	gids := deviceGIDs(logger, devices)
	if len(gids) == 0 {
		logger.Warnw("Couldn't determine the owner of any inaccessible serial device", "devices", devices)
		return nil
	}

	database := hostGroupDatabase(logger)
	if len(database) == 0 {
		logger.Warn("Couldn't read the group database of the machine deej is running on")
		return nil
	}

	groups := []deviceGroup{}
	seen := map[uint32]bool{}

	for _, device := range devices {
		gid, ok := gids[device]
		if !ok || seen[gid] {
			continue
		}
		seen[gid] = true

		// a gid without a local name is no use to usermod, and guessing one is how deej
		// ended up naming a group that didn't exist on this machine
		names, ok := database[gid]
		if !ok {
			logger.Warnw("The device's owning group doesn't exist on this machine",
				"device", device, "gid", gid)

			continue
		}

		logger.Debugw("Resolved the group owning a serial device",
			"device", device, "gid", gid, "names", names)

		groups = append(groups, deviceGroup{gid: gid, names: names})
	}

	return groups
}

// deviceGIDs returns the group id owning each of the given devices
func deviceGIDs(logger *zap.SugaredLogger, devices []string) map[string]uint32 {
	if inFlatpak() {
		return hostDeviceGIDs(logger, devices)
	}

	overflow := overflowGID(logger)
	gids := map[string]uint32{}

	for _, device := range devices {
		info, err := os.Stat(device)
		if err != nil {
			logger.Warnw("Failed to stat serial device", "device", device, "error", err)
			continue
		}

		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}

		if stat.Gid == overflow {
			logger.Warnw("Serial device is owned by the overflow group, ignoring it",
				"device", device, "gid", stat.Gid)

			continue
		}

		gids[device] = stat.Gid
	}

	return gids
}

// hostDeviceGIDs asks the host which group owns each device. A sandbox maps only our own
// gid into its user namespace, so stat(2) in here reports the overflow gid for every device
// we don't own - the number itself is wrong, not just the name it looks up to
func hostDeviceGIDs(logger *zap.SugaredLogger, devices []string) map[string]uint32 {
	output, err := hostCommand("stat", append([]string{"-c", "%n %g"}, devices...)...).Output()
	if len(output) == 0 {
		logger.Warnw("Couldn't ask the host which group owns these devices - is the flatpak "+
			"missing --talk-name=org.freedesktop.Flatpak?",
			"devices", devices, "error", err)

		return nil
	}

	return parseDeviceGIDs(string(output))
}

// parseDeviceGIDs reads the output of `stat -c "%n %g"` over several files
func parseDeviceGIDs(output string) map[string]uint32 {
	gids := map[string]uint32{}

	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}

		gid, err := strconv.ParseUint(fields[1], 10, 32)
		if err != nil {
			continue
		}

		gids[fields[0]] = uint32(gid)
	}

	return gids
}

// overflowGID is the gid the kernel substitutes for groups outside our user namespace
func overflowGID(logger *zap.SugaredLogger) uint32 {
	contents, err := os.ReadFile("/proc/sys/kernel/overflowgid")
	if err != nil {
		return defaultOverflowGID
	}

	gid, err := strconv.ParseUint(strings.TrimSpace(string(contents)), 10, 32)
	if err != nil {
		logger.Debugw("Failed to parse the kernel's overflow gid, assuming the default", "error", err)
		return defaultOverflowGID
	}

	return uint32(gid)
}

// hostGroupDatabase loads the group database of the machine deej runs on. Inside a sandbox
// the local /etc/group describes the flatpak runtime, so it's deliberately left out: a group
// resolved from it wouldn't exist on the machine whose permissions we're about to change
func hostGroupDatabase(logger *zap.SugaredLogger) groupDatabase {
	sources := []struct {
		name string
		load func() (groupDatabase, error)
	}{
		{"getent group", func() (groupDatabase, error) {
			return groupDatabaseFromCommand(hostCommand("getent", "group"))
		}},
		{hostGroupDatabasePath, func() (groupDatabase, error) {
			return groupDatabaseFromFile(hostGroupDatabasePath)
		}},
	}

	if !inFlatpak() {
		sources = append(sources, struct {
			name string
			load func() (groupDatabase, error)
		}{localGroupDatabasePath, func() (groupDatabase, error) {
			return groupDatabaseFromFile(localGroupDatabasePath)
		}})
	}

	for _, source := range sources {
		database, err := source.load()
		if err != nil {
			logger.Debugw("Failed to read a group database", "source", source.name, "error", err)
			continue
		}

		if len(database) == 0 {
			continue
		}

		logger.Debugw("Read the group database of the machine deej is running on",
			"source", source.name, "groups", len(database))

		return database
	}

	return nil
}

func groupDatabaseFromCommand(cmd *exec.Cmd) (groupDatabase, error) {
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run %s: %w", cmd.Path, err)
	}

	return parseGroupDatabase(strings.NewReader(string(output))), nil
}

func groupDatabaseFromFile(path string) (groupDatabase, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	return parseGroupDatabase(file), nil
}

// parseGroupDatabase reads a group(5)-formatted stream of "name:password:gid:members" lines
func parseGroupDatabase(reader io.Reader) groupDatabase {
	database := groupDatabase{}

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ":")
		if len(fields) < 3 {
			continue
		}

		name := strings.TrimSpace(fields[0])
		if name == "" {
			continue
		}

		gid, err := strconv.ParseUint(strings.TrimSpace(fields[2]), 10, 32)
		if err != nil {
			continue
		}

		database[uint32(gid)] = append(database[uint32(gid)], name)
	}

	return database
}

// hostUsername returns the name of the user deej runs as, as the host knows it
func hostUsername(logger *zap.SugaredLogger) string {
	output, err := hostCommand("id", "-un").Output()
	if err == nil {
		if username := strings.TrimSpace(string(output)); username != "" {
			return username
		}
	} else {
		logger.Debugw("Failed to ask the host for our username", "error", err)
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
		return nil, fmt.Errorf("list the groups of %s: %w", username, err)
	}

	return strings.Fields(string(output)), nil
}

// firstMembership returns the first of the given names the user is a member of. Names are
// compared whole, so being in 'nodialout' isn't mistaken for being in 'dialout'
func firstMembership(memberships []string, names []string) (string, bool) {
	for _, name := range names {
		for _, membership := range memberships {
			if membership == name {
				return name, true
			}
		}
	}

	return "", false
}

// askToChangeGroups puts a yes/no question to the user, preferring the host's dialog
// binaries since the flatpak runtime doesn't ship any
func askToChangeGroups(logger *zap.SugaredLogger, question string) promptAnswer {
	attempts := []*exec.Cmd{
		hostCommand("zenity", "--question", "--title", "deej", "--text", question),
		hostCommand("kdialog", "--title", "deej", "--yesno", question),
	}

	for _, cmd := range attempts {
		err := cmd.Run()
		if err == nil {
			return promptYes
		}

		// both dialogs exit with 1 when the user says no; anything else means we never
		// got an answer out of them
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return promptNo
		}

		logger.Debugw("Failed to show a confirmation dialog", "command", cmd.Args, "error", err)
	}

	return promptUnavailable
}

// inFlatpak reports whether deej is running inside a flatpak sandbox
func inFlatpak() bool {
	if _, err := os.Stat(flatpakInfoPath); err == nil {
		return true
	}

	return os.Getenv("FLATPAK_ID") != ""
}

// hostCommand builds a command that runs on the machine deej is installed on rather than
// inside its sandbox. Outside of a flatpak that's just the command itself
func hostCommand(name string, args ...string) *exec.Cmd {
	if !inFlatpak() {
		return exec.Command(name, args...)
	}

	return exec.Command("flatpak-spawn", append([]string{"--host", name}, args...)...)
}
