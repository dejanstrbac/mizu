package health

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func TestCheckS3Connection(t *testing.T) {
	// --- Test Cases ---
	tests := []struct {
		name           string
		handler        http.HandlerFunc
		client         *minio.Client // Use a specific client if handler is nil
		expectStatus   string
		expectContains string // Substring to check for in details
	}{
		{
			name: "Healthy - Bucket Exists",
			handler: func(w http.ResponseWriter, r *http.Request) {
				// MinIO BucketExists checks via GET /?location
				// Return minimal S3 XML response for bucket location
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusOK)
				// Return minimal valid XML
				w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`))
			},
			expectStatus: "healthy",
		},
		{
			name: "Unhealthy - Bucket Not Found",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			expectStatus:   "unhealthy",
			expectContains: "does not exist",
		},
		{
			name: "Unhealthy - S3 Server Error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			expectStatus:   "unhealthy",
			expectContains: "failed to check S3 bucket",
		},
		{
			name:           "Disabled - Nil Client",
			handler:        nil, // No server needed
			client:         nil,
			expectStatus:   "disabled",
			expectContains: "S3 client not initialized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var checker *CheckS3Connection
			bucketName := "test-bucket"

			if tt.handler != nil {
				// Setup mock S3 server
				server := httptest.NewServer(tt.handler)
				defer server.Close()

				// Create a MinIO client pointed at the mock server
				client, err := minio.New(server.Listener.Addr().String(), &minio.Options{
					Creds:  credentials.NewStaticV4("accesskey", "secretkey", ""),
					Secure: false, // Use HTTP for test server
				})
				if err != nil {
					t.Fatalf("Failed to create minio client: %v", err)
				}
				checker = NewCheckS3Connection(client, bucketName)
			} else {
				// Use the pre-configured client (for nil case)
				checker = NewCheckS3Connection(tt.client, bucketName)
			}

			// --- Act ---
			status := checker.CheckHealth()

			// --- Assert ---
			if status.Status != tt.expectStatus {
				t.Errorf("Expected status '%s', but got '%s'. Details: %+v", tt.expectStatus, status.Status, status.Details)
			}

			if tt.expectContains != "" {
				detailsStr := ""
				if detailsMap, ok := status.Details.(map[string]any); ok {
					if errStr, ok := detailsMap["error"].(string); ok {
						detailsStr = errStr
					}
				} else if details, ok := status.Details.(string); ok {
					detailsStr = details
				}

				if !contains(detailsStr, tt.expectContains) {
					t.Errorf("Expected details to contain '%s', but got '%s'", tt.expectContains, detailsStr)
				}
			}
		})
	}
}

// contains is a helper function to check for a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[0:len(substr)] == substr || len(s) > len(substr) && contains(s[1:], substr)
}
