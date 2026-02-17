package main

import "testing"

func TestParseSSHAddr(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		wantUser string
		wantHost string
	}{
		{"user and host", "ubuntu@host.example.com", "ubuntu", "host.example.com:22"},
		{"ip only", "10.0.0.1", "root", "10.0.0.1:22"},
		{"user and ip with port", "deploy@10.0.0.1:2222", "deploy", "10.0.0.1:2222"},
		{"host with port", "host.example.com:2222", "root", "host.example.com:2222"},
		{"user and ip", "admin@192.168.1.1", "admin", "192.168.1.1:22"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, host := parseSSHAddr(tt.addr)
			if user != tt.wantUser {
				t.Errorf("user = %q, want %q", user, tt.wantUser)
			}
			if host != tt.wantHost {
				t.Errorf("host = %q, want %q", host, tt.wantHost)
			}
		})
	}
}
