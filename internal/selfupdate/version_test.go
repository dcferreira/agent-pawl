package selfupdate

import "testing"

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantMaj int
		wantMin int
		wantPat int
		wantErr bool
	}{
		{"plain", "1.2.3", 1, 2, 3, false},
		{"leading v", "v1.2.3", 1, 2, 3, false},
		{"prerelease suffix ignored for parse", "1.2.3-rc1", 1, 2, 3, false},
		{"dev is unparseable", "dev", 0, 0, 0, true},
		{"empty is unparseable", "", 0, 0, 0, true},
		{"missing patch", "1.2", 0, 0, 0, true},
		{"non numeric", "a.b.c", 0, 0, 0, true},
		// Security: these must all be rejected, not just parsed
		// "somehow" — internal/cli's validatePin relies on ParseVersion
		// to keep a malicious --version value out of a download URL path
		// (archive AND checksums.txt are fetched from the same
		// attacker-controlled tag, so a traversal here defeats the
		// checksum check too).
		{"traversal via prerelease slash", "v1.2.3-/../../../other/repo/releases/download/v9.9.9", 0, 0, 0, true},
		{"prerelease with query/fragment-shaped chars", "1.2.3-x?y=1#frag", 0, 0, 0, true},
		{"leading plus on every component", "+1.+2.+3", 0, 0, 0, true},
		{"leading zero", "01.2.3", 0, 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := ParseVersion(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseVersion(%q) = %+v, want error", tt.in, v)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseVersion(%q) unexpected error: %v", tt.in, err)
			}
			if v.Major != tt.wantMaj || v.Minor != tt.wantMin || v.Patch != tt.wantPat {
				t.Errorf("ParseVersion(%q) = %+v, want %d.%d.%d", tt.in, v, tt.wantMaj, tt.wantMin, tt.wantPat)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name    string
		a, b    string
		want    int
		wantErr bool
	}{
		{"equal", "1.2.3", "1.2.3", 0, false},
		{"equal with v prefix mixed", "v1.2.3", "1.2.3", 0, false},
		{"a older major", "1.2.3", "2.0.0", -1, false},
		{"a newer major", "2.0.0", "1.9.9", 1, false},
		{"a older minor", "1.2.3", "1.3.0", -1, false},
		{"a older patch", "1.2.3", "1.2.4", -1, false},
		{"a newer patch", "1.2.4", "1.2.3", 1, false},
		{"prerelease is older than release", "1.2.3-rc1", "1.2.3", -1, false},
		{"release is newer than prerelease", "1.2.3", "1.2.3-rc1", 1, false},
		{"dev unparseable errors", "dev", "1.2.3", 0, true},
		{"other unparseable errors", "1.2.3", "dev", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CompareVersions(tt.a, tt.b)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("CompareVersions(%q, %q) = %d, want error", tt.a, tt.b, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("CompareVersions(%q, %q) unexpected error: %v", tt.a, tt.b, err)
			}
			if got != tt.want {
				t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestNormalizeTag(t *testing.T) {
	tests := []struct{ in, want string }{
		{"0.2.0", "v0.2.0"},
		{"v0.2.0", "v0.2.0"},
	}
	for _, tt := range tests {
		if got := NormalizeTag(tt.in); got != tt.want {
			t.Errorf("NormalizeTag(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
