package main

import (
	"crypto/x509"
	"testing"
)

func TestShouldUseLocalHealthCertificate(t *testing.T) {
	cases := []struct {
		name, serverName, remoteAddr string
		want                         bool
	}{
		{"old watchdog without SNI", "", "127.0.0.1:12345", true},
		{"old watchdog IPv6", "", "[::1]:12345", true},
		{"loopback SNI", "127.0.0.1", "127.0.0.1:12345", true},
		{"public client without SNI", "", "203.0.113.10:12345", false},
		{"normal domain from loopback", "panel.example.com", "127.0.0.1:12345", false},
		{"public IP SNI from public client", "207.246.123.42", "203.0.113.10:12345", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldUseLocalHealthCertificate(tc.serverName, tc.remoteAddr); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGenerateLocalHealthCertificate(t *testing.T) {
	cert, err := generateLocalHealthCertificate()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := parsed.VerifyHostname("127.0.0.1"); err != nil {
		t.Fatalf("certificate does not cover loopback: %v", err)
	}
}
