package deej

import "testing"

func TestIsAutoCOMPort(t *testing.T) {
	cases := map[string]bool{
		"":             true,
		"   ":          true,
		"auto":         true,
		"Auto":         true,
		"AUTO":         true,
		"/dev/ttyACM0": false,
		"COM4":         false,
	}

	for comPort, expected := range cases {
		if actual := isAutoCOMPort(comPort); actual != expected {
			t.Fatalf("isAutoCOMPort(%q) = %v, expected %v", comPort, actual, expected)
		}
	}
}
