package deej

import "testing"

func TestPortSatisfiesConfig(t *testing.T) {
	cases := map[string]struct {
		configured string
		connected  string
		expected   bool
	}{
		"auto with a connected port":   {configured: "auto", connected: "/dev/ttyACM0", expected: true},
		"empty config with connection": {configured: "", connected: "/dev/ttyACM0", expected: true},
		"exact match":                  {configured: "COM4", connected: "COM4", expected: true},
		"mismatch":                     {configured: "COM4", connected: "COM5", expected: false},
		"nothing connected yet":        {configured: "auto", connected: "", expected: false},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if actual := portSatisfiesConfig(testCase.configured, testCase.connected); actual != testCase.expected {
				t.Fatalf("portSatisfiesConfig(%q, %q) = %v, expected %v",
					testCase.configured, testCase.connected, actual, testCase.expected)
			}
		})
	}
}
