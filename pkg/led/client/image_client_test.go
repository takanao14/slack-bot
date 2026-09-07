package client

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	imagev1 "github.com/takanao14/led-image-api/gen/go/image/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

type imageServiceServer struct {
	imagev1.UnimplementedImageServiceServer
	sendImage func(ctx context.Context, req *imagev1.SendImageRequest) (*imagev1.SendImageResponse, error)
}

func (s *imageServiceServer) SendImage(ctx context.Context, req *imagev1.SendImageRequest) (*imagev1.SendImageResponse, error) {
	if s.sendImage != nil {
		return s.sendImage(ctx, req)
	}
	return &imagev1.SendImageResponse{Success: true, Message: "ok"}, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func withDialOptions(t *testing.T, opts ...grpc.DialOption) {
	t.Helper()
	prev := additionalDialOptions
	additionalDialOptions = func() []grpc.DialOption {
		return opts
	}
	t.Cleanup(func() {
		additionalDialOptions = prev
	})
}

func startBufconnServer(t *testing.T, svc imagev1.ImageServiceServer) (*grpc.Server, *bufconn.Listener) {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	s := grpc.NewServer()
	imagev1.RegisterImageServiceServer(s, svc)
	go func() {
		_ = s.Serve(lis)
	}()
	t.Cleanup(func() {
		s.Stop()
		_ = lis.Close()
	})
	return s, lis
}

func TestNewImageClientConnectsToInMemoryServer(t *testing.T) {
	_, lis := startBufconnServer(t, &imageServiceServer{})

	withDialOptions(t,
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
	)

	c, err := NewImageClient("passthrough:///bufnet", time.Second, time.Second, testLogger())
	if err != nil {
		t.Fatalf("expected NewImageClient to connect, got error: %v", err)
	}
	if c == nil {
		t.Fatal("expected client instance")
	}
	t.Cleanup(func() { _ = c.Close() })
}

func TestNewImageClientTimesOutWhenConnectionNeverBecomesReady(t *testing.T) {
	withDialOptions(t,
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
	)

	_, err := NewImageClient("passthrough:///never-ready", 50*time.Millisecond, time.Second, testLogger())
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "failed to connect to gRPC server within timeout") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestSendImageSuccess(t *testing.T) {
	_, lis := startBufconnServer(t, &imageServiceServer{
		sendImage: func(_ context.Context, _ *imagev1.SendImageRequest) (*imagev1.SendImageResponse, error) {
			return &imagev1.SendImageResponse{Success: true, Message: "sent"}, nil
		},
	})

	withDialOptions(t,
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
	)

	c, err := NewImageClient("passthrough:///bufnet", time.Second, time.Second, testLogger())
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	resp, sendErr := c.SendImage(context.Background(), []byte("ppm"), "image/x-portable-pixmap", 5)
	if sendErr != nil {
		t.Fatalf("expected send success, got error: %v", sendErr)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("expected successful response, got %+v", resp)
	}
}

func TestSendImageReturnsErrorWhenServiceReturnsSuccessFalse(t *testing.T) {
	_, lis := startBufconnServer(t, &imageServiceServer{
		sendImage: func(_ context.Context, _ *imagev1.SendImageRequest) (*imagev1.SendImageResponse, error) {
			return &imagev1.SendImageResponse{Success: false, Message: "rejected"}, nil
		},
	})

	withDialOptions(t,
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
	)

	c, err := NewImageClient("passthrough:///bufnet", time.Second, time.Second, testLogger())
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	resp, sendErr := c.SendImage(context.Background(), []byte("ppm"), "image/x-portable-pixmap", 5)
	if sendErr == nil {
		t.Fatal("expected send error when success=false")
	}
	if resp == nil || resp.Success {
		t.Fatalf("expected response with success=false, got %+v", resp)
	}
}

func TestSendImageReturnsErrorWhenRPCFails(t *testing.T) {
	_, lis := startBufconnServer(t, &imageServiceServer{
		sendImage: func(_ context.Context, _ *imagev1.SendImageRequest) (*imagev1.SendImageResponse, error) {
			return nil, status.Error(codes.Internal, "boom")
		},
	})

	withDialOptions(t,
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
	)

	c, err := NewImageClient("passthrough:///bufnet", time.Second, time.Second, testLogger())
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	resp, sendErr := c.SendImage(context.Background(), []byte("ppm"), "image/x-portable-pixmap", 5)
	if sendErr == nil {
		t.Fatal("expected send error")
	}
	if resp != nil {
		t.Fatalf("expected nil response on RPC failure, got %+v", resp)
	}
}

func TestCloseWithNilConnectionIsNoop(t *testing.T) {
	c := &ImageClient{}
	if err := c.Close(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestCloseClosesOpenConnection(t *testing.T) {
	_, lis := startBufconnServer(t, &imageServiceServer{})

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to create grpc conn: %v", err)
	}
	conn.Connect()

	client := &ImageClient{conn: conn}
	if err := client.Close(); err != nil {
		t.Fatalf("expected close success, got error: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if conn.GetState() == connectivity.Shutdown {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected connection state Shutdown after close, got %s", conn.GetState())
}
