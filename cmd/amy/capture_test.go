package main

import "testing"

func TestCaptureWorkspaceTag(t *testing.T) {
	for _, tc := range []struct{ text, tag, want string }{
		{"Water seedlings", "garden", "Water seedlings #garden"},
		{"Water seedlings #Garden", "garden", "Water seedlings #Garden"},
		{"Water seedlings #gardening", "garden", "Water seedlings #gardening #garden"},
		{"Water seedlings", "", "Water seedlings"},
	} {
		if got := captureText(tc.text, tc.tag); got != tc.want {
			t.Errorf("capture = %q want %q", got, tc.want)
		}
	}
}
