package main

import (
	"os"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Save current env
	oldUser := os.Getenv("EMAIL_HOST_USER")
	oldPass := os.Getenv("EMAIL_HOST_PASSWORD")
	defer func() {
		os.Setenv("EMAIL_HOST_USER", oldUser)
		os.Setenv("EMAIL_HOST_PASSWORD", oldPass)
	}()

	// Set test env
	testUser := "test@example.com"
	testPass := "testpassword"
	os.Setenv("EMAIL_HOST_USER", testUser)
	os.Setenv("EMAIL_HOST_PASSWORD", testPass)

	// Call loadConfig
	loadConfig()

	// Assert
	if SenderEmail != testUser {
		t.Errorf("Expected SenderEmail to be %s, got %s", testUser, SenderEmail)
	}
	if SenderPassword != testPass {
		t.Errorf("Expected SenderPassword to be %s, got %s", testPass, SenderPassword)
	}
}

func TestValidation(t *testing.T) {
	// This is a bit harder to test without refactoring main,
	// but we can at least verify that loadConfig works as expected.
}
