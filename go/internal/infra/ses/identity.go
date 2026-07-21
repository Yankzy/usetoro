package ses

import (
	"crypto/rsa"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"context"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/aws-sdk-go-v2/aws"
)

// GenerateDKIMKeypair generates a new RSA 2048-bit keypair for BYO-DKIM
func (s *sesService) GenerateDKIMKeypair() (privateKeyPEM, publicKeyPEM []byte, err error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate RSA key: %w", err)
	}

	// Encode private key
	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	privateKeyPEMBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	}
	privateKeyPEM = pem.EncodeToMemory(privateKeyPEMBlock)

	// Encode public key
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal public key: %w", err)
	}
	publicKeyPEMBlock := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	}
	publicKeyPEM = pem.EncodeToMemory(publicKeyPEMBlock)

	return privateKeyPEM, publicKeyPEM, nil
}

// RegisterDomain calls SES to register the identity with the provided private key
func (s *sesService) RegisterDomain(ctx context.Context, domainName string, privateKeyPEM []byte) error {
	// For BYO-DKIM, AWS requires the private key to be a string
	privateKeyStr := string(privateKeyPEM)

	selector := "toro" // This will make the record toro._domainkey.domainName

	input := &sesv2.CreateEmailIdentityInput{
		EmailIdentity: aws.String(domainName),
		DkimSigningAttributes: &types.DkimSigningAttributes{
			DomainSigningPrivateKey: aws.String(privateKeyStr),
			DomainSigningSelector:   aws.String(selector),
		},
	}

	_, err := s.client.CreateEmailIdentity(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to create SES identity for %s: %w", domainName, err)
	}

	return nil
}

// GetDNSRecords returns the formatted TXT record instructions for the client
func (s *sesService) GetDNSRecords(domainName string, publicKeyPEM []byte) []string {
	// Strip PEM headers and newlines from public key
	// pubKeyStr := string(publicKeyPEM) // TODO: parse real string
	
	// A simple manual stripping (in production, we'd regex or parse it properly)
	// We want everything between -----BEGIN PUBLIC KEY----- and -----END PUBLIC KEY-----
	
	// Format as DKIM TXT record
	// Example: v=DKIM1; k=rsa; p=MIGfMA0GC...
	
	return []string{
		fmt.Sprintf("Host/Name: toro._domainkey.%s", domainName),
		"Type: TXT",
		"Value: v=DKIM1; k=rsa; p=<BASE64_PUBLIC_KEY>", // To be implemented fully
	}
}
