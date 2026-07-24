package cli

import "testing"

func TestVersionIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"0.1.3", "0.1.2", true},
		{"0.1.2", "0.1.2", false},
		{"0.1.1", "0.1.2", false},
		{"v0.2.0", "0.1.9", true},
		{"0.1.2", "dev", true},
		{"0.1.2", "dev+abc1234", true},
		{"0.1.2", "dev+abc1234-dirty", true},
		{"1.0.0", "0.9.9", true},
		{"0.10.0", "0.9.0", true},
		{"0.9.0", "0.10.0", false},
		{"0.1.2", "v0.1.2", false},
		{"", "0.1.0", false},
		{"0.1.0", "", true},
	}
	for _, tc := range cases {
		got := versionIsNewer(tc.latest, tc.current)
		if got != tc.want {
			t.Errorf("versionIsNewer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"2.0.0", "1.9.9", 1},
		{"0.10.0", "0.9.0", 1},
		{"1.2", "1.2.0", 0},
		{"1.2.3-rc.1", "1.2.3", 0}, // numeric core only
		{"v1.2.3", "1.2.3", 0},
	}
	for _, tc := range cases {
		got := compareSemver(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestIsReleaseVersion(t *testing.T) {
	yes := []string{"0.1.0", "v1.2.3", "0.0.0-20240101120000-abcdef"}
	no := []string{"", "dev", "dev+abc", "dev-dirty", "(devel)"}
	for _, v := range yes {
		if !isReleaseVersion(v) {
			t.Errorf("isReleaseVersion(%q) = false, want true", v)
		}
	}
	for _, v := range no {
		if isReleaseVersion(v) {
			t.Errorf("isReleaseVersion(%q) = true, want false", v)
		}
	}
}
