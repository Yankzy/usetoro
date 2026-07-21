package ses

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

func TestGenerateDKIMKeypair(t *testing.T) {
	svc := &sesService{}
	
	privPEM, pubPEM, err := svc.GenerateDKIMKeypair()
	if err != nil {
		t.Fatalf("GenerateDKIMKeypair failed: %v", err)
	}

	if len(privPEM) == 0 || len(pubPEM) == 0 {
		t.Errorf("Expected non-empty PEM bytes")
	}

	// Verify private key parses
	block, _ := pem.Decode(privPEM)
	if block == nil || block.Type != "RSA PRIVATE KEY" {
		t.Fatalf("Failed to decode PEM block containing RSA PRIVATE KEY")
	}

	privKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("Failed to parse private key: %v", err)
	}

	// Verify public key parses
	pubBlock, _ := pem.Decode(pubPEM)
	if pubBlock == nil || pubBlock.Type != "PUBLIC KEY" {
		t.Fatalf("Failed to decode PEM block containing PUBLIC KEY")
	}

	pubKeyInterface, err := x509.ParsePKIXPublicKey(pubBlock.Bytes)
	if err != nil {
		t.Fatalf("Failed to parse public key: %v", err)
	}

	pubKey, ok := pubKeyInterface.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("Parsed public key is not RSA")
	}

	// Ensure they match
	if privKey.PublicKey.N.Cmp(pubKey.N) != 0 {
		t.Errorf("Public and private keys do not match")
	}
}

func TestGetDNSRecords(t *testing.T) {
	svc := &sesService{}
	
	fakePubKey := []byte("-----BEGIN PUBLIC KEY-----\nMIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQC3...\n-----END PUBLIC KEY-----")
	records := svc.GetDNSRecords("example.com", fakePubKey)

	if len(records) != 3 {
		t.Fatalf("Expected 3 DNS record instruction lines, got %d", len(records))
	}

	if !strings.Contains(records[0], "toro._domainkey.example.com") {
		t.Errorf("Expected Host line to contain toro._domainkey.example.com, got %s", records[0])
	}
	if !strings.Contains(records[1], "TXT") {
		t.Errorf("Expected Type line to be TXT, got %s", records[1])
	}
	if !strings.Contains(records[2], "v=DKIM1; k=rsa;") {
		t.Errorf("Expected Value line to contain DKIM signature format, got %s", records[2])
	}
}
