package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateLocalContextMap(t *testing.T) {
	pages := []PageContext{
		{
			Type:        "Receipt",
			Title:       "Fuel Invoice",
			Description: "Gasoline purchase",
			UUID:        "uuid-1234",
		},
		{
			Type:    "Invoice",
			Summary: "Legacy summary description",
			UUID:    "uuid-5678",
		},
	}

	localMap, pagesJSON := GenerateLocalContextMap(pages)

	// Validate map translation
	if localMap[1] != "uuid-1234" {
		t.Errorf("expected localMap[1] to be uuid-1234, got %s", localMap[1])
	}
	if localMap[2] != "uuid-5678" {
		t.Errorf("expected localMap[2] to be uuid-5678, got %s", localMap[2])
	}

	// Validate JSON output compatibility
	jsonStr := string(pagesJSON)
	if !strings.Contains(jsonStr, `"title":"Fuel Invoice"`) {
		t.Errorf("expected json to contain title: Fuel Invoice, got: %s", jsonStr)
	}
	// Verify description maps to summary for backward compatibility
	if !strings.Contains(jsonStr, `"summary":"Gasoline purchase"`) {
		t.Errorf("expected summary fallback from description, got: %s", jsonStr)
	}
	// Verify summary maps to description for forward compatibility
	if !strings.Contains(jsonStr, `"description":"Legacy summary description"`) {
		t.Errorf("expected description fallback from summary, got: %s", jsonStr)
	}
}

func TestWithTenantIDContext(t *testing.T) {
	ctx := context.Background()

	// Test empty context
	if id := TenantIDFromContext(ctx); id != "" {
		t.Errorf("expected empty string from context, got %q", id)
	}

	// Test populated context
	ctx = WithTenantID(ctx, "tenant-abc")
	if id := TenantIDFromContext(ctx); id != "tenant-abc" {
		t.Errorf("expected tenant-abc, got %q", id)
	}
}

func TestOKFDocumentFetcher(t *testing.T) {
	tenantID := "test-tenant-uuid"
	tempDir, err := os.MkdirTemp("", "toro-knowledge-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// We temporarily mock the current working directory or base path behavior by writing to the test execution path.
	// In the real system, pathing is relative to "docs/knowledge/<tenantID>".
	// Let's mock this structure inside the tempDir and change directory or mock the base path.
	// Wait! The fetcher is hardcoded to "docs/knowledge/<tenantID>/<path>".
	// So let's create a local "docs/knowledge/<tenantID>" subdirectory structure relative to the test run path.
	testBaseDir := filepath.Join("docs", "knowledge", tenantID)
	err = os.MkdirAll(testBaseDir, 0755)
	if err != nil {
		t.Fatalf("failed to create local test path structures: %v", err)
	}
	defer os.RemoveAll(testBaseDir)

	conceptPath := "playbooks/audit_rules.md"
	fullTestPath := filepath.Join(testBaseDir, conceptPath)
	err = os.MkdirAll(filepath.Dir(fullTestPath), 0755)
	if err != nil {
		t.Fatalf("failed to create parent dirs: %v", err)
	}

	content := `---
type: Playbook
title: Audit Playbook
description: Standard rules
---
# Actual Content
This is the raw markdown body of the document.`

	err = os.WriteFile(fullTestPath, []byte(content), 0644)
	if err != nil {
		t.Fatalf("failed to write test concept document: %v", err)
	}

	ctx := WithTenantID(context.Background(), tenantID)

	// Test Happy Path (with frontmatter stripping)
	body, err := OKFDocumentFetcher(ctx, conceptPath)
	if err != nil {
		t.Fatalf("fetcher returned unexpected error: %v", err)
	}
	expectedBody := "# Actual Content\nThis is the raw markdown body of the document."
	if body != expectedBody {
		t.Errorf("expected %q, got %q", expectedBody, body)
	}

	// Test Non-existent document
	_, err = OKFDocumentFetcher(ctx, "playbooks/does_not_exist.md")
	if err == nil {
		t.Error("expected error for non-existent document, got nil")
	}

	// Test Directory Traversal Prevention
	_, err = OKFDocumentFetcher(ctx, "../../../outside.md")
	if err == nil {
		t.Error("expected traversal error for relative path escaping context, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "directory traversal detected") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Test Missing Context Tenant ID
	_, err = OKFDocumentFetcher(context.Background(), conceptPath)
	if err == nil {
		t.Error("expected error for missing tenant ID in context, got nil")
	}
}

func TestScanOKFCatalog(t *testing.T) {
	tenantID := "test-scan-realm"
	testBaseDir := filepath.Join("docs", "knowledge", tenantID)
	err := os.MkdirAll(testBaseDir, 0755)
	if err != nil {
		t.Fatalf("failed to create path: %v", err)
	}
	defer os.RemoveAll(testBaseDir)

	// Create valid OKF file
	content1 := `---
type: Reference
title: Ref Doc
description: Ref desc
---
body1`
	err = os.WriteFile(filepath.Join(testBaseDir, "ref.md"), []byte(content1), 0644)
	if err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	// Create invalid OKF file (missing type)
	content2 := `---
title: Missing Type Doc
description: Missing type desc
---
body2`
	err = os.WriteFile(filepath.Join(testBaseDir, "invalid.md"), []byte(content2), 0644)
	if err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	// Create log/index files which should be ignored
	err = os.WriteFile(filepath.Join(testBaseDir, "index.md"), []byte(content1), 0644)
	if err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	ctx := context.Background()

	// Test parsing
	pages, err := ScanOKFCatalog(ctx, tenantID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pages) != 1 {
		t.Fatalf("expected exactly 1 parsed OKF page, got %d", len(pages))
	}

	p := pages[0]
	if p.Type != "Reference" || p.Title != "Ref Doc" || p.Description != "Ref desc" || p.UUID != "ref.md" {
		t.Errorf("unexpected parsed page context fields: %+v", p)
	}
}
