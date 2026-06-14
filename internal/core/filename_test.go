package core

import "testing"

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain ascii lowercased", "Alpha Bravo.mp4", "alpha bravo.mp4"},
		{"strips emoji and symbols", "Alpha \U0001F525\U0001F525 [Bravo]!.mp4", "alpha bravo.mp4"},
		{"drops non-latin and collapses spaces", "Alpha (éèê) x Bravo 'Charlie'.mp4", "alpha x bravo charlie.mp4"},
		{"keeps digits", "Alpha 10 Bravo 2026.mp4", "alpha 10 bravo 2026.mp4"},
		{"empty after strip falls back", "\U0001F525\U0001F389.mp4", "download.mp4"},
		{"uppercase extension lowercased", "Alpha.MP4", "alpha.mp4"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SanitizeFilename(c.in)
			if got != c.want {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
