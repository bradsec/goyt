package core

import "testing"

func TestParseBytesField(t *testing.T) {
	cases := map[string]int64{
		"1048576": 1048576,
		"  2048 ": 2048,
		"1.5e3":   1500,
		"NA":      -1,
		"None":    -1,
		"":        -1,
		"abc":     -1,
	}
	for in, want := range cases {
		if got := parseBytesField(in); got != want {
			t.Errorf("parseBytesField(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestCleanTemplateField(t *testing.T) {
	cases := map[string]string{
		"  2.47MiB/s": "2.47MiB/s",
		"NA":          "",
		"None":        "",
		"00:42":       "00:42",
	}
	for in, want := range cases {
		if got := cleanTemplateField(in); got != want {
			t.Errorf("cleanTemplateField(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{
		512:         "512 B",
		1536:        "1.5 KB",
		5 * 1 << 20: "5.0 MB",
		3 * 1 << 30: "3.0 GB",
	}
	for in, want := range cases {
		if got := formatBytes(in); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestHandleTemplateProgress(t *testing.T) {
	ch := make(chan DownloadProgress, 4)
	d := NewDownloader("yt-dlp", "ffmpeg", "", true, false)
	m := d.createProgressMonitor("test", ch, nil)

	// downloaded|total|estimate|speed|eta, with yt-dlp-style padding.
	m.handleTemplateProgress("524288000|1048576000|1048576000| 2.47MiB/s |01:23")

	select {
	case p := <-ch:
		if p.Phase != "downloading" {
			t.Errorf("Phase = %q, want downloading", p.Phase)
		}
		if p.Percentage < 49.9 || p.Percentage > 50.1 {
			t.Errorf("Percentage = %.2f, want ~50", p.Percentage)
		}
		if p.Size != "1000.0 MB" {
			t.Errorf("Size = %q, want 1000.0 MB", p.Size)
		}
		if p.Speed != "2.47MiB/s" {
			t.Errorf("Speed = %q, want 2.47MiB/s", p.Speed)
		}
		if p.ETA != "01:23" {
			t.Errorf("ETA = %q, want 01:23", p.ETA)
		}
	default:
		t.Fatal("expected a progress update, got none")
	}
}

// When total_bytes is unknown ("NA"), the estimate should be used for the size
// and percentage so the bar stays determinate instead of falling back to a spinner.
func TestHandleTemplateProgressUsesEstimate(t *testing.T) {
	ch := make(chan DownloadProgress, 4)
	d := NewDownloader("yt-dlp", "ffmpeg", "", true, false)
	m := d.createProgressMonitor("test", ch, nil)

	m.handleTemplateProgress("262144000|NA|1048576000|1.00MiB/s|02:00")

	select {
	case p := <-ch:
		if p.Percentage < 24.9 || p.Percentage > 25.1 {
			t.Errorf("Percentage = %.2f, want ~25", p.Percentage)
		}
		if p.Size != "1000.0 MB" {
			t.Errorf("Size = %q, want 1000.0 MB", p.Size)
		}
	default:
		t.Fatal("expected a progress update, got none")
	}
}
