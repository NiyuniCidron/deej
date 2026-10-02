package deej

import (
	"strings"
	"testing"
)

const testGroupDatabase = `root:x:0:
# a comment, which group(5) files don't really have but let's not choke on one
dialout:x:20:someone,deej-user
uucp:x:20:
plugdev:x:46:deej-user
nodialout:x:1000:
malformed-line
truncated:x
:x:46:
`

func TestParseGroupDatabaseReturnsEveryNameForGID(t *testing.T) {
	database := parseGroupDatabase(strings.NewReader(testGroupDatabase))
	names := database[20]

	if len(names) != 2 || names[0] != "dialout" || names[1] != "uucp" {
		t.Fatalf("expected [dialout uucp] for gid 20, got %v", names)
	}
}

func TestParseGroupDatabaseSkipsEmptyAndMalformedEntries(t *testing.T) {
	database := parseGroupDatabase(strings.NewReader(testGroupDatabase))
	names := database[46]

	if len(names) != 1 || names[0] != "plugdev" {
		t.Fatalf("expected [plugdev] for gid 46, got %v", names)
	}
}

func TestParseGroupDatabaseReturnsNothingForUnknownGID(t *testing.T) {
	database := parseGroupDatabase(strings.NewReader(testGroupDatabase))

	if names := database[1234]; len(names) != 0 {
		t.Fatalf("expected no names for an unknown gid, got %v", names)
	}
}

func TestFirstMembershipMatchesWholeNamesOnly(t *testing.T) {
	memberships := []string{"deej-user", "nodialout", "wheel"}

	if _, member := firstMembership(memberships, []string{"dialout", "uucp"}); member {
		t.Fatal("expected 'dialout' not to match the 'nodialout' group")
	}

	matched, member := firstMembership(memberships, []string{"dialout", "wheel"})
	if !member || matched != "wheel" {
		t.Fatalf("expected to match 'wheel', got (%v, %q)", member, matched)
	}
}

func TestFirstMembershipWithNoMemberships(t *testing.T) {
	if _, member := firstMembership(nil, []string{"dialout"}); member {
		t.Fatal("expected no match against an empty membership list")
	}
}

func TestParseDeviceGIDs(t *testing.T) {
	output := "/dev/ttyUSB0 20\n/dev/ttyACM0 5\n"
	gids := parseDeviceGIDs(output)

	if gids["/dev/ttyUSB0"] != 20 || gids["/dev/ttyACM0"] != 5 {
		t.Fatalf("unexpected gid map: %v", gids)
	}
}

func TestHostCommandRunsDirectlyOutsideFlatpak(t *testing.T) {
	if inFlatpak() {
		t.Skip("this test only describes the non-sandboxed case")
	}

	cmd := hostCommand("getent", "group", "20")

	if len(cmd.Args) != 3 || cmd.Args[0] != "getent" {
		t.Fatalf("expected the command to run as-is, got %v", cmd.Args)
	}
}
