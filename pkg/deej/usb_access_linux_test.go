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
	names := parseGroupDatabase(strings.NewReader(testGroupDatabase), 20)

	if len(names) != 2 || names[0] != "dialout" || names[1] != "uucp" {
		t.Fatalf("expected [dialout uucp] for gid 20, got %v", names)
	}
}

func TestParseGroupDatabaseSkipsEmptyAndMalformedEntries(t *testing.T) {
	names := parseGroupDatabase(strings.NewReader(testGroupDatabase), 46)

	if len(names) != 1 || names[0] != "plugdev" {
		t.Fatalf("expected [plugdev] for gid 46, got %v", names)
	}
}

func TestParseGroupDatabaseReturnsNothingForUnknownGID(t *testing.T) {
	if names := parseGroupDatabase(strings.NewReader(testGroupDatabase), 1234); len(names) != 0 {
		t.Fatalf("expected no names for an unknown gid, got %v", names)
	}
}

func TestGroupNameForGIDLine(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		gid      uint32
		expected string
		matches  bool
	}{
		{"matching entry", "dialout:x:20:someone", 20, "dialout", true},
		{"non-matching gid", "dialout:x:20:someone", 21, "", false},
		{"gid is a prefix of the line's gid", "weird:x:200:", 20, "", false},
		{"too few fields", "truncated:x", 20, "", false},
		{"non-numeric gid", "broken:x:twenty:", 20, "", false},
		{"empty name", ":x:20:", 20, "", false},
		{"surrounding whitespace", " dialout:x: 20 :", 20, "dialout", true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			name, ok := groupNameForGIDLine(testCase.line, testCase.gid)

			if ok != testCase.matches || name != testCase.expected {
				t.Fatalf("expected (%q, %v), got (%q, %v)", testCase.expected, testCase.matches, name, ok)
			}
		})
	}
}

func TestMemberOfAnyMatchesWholeNamesOnly(t *testing.T) {
	memberships := []string{"deej-user", "nodialout", "wheel"}

	if member, _ := memberOfAny(memberships, []string{"dialout", "uucp"}); member {
		t.Fatal("expected 'dialout' not to match the 'nodialout' group")
	}

	member, matched := memberOfAny(memberships, []string{"dialout", "wheel"})
	if !member || matched != "wheel" {
		t.Fatalf("expected to match 'wheel', got (%v, %q)", member, matched)
	}
}

func TestMemberOfAnyWithNoMemberships(t *testing.T) {
	if member, _ := memberOfAny(nil, []string{"dialout"}); member {
		t.Fatal("expected no match against an empty membership list")
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
