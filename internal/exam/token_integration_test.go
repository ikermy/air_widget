//go:build integration

package exam

import (
	"air_widget/internal/domain"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	rpcclient "github.com/ikermy/air_common/pkg/rpc"
)

func TestIntegration_WidgetTokenRPC(t *testing.T) {
	grpcHost := os.Getenv("GRPC_CONFIG_HOST")
	if grpcHost == "" {
		grpcHost = "airorc:50051"
	}
	t.Setenv("GRPC_CONFIG_HOST", grpcHost)

	serviceKeyFile := os.Getenv("SERVICE_KEY_FILE")
	if serviceKeyFile == "" {
		serviceKeyFile = "/run/secrets/.service_key"
	}
	if _, err := os.Stat(serviceKeyFile); err != nil {
		fallback := filepath.Join("..", "..", "..", "secrets", ".service_key")
		if _, fallbackErr := os.Stat(fallback); fallbackErr != nil {
			t.Skipf("service key file is not available at %s or %s: %v", serviceKeyFile, fallback, err)
		}
		serviceKeyFile = fallback
	}
	t.Setenv("SERVICE_KEY_FILE", serviceKeyFile)

	t.Logf("using grpc host %s and service key file %s", grpcHost, serviceKeyFile)

	client, err := rpcclient.New()
	if err != nil {
		t.Fatalf("create rpc client: %v", err)
	}
	defer func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Fatalf("close rpc client: %v", closeErr)
		}
	}()

	exam := &Exam{ctx: context.Background(), rpc: client}
	const userID uint32 = 42
	const respID uint64 = 777
	const ttl = time.Duration(domain.AuthTokenTTL) * time.Minute

	t.Logf("requesting token with ttl=%s", ttl)
	token, err := exam.helperNewToken(userID, respID, ttl)
	if err != nil {
		t.Fatalf("helperNewToken failed: %v", err)
	}
	if token == "" {
		t.Fatal("helperNewToken returned empty token")
	}
	t.Logf("received token: %s", token)

	parsedUserID, parsedRespID, err := exam.helperParseToken(token)
	if err != nil {
		t.Fatalf("helperParseToken failed: %v", err)
	}
	if parsedUserID != userID || parsedRespID != respID {
		t.Fatalf("unexpected parsed token values: userID=%d respID=%d", parsedUserID, parsedRespID)
	}
}
