package main

import "testing"

func TestSubnetHosts(t *testing.T) {
	tests := []struct {
		cidr      string
		wantFirst string
		wantLast  string
		wantCount int
	}{
		{"192.168.8.0/24", "192.168.8.1", "192.168.8.254", 254},
		{"192.168.8.37/24", "192.168.8.1", "192.168.8.254", 254}, // host bits are masked off
		{"10.0.0.4/30", "10.0.0.5", "10.0.0.6", 2},
	}
	for _, tt := range tests {
		hosts, err := subnetHosts(tt.cidr)
		if err != nil {
			t.Fatalf("subnetHosts(%q): %v", tt.cidr, err)
		}
		if len(hosts) != tt.wantCount {
			t.Errorf("subnetHosts(%q): got %d hosts, want %d", tt.cidr, len(hosts), tt.wantCount)
		}
		if got := hosts[0].String(); got != tt.wantFirst {
			t.Errorf("subnetHosts(%q): first host %s, want %s", tt.cidr, got, tt.wantFirst)
		}
		if got := hosts[len(hosts)-1].String(); got != tt.wantLast {
			t.Errorf("subnetHosts(%q): last host %s, want %s", tt.cidr, got, tt.wantLast)
		}
	}
}

func TestSubnetHostsRejects(t *testing.T) {
	for _, cidr := range []string{"10.0.0.0/8", "2001:db8::/64", "not-a-subnet", "192.168.8.1/32"} {
		if _, err := subnetHosts(cidr); err == nil {
			t.Errorf("subnetHosts(%q): nil error, want rejection", cidr)
		}
	}
}

func TestBuildStreamURL(t *testing.T) {
	got := buildStreamURL("192.168.8.58", 554, "1")
	want := "rtsp://192.168.8.58:554/user=admin_password=_channel=0_stream=1.sdp"
	if got != want {
		t.Errorf("buildStreamURL = %q, want %q", got, want)
	}
}

func TestParseNameMapping(t *testing.T) {
	ip, name, err := parseNameMapping("192.168.8.58=area.jpg")
	if err != nil {
		t.Fatalf("parseNameMapping: %v", err)
	}
	if ip != "192.168.8.58" || name != "area.jpg" {
		t.Errorf("parseNameMapping = %q, %q; want 192.168.8.58, area.jpg", ip, name)
	}

	if _, name, err := parseNameMapping("192.168.8.58=../../tmp/entrance.jpg"); err != nil || name != "entrance.jpg" {
		t.Errorf("path traversal not flattened: name=%q err=%v", name, err)
	}

	for _, spec := range []string{"192.168.8.58", "=x.jpg", "not-an-ip=x.jpg", "192.168.8.58="} {
		if _, _, err := parseNameMapping(spec); err == nil {
			t.Errorf("parseNameMapping(%q): nil error, want rejection", spec)
		}
	}
}
