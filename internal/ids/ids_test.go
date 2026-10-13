package ids

import "testing"

func TestNewPublicIDShapeAndUniqueness(t *testing.T) {
	seen := make(map[string]bool, 2000)
	for i := 0; i < 2000; i++ {
		id := NewPublicID()
		if !IsPublicID(id) {
			t.Fatalf("NewPublicID returned %q, which IsPublicID rejects", id)
		}
		if seen[id] {
			t.Fatalf("duplicate identifier %q after %d iterations", id, i)
		}
		seen[id] = true
	}
}

func TestIsPublicIDRejectsInvalidInput(t *testing.T) {
	cases := []string{
		"",
		"abc",
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ",  // uppercase
		"abcdefghijklmnopqrstuvwxy1",  // '1' is not in the alphabet
		"abcdefghijklmnopqrstuvwxyz2", // too long
		"abcdefghijklm/opqrstuvwxyz",
		"1 OR 1=1; --aaaaaaaaaaaaaa",
	}
	for _, c := range cases {
		if IsPublicID(c) {
			t.Errorf("IsPublicID(%q) = true, want false", c)
		}
	}
}

func TestNewTokenLength(t *testing.T) {
	if got := len(NewToken()); got != 2*PublicIDLength {
		t.Fatalf("token length = %d, want %d", got, 2*PublicIDLength)
	}
}
